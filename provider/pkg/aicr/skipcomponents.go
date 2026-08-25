// Copyright 2026, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// skipcomponents.go reconciles a validation run with the component subset a
// ClusterStack actually deployed (its skipComponents input).
//
// The SDK resolves and validates the FULL recipe; it has no notion of "the
// caller deployed a subset". Left alone, every check that probes a skipped
// component's namespace, Deployment, or Service fails — so a correctly
// deployed subset stack (bring-your-own cert-manager, a cloud whose managed
// GPU stack replaces gpu-operator/NFD/DRA) can never report status passed.
//
// Two mechanisms, both applied to the resolved recipe before ValidateState:
//
//  1. Skipped components are marked disabled (overrides.enabled=false), the
//     SDK's own representation of "present in the recipe, not deployed".
//     Component-aware validators honor ComponentRef.IsEnabled: platform-health
//     and expected-resources ignore disabled components, dra-support skips
//     itself, dependencyAffinity resolution treats them as absent.
//     Exception: the device-plugin advertiser components (gpu-operator,
//     gpu-operator-ocp) cannot be disabled — the SDK resolves its GPU
//     allocation policy from an ENABLED advertiser and fails closed without
//     one ("externally managed advertisers are a #1327 non-goal"). A skipped
//     advertiser is the externally-managed case by definition, so it stays
//     enabled for policy resolution but is made invisible to the composite
//     checks (no namespace, expected resources, or health asserts to probe —
//     inert because no validator name-probes these refs; see
//     externallyProvided for why the DRA driver is NOT treated this way).
//  2. Checks that hard-code a skipped component (see checkRequires) are
//     removed from the phase plan and reported as skipped with a reason,
//     instead of running and failing. They flow into the report and the
//     merged CTRF like any SDK-produced result.
//
// What this does NOT do: verify an externally managed replacement (e.g. a
// cloud-managed GPU operator in another namespace). A skipped component's
// checks are out of scope, not satisfied — that needs recipe-level ownership
// data upstream (AICR gpuStack profiles).
package aicr

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	aicrclient "github.com/NVIDIA/aicr/pkg/client/v1"
	"github.com/NVIDIA/aicr/pkg/recipe"
	"github.com/NVIDIA/aicr/pkg/validator/ctrf"
)

// checkRequires maps validator-catalog check names to the recipe components
// the check cannot produce a meaningful verdict without — components it
// hard-codes (namespace, Deployment, or Service name) rather than discovers
// from the recipe. A check is pre-skipped when ANY listed component is
// skipped.
//
// Only checks that would otherwise FAIL spuriously belong here. Checks that
// already consult ComponentRef.IsEnabled (platform-health, expected-resources,
// dra-support, robust-controller, inference-gateway, slinky-*) are left to the
// SDK, which skips or scopes them itself once the component is disabled.
//
// Pinned against SDK v0.18.0's validators/ sources and guarded by
// TestCheckRequiresMatchesCatalog, which fails the build if an SDK bump
// renames a check or a component so the table cannot silently rot.
var checkRequires = map[string][]string{
	// validators/deployment: operator_health.go, gpu_operator_version.go list
	// gpu-operator pods/Deployments in the gpu-operator namespace.
	"operator-health":      {"gpu-operator"},
	"gpu-operator-version": {"gpu-operator"},
	// validators/conformance: gpu_operator_health_check.go probes the
	// gpu-operator Deployment and the nvidia-dcgm-exporter DaemonSet.
	"gpu-operator-health": {"gpu-operator"},
	// accelerator_metrics_check.go dials
	// nvidia-dcgm-exporter.gpu-operator.svc — DCGM exporter is a gpu-operator
	// operand.
	"accelerator-metrics": {"gpu-operator"},
	// ai_service_metrics_check.go queries DCGM_FI_DEV_GPU_UTIL through the
	// kube-prometheus-stack Prometheus; both the exporter and the scraper
	// must be recipe-deployed for the series to exist.
	"ai-service-metrics": {"gpu-operator", "kube-prometheus-stack"},
	// pod_autoscaling_check.go drives an HPA off prometheus-adapter's custom
	// metrics API, fed by Prometheus scraping DCGM in the gpu-operator
	// namespace.
	"pod-autoscaling": {"gpu-operator", "kube-prometheus-stack", "prometheus-adapter"},
	// cluster_autoscaling_check.go rides the same chain: with Karpenter
	// present it scales an HPA on the external metric dcgm_gpu_power_usage
	// (DCGM -> kube-prometheus-stack -> prometheus-adapter); its no-Karpenter
	// EKS/GKE fallbacks still select GPU nodes by the GFD label, a
	// gpu-operator operand. Requiring all three over-skips only the fallback
	// paths (which need no metrics), a conservative trade matching
	// pod-autoscaling; without it, Karpenter clusters fail on the missing
	// metric when any of the three is skipped.
	"cluster-autoscaling": {"gpu-operator", "kube-prometheus-stack", "prometheus-adapter"},
	// gang_scheduling_check.go asserts the kai-scheduler Deployments and
	// schedules pods with schedulerName kai-scheduler.
	"gang-scheduling": {"kai-scheduler"},
	// dra_support_check.go would self-skip once nvidia-dra-driver-gpu is
	// disabled; pre-skipping here short-circuits before its validator Job is
	// even deployed and keeps the reason message uniform.
	"dra-support": {"nvidia-dra-driver-gpu"},
}

// externallyProvided lists the components the SDK's GPU allocation policy
// resolver (pkg/validator/v1/allocation_policy.go) reads its device-plugin
// advertiser from. It requires an ENABLED advertiser and fails closed with
// "no whole-GPU advertiser" otherwise, so these cannot be disabled when
// skipped. They stay enabled — which also keeps the policy the recipe
// intends, correct when the same stack is run by the platform — and are
// blanked so the composite checks have nothing of theirs to probe. Blanking
// is inert for exactly these components: no validator name-probes their
// ComponentRef (platform-health and expected-resources skip a ref with no
// namespace, expected resources, or health asserts).
//
// nvidia-dra-driver-gpu is deliberately NOT here, although the resolver
// reads it too. A skipped DRA driver is disabled like any other component:
// the resolver natively maps a disabled DRA ref to the device-plugin path
// (needing only an enabled operator ref, which the blanking above
// preserves), and blanking would NOT be inert — expected-resources
// name-probes the DRA ref (verifyDRAKubeletPluginReady with ref.Namespace),
// and client-go treats an empty namespace as ALL namespaces: ≥2 matching
// DaemonSets cluster-wide error as ambiguous, zero fail after the full GPU
// readiness poll, and exactly one silently validates the externally managed
// replacement — contradicting the skipped-means-out-of-scope contract.
// Residual edge, accepted: a recipe that pins the DRA driver as the
// advertiser (resources.gpus.enabled=true, operator devicePlugin.enabled
// pinned false) with ONLY the DRA driver skipped now fails closed in the
// resolver with its explicit no-advertiser message instead of validating.
// No embedded recipe configures that today, and fail-closed beats silently
// mis-validating.
var externallyProvided = map[string]bool{
	"gpu-operator":     true,
	"gpu-operator-ocp": true,
}

// preSkippedCheck is a declared check removed from the run because a
// component it requires was skipped.
type preSkippedCheck struct {
	Name   string
	Phase  string
	Reason string
}

// applySkipComponents reconciles a resolved recipe with skipComponents for
// the given phases: skipped components are marked disabled (or, for the
// whole-GPU advertisers, kept enabled but blanked — see externallyProvided)
// and checks that require them are removed from those phases' plans. It
// returns the removed checks in declaration order (nil when nothing was
// skipped).
//
// Copy-on-write: the result's ComponentRefs slice, each touched ComponentRef's
// Overrides map, the Validation config, and each touched phase's Checks slice
// are replaced with copies. Nothing reachable from the input before the call
// is mutated, so embedded/cached recipe data the SDK may share with the
// result stays pristine. r itself is updated in place — it is the per-resolve
// result the caller owns and hands to ValidateState.
func applySkipComponents(r *recipe.RecipeResult, skipComponents, phases []string) []preSkippedCheck {
	skip := skipSet(skipComponents)
	if r == nil || len(skip) == 0 {
		return nil
	}

	// 1. Disable skipped components (ref is a value copy; only its map is
	// replaced, so the input slice's elements are untouched).
	matched := make(map[string]bool, len(skip))
	refs := make([]recipe.ComponentRef, len(r.ComponentRefs))
	for i, ref := range r.ComponentRefs {
		if skip[ref.Name] {
			matched[ref.Name] = true
			if externallyProvided[ref.Name] {
				ref.Namespace = ""
				ref.ExpectedResources = nil
				ref.HealthCheckAsserts = ""
				ref.HealthCheckSkip = true
			} else {
				overrides := make(map[string]interface{}, len(ref.Overrides)+1)
				for k, v := range ref.Overrides {
					overrides[k] = v
				}
				overrides["enabled"] = false
				ref.Overrides = overrides
			}
		}
		refs[i] = ref
	}
	r.ComponentRefs = refs
	for _, name := range sortedKeys(skip) {
		if !matched[name] {
			// Mirror ClusterStack, which ignores unknown skip names rather
			// than failing: the recipe simply has nothing to leave out.
			slog.Warn("skipComponents entry matches no component in the resolved recipe; ignoring it for validation",
				"component", name)
		}
	}

	// 2. Prune checks that presuppose a skipped component.
	if r.Validation == nil {
		return nil
	}
	cfg := *r.Validation
	var pre []preSkippedCheck
	for _, phase := range phases {
		slot := phaseSlot(&cfg, phase)
		if slot == nil || *slot == nil {
			continue
		}
		kept := make([]string, 0, len((*slot).Checks))
		for _, name := range (*slot).Checks {
			if missing := requiredSkipped(name, skip); len(missing) > 0 {
				pre = append(pre, preSkippedCheck{Name: name, Phase: phase, Reason: skipReason(missing)})
				continue
			}
			kept = append(kept, name)
		}
		if len(kept) != len((*slot).Checks) {
			vp := **slot
			vp.Checks = kept
			*slot = &vp
		}
	}
	r.Validation = &cfg
	return pre
}

// requiredSkipped returns the required components of check that are in
// skip, sorted, or nil when the check is not in checkRequires or none of its
// requirements are skipped.
func requiredSkipped(check string, skip map[string]bool) []string {
	var missing []string
	for _, comp := range checkRequires[check] {
		if skip[comp] {
			missing = append(missing, comp)
		}
	}
	sort.Strings(missing)
	return missing
}

// skipReason is the CTRF message for a pre-skipped check.
func skipReason(missing []string) string {
	noun := "component"
	if len(missing) > 1 {
		noun = "components"
	}
	return fmt.Sprintf("skipped: requires %s %s, which the stack did not deploy (skipComponents); "+
		"nothing to validate", noun, strings.Join(missing, ", "))
}

// phaseSlot returns the address of the ValidationConfig field for a canonical
// phase name, or nil for an unknown phase.
func phaseSlot(cfg *recipe.ValidationConfig, phase string) **recipe.ValidationPhase {
	switch phase {
	case string(aicrclient.PhaseDeployment):
		return &cfg.Deployment
	case string(aicrclient.PhaseConformance):
		return &cfg.Conformance
	case string(aicrclient.PhasePerformance):
		return &cfg.Performance
	}
	return nil
}

// skipSet builds a lookup set from skipComponents, dropping blank entries.
func skipSet(skipComponents []string) map[string]bool {
	set := make(map[string]bool, len(skipComponents))
	for _, name := range skipComponents {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = true
		}
	}
	return set
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// preSkippedPhaseResults renders pre-skipped checks as one CTRF phase report
// per phase (in first-seen phase order), so they flow through buildReport —
// and into the merged CTRF — exactly like SDK-produced results.
func preSkippedPhaseResults(pre []preSkippedCheck) []*aicrclient.PhaseResult {
	if len(pre) == 0 {
		return nil
	}
	builders := map[string]*ctrf.Builder{}
	var order []string
	for _, c := range pre {
		b, ok := builders[c.Phase]
		if !ok {
			b = ctrf.NewBuilder("pulumi-nvidia-aicr", sdkModuleVersion(), c.Phase)
			builders[c.Phase] = b
			order = append(order, c.Phase)
		}
		b.AddSkipped(c.Name, c.Phase, c.Reason)
	}
	out := make([]*aicrclient.PhaseResult, 0, len(order))
	for _, phase := range order {
		out = append(out, &aicrclient.PhaseResult{
			Phase:  aicrclient.Phase(phase),
			Status: ctrf.StatusSkipped,
			Report: builders[phase].Build(),
		})
	}
	return out
}

// mergePreSkipped folds pre-skipped checks into the SDK's phase results so
// each is reported exactly once, skipped, with the provider's reason:
//
//   - a check the SDK still reported is overwritten in place (status skipped,
//     our reason, summary adjusted). The live path never reports a pruned
//     check, but the SDK's no-cluster mode emits every catalog entry as
//     "skipped - no-cluster mode" regardless of the recipe's check list;
//   - the rest are rendered as one synthetic CTRF report per phase, placed
//     right after that phase's SDK result (appended when the SDK reported
//     nothing for the phase).
func mergePreSkipped(results []*aicrclient.PhaseResult, pre []preSkippedCheck) []*aicrclient.PhaseResult {
	if len(pre) == 0 {
		return results
	}
	var remaining []preSkippedCheck
	for _, c := range pre {
		if !overrideReported(results, c) {
			remaining = append(remaining, c)
		}
	}
	return appendSyntheticResults(results, preSkippedPhaseResults(remaining))
}

// overrideReported rewrites an already-reported check (same phase and name)
// to skipped with c's reason, keeping the report summary consistent. It
// reports whether a match was found.
func overrideReported(results []*aicrclient.PhaseResult, c preSkippedCheck) bool {
	for _, pr := range results {
		if pr == nil || pr.Report == nil || string(pr.Phase) != c.Phase {
			continue
		}
		tests := pr.Report.Results.Tests
		for i := range tests {
			if tests[i].Name != c.Name {
				continue
			}
			adjustSummary(&pr.Report.Results.Summary, tests[i].Status, -1)
			tests[i].Status = ctrf.StatusSkipped
			tests[i].Message = c.Reason
			adjustSummary(&pr.Report.Results.Summary, ctrf.StatusSkipped, +1)
			return true
		}
	}
	return false
}

// adjustSummary moves one test between CTRF summary buckets (delta ±1);
// Tests stays constant because the test is rewritten, not added.
func adjustSummary(s *ctrf.Summary, status string, delta int) {
	switch status {
	case ctrf.StatusPassed:
		s.Passed += delta
	case ctrf.StatusFailed:
		s.Failed += delta
	case ctrf.StatusSkipped:
		s.Skipped += delta
	case ctrf.StatusPending:
		s.Pending += delta
	default:
		s.Other += delta
	}
}

// appendSyntheticResults interleaves synthetic phase results after the SDK
// result for the same phase, keeping each phase's checks together; synthetic
// results for phases the SDK did not report are appended.
func appendSyntheticResults(results, synthetic []*aicrclient.PhaseResult) []*aicrclient.PhaseResult {
	if len(synthetic) == 0 {
		return results
	}
	byPhase := make(map[aicrclient.Phase]*aicrclient.PhaseResult, len(synthetic))
	for _, s := range synthetic {
		byPhase[s.Phase] = s
	}
	merged := make([]*aicrclient.PhaseResult, 0, len(results)+len(synthetic))
	for _, pr := range results {
		merged = append(merged, pr)
		if pr == nil {
			continue
		}
		if s, ok := byPhase[pr.Phase]; ok {
			merged = append(merged, s)
			delete(byPhase, pr.Phase)
		}
	}
	for _, s := range synthetic {
		if _, pending := byPhase[s.Phase]; pending {
			merged = append(merged, s)
			delete(byPhase, s.Phase)
		}
	}
	return merged
}
