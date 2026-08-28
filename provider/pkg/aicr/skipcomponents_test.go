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

package aicr

import (
	"encoding/json"
	"sort"
	"testing"

	aicrclient "github.com/NVIDIA/aicr/pkg/client/v1"
	"github.com/NVIDIA/aicr/pkg/recipe"
	"github.com/NVIDIA/aicr/pkg/validator/catalog"
	"github.com/NVIDIA/aicr/pkg/validator/ctrf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eksTrainingCriteria resolves to a recipe that declares the gpu-operator,
// metrics, and gang-scheduling checks checkRequires covers.
var eksTrainingCriteria = Criteria{
	Service:     "eks",
	Accelerator: "h100",
	Intent:      "training",
}

// TestCheckRequiresMatchesCatalog is the SDK-bump tripwire for the pinned
// table: every check name must exist in the embedded validator catalog (in a
// phase the provider can run), and every required component must be a real
// component of an embedded recipe that declares that check.
func TestCheckRequiresMatchesCatalog(t *testing.T) {
	cat, err := catalog.LoadWithDataProvider(t.Context(), nil, "", "")
	require.NoError(t, err)
	phaseOf := make(map[string]string, len(cat.Validators))
	for _, v := range cat.Validators {
		phaseOf[v.Name] = v.Phase
	}

	resolved, err := Resolve(t.Context(), eksTrainingCriteria)
	require.NoError(t, err)
	components := make(map[string]bool, len(resolved.Components))
	for _, c := range resolved.Components {
		components[c.Name] = true
	}

	for check, requires := range checkRequires {
		phase, ok := phaseOf[check]
		assert.True(t, ok, "check %q is not in the SDK validator catalog — renamed upstream?", check)
		assert.Contains(t, []string{"deployment", "conformance", "performance"}, phase, "check %q phase", check)
		require.NotEmpty(t, requires, "check %q must require at least one component", check)
		for _, comp := range requires {
			assert.True(t, components[comp],
				"check %q requires component %q, which the %s recipe does not declare — renamed upstream?",
				check, comp, resolved.Name)
		}
	}
}

// TestApplySkipComponents covers the pure reconciliation: disabled refs,
// pruned checks with reasons, untouched phases, copy-on-write on the input,
// and unknown names being ignored.
func TestApplySkipComponents(t *testing.T) {
	newResult := func() *recipe.RecipeResult {
		return &recipe.RecipeResult{
			ComponentRefs: []recipe.ComponentRef{
				{
					Name: "gpu-operator", Namespace: "gpu-operator",
					Overrides:          map[string]interface{}{"driver": map[string]interface{}{"enabled": true}},
					ExpectedResources:  []recipe.ExpectedResource{{Kind: "ClusterPolicy", Name: "cluster-policy"}},
					HealthCheckAsserts: "apiVersion: v1\nkind: Pod\n",
				},
				{Name: "kube-prometheus-stack", Namespace: "monitoring", Overrides: map[string]interface{}{"grafana": map[string]interface{}{"enabled": true}}},
				{Name: "kai-scheduler", Namespace: "kai-scheduler"},
			},
			Validation: &recipe.ValidationConfig{
				Deployment: &recipe.ValidationPhase{
					Checks: []string{"operator-health", "expected-resources", "gpu-operator-version", "check-nvidia-smi"},
				},
				Conformance: &recipe.ValidationPhase{
					Checks: []string{"platform-health", "gang-scheduling", "accelerator-metrics", "ai-service-metrics", "pod-autoscaling", "cluster-autoscaling"},
				},
				Performance: &recipe.ValidationPhase{
					Checks: []string{"nccl-all-reduce-bw"},
				},
			},
		}
	}

	t.Run("disables refs and prunes requiring checks", func(t *testing.T) {
		r := newResult()
		origRefs := r.ComponentRefs
		origOverrides := origRefs[1].Overrides
		origValidation := r.Validation
		origDeployChecks := r.Validation.Deployment.Checks

		pre := applySkipComponents(r, []string{"gpu-operator", "kube-prometheus-stack"}, []string{"deployment", "conformance"})

		// Refs: a skipped advertiser stays enabled (policy resolution) but is
		// blanked; any other skipped component is disabled; the rest untouched.
		require.Len(t, r.ComponentRefs, 3)
		gpuOp := r.ComponentRefs[0]
		assert.True(t, gpuOp.IsEnabled(), "gpu-operator (advertiser) must stay enabled")
		assert.Empty(t, gpuOp.Namespace, "advertiser namespace blanked so composite checks do not probe it")
		assert.Empty(t, gpuOp.ExpectedResources)
		assert.Empty(t, gpuOp.HealthCheckAsserts)
		assert.True(t, gpuOp.HealthCheckSkip)
		assert.Equal(t, map[string]interface{}{"enabled": true}, gpuOp.Overrides["driver"], "advertiser overrides untouched")
		assert.False(t, r.ComponentRefs[1].IsEnabled(), "kube-prometheus-stack must be disabled")
		assert.Equal(t, map[string]interface{}{"enabled": true}, r.ComponentRefs[1].Overrides["grafana"], "existing overrides must survive")
		assert.True(t, r.ComponentRefs[2].IsEnabled(), "kai-scheduler must stay enabled")
		assert.Equal(t, "kai-scheduler", r.ComponentRefs[2].Namespace)

		// Checks: only checks requiring a skipped component are pruned.
		assert.Equal(t, []string{"expected-resources", "check-nvidia-smi"}, r.Validation.Deployment.Checks)
		assert.Equal(t, []string{"platform-health", "gang-scheduling"}, r.Validation.Conformance.Checks)
		assert.Equal(t, []string{"nccl-all-reduce-bw"}, r.Validation.Performance.Checks, "unrequested phase untouched")

		names := make([]string, 0, len(pre))
		for _, c := range pre {
			names = append(names, c.Phase+"/"+c.Name)
			assert.Contains(t, c.Reason, "skipComponents")
		}
		assert.Equal(t, []string{
			"deployment/operator-health", "deployment/gpu-operator-version",
			"conformance/accelerator-metrics", "conformance/ai-service-metrics", "conformance/pod-autoscaling", "conformance/cluster-autoscaling",
		}, names, "pre-skipped checks in declaration order")
		var aiMetrics preSkippedCheck
		for _, c := range pre {
			if c.Name == "ai-service-metrics" {
				aiMetrics = c
			}
		}
		assert.Contains(t, aiMetrics.Reason, "components gpu-operator, kube-prometheus-stack",
			"every skipped requirement is named, sorted")

		// Copy-on-write: nothing reachable from the original input changed.
		assert.Equal(t, "gpu-operator", origRefs[0].Namespace, "input ComponentRefs slice must not be mutated")
		assert.NotEmpty(t, origRefs[0].ExpectedResources)
		assert.True(t, origRefs[1].IsEnabled(), "input ComponentRefs slice must not be mutated")
		_, hasEnabled := origOverrides["enabled"]
		assert.False(t, hasEnabled, "input Overrides map must not be mutated")
		assert.Equal(t, []string{"operator-health", "expected-resources", "gpu-operator-version", "check-nvidia-smi"},
			origDeployChecks, "input Checks slice must not be mutated")
		assert.Equal(t, []string{"operator-health", "expected-resources", "gpu-operator-version", "check-nvidia-smi"},
			origValidation.Deployment.Checks, "input ValidationConfig must not be mutated")
	})

	t.Run("no skip is a no-op", func(t *testing.T) {
		r := newResult()
		before := r.ComponentRefs
		assert.Nil(t, applySkipComponents(r, nil, []string{"deployment"}))
		assert.Nil(t, applySkipComponents(r, []string{""}, []string{"deployment"}))
		assert.Equal(t, before, r.ComponentRefs)
		assert.Len(t, r.Validation.Deployment.Checks, 4)
	})

	t.Run("names match verbatim, mirroring ApplyOverrides", func(t *testing.T) {
		// " gpu-operator" (stray space) deploys gpu-operator — ApplyOverrides
		// matches verbatim — so validation must NOT treat it as skipped:
		// trimming here would report a deployed component's checks skipped,
		// masking real failures.
		r := newResult()
		pre := applySkipComponents(r, []string{" gpu-operator"}, []string{"deployment", "conformance"})
		assert.Nil(t, pre)
		assert.Equal(t, "gpu-operator", r.ComponentRefs[0].Namespace, "gpu-operator must be untouched")
		assert.Len(t, r.Validation.Deployment.Checks, 4)
	})

	t.Run("dra driver is disabled, not blanked, and pre-skips dra-support", func(t *testing.T) {
		// Unlike the operator advertisers, a skipped DRA driver must be
		// DISABLED: the policy resolver natively maps a disabled DRA ref to
		// the device-plugin path, and blanking would not be inert —
		// expected-resources probes the DRA ref's namespace, and an empty
		// namespace lists DaemonSets across ALL namespaces (review finding
		// on #23).
		r := newResult()
		r.ComponentRefs = append(r.ComponentRefs, recipe.ComponentRef{Name: "nvidia-dra-driver-gpu", Namespace: "nvidia-dra-driver"})
		r.Validation.Conformance.Checks = append(r.Validation.Conformance.Checks, "dra-support")
		pre := applySkipComponents(r, []string{"nvidia-dra-driver-gpu"}, []string{"conformance"})
		dra := r.ComponentRefs[3]
		assert.False(t, dra.IsEnabled(), "skipped DRA driver must be disabled so component-aware checks drop it")
		assert.Equal(t, "nvidia-dra-driver", dra.Namespace, "namespace must be left intact, never blanked to empty")
		require.Len(t, pre, 1)
		assert.Equal(t, "dra-support", pre[0].Name)
		assert.Contains(t, pre[0].Reason, "nvidia-dra-driver-gpu")
		assert.NotContains(t, r.Validation.Conformance.Checks, "dra-support")
	})

	t.Run("unknown component is ignored", func(t *testing.T) {
		r := newResult()
		pre := applySkipComponents(r, []string{"no-such-component"}, []string{"deployment", "conformance"})
		assert.Nil(t, pre)
		for _, ref := range r.ComponentRefs {
			assert.True(t, ref.IsEnabled(), "%s must stay enabled", ref.Name)
		}
		assert.Len(t, r.Validation.Deployment.Checks, 4)
		assert.Len(t, r.Validation.Conformance.Checks, 6)
	})

	t.Run("nil validation and nil result are safe", func(t *testing.T) {
		r := newResult()
		r.Validation = nil
		assert.Nil(t, applySkipComponents(r, []string{"kube-prometheus-stack"}, []string{"deployment"}))
		assert.False(t, r.ComponentRefs[1].IsEnabled(), "refs are still disabled without a validation block")
		assert.Nil(t, applySkipComponents(nil, []string{"gpu-operator"}, []string{"deployment"}))
	})
}

// TestPreSkippedPhaseResultsAndMerge covers the CTRF rendering of pre-skipped
// checks and their interleaving with SDK phase results.
func TestPreSkippedPhaseResultsAndMerge(t *testing.T) {
	pre := []preSkippedCheck{
		{Name: "operator-health", Phase: "deployment", Reason: "r1"},
		{Name: "accelerator-metrics", Phase: "conformance", Reason: "r2"},
		{Name: "gpu-operator-version", Phase: "deployment", Reason: "r3"},
	}
	synthetic := preSkippedPhaseResults(pre)
	require.Len(t, synthetic, 2, "one report per phase, first-seen order")
	assert.Equal(t, aicrclient.PhaseDeployment, synthetic[0].Phase)
	assert.Equal(t, aicrclient.PhaseConformance, synthetic[1].Phase)
	require.NotNil(t, synthetic[0].Report)
	assert.Equal(t, 2, synthetic[0].Report.Results.Summary.Skipped)
	assert.Equal(t, 2, synthetic[0].Report.Results.Summary.Tests)
	assert.Zero(t, synthetic[0].Report.Results.Summary.Failed)
	assert.Equal(t, "operator-health", synthetic[0].Report.Results.Tests[0].Name)
	assert.Equal(t, ctrf.StatusSkipped, synthetic[0].Report.Results.Tests[0].Status)
	assert.Equal(t, "r1", synthetic[0].Report.Results.Tests[0].Message)

	assert.Nil(t, preSkippedPhaseResults(nil))

	// Append: synthetic lands right after its SDK phase; unmatched phases append.
	sdkDeploy := &aicrclient.PhaseResult{Phase: aicrclient.PhaseDeployment, Report: ctrf.NewBuilder("t", "v", "deployment").Build()}
	sdkPerf := &aicrclient.PhaseResult{Phase: aicrclient.PhasePerformance, Report: ctrf.NewBuilder("t", "v", "performance").Build()}
	merged := appendSyntheticResults([]*aicrclient.PhaseResult{sdkDeploy, nil, sdkPerf}, synthetic)
	require.Len(t, merged, 5)
	assert.Same(t, sdkDeploy, merged[0])
	assert.Same(t, synthetic[0], merged[1], "deployment synthetic follows the SDK deployment result")
	assert.Nil(t, merged[2], "nil SDK entries pass through untouched")
	assert.Same(t, sdkPerf, merged[3])
	assert.Same(t, synthetic[1], merged[4], "conformance synthetic appended (no SDK conformance result)")

	same := []*aicrclient.PhaseResult{sdkDeploy}
	assert.Equal(t, same, appendSyntheticResults(same, nil), "no synthetic → input returned as-is")
}

// TestMergePreSkippedOverridesReported: a pre-skipped check the SDK still
// reported (no-cluster mode, or a defensive live case) is rewritten in place
// — once, skipped, our reason, summary adjusted — while checks the SDK did
// not report are appended as synthetic results.
func TestMergePreSkippedOverridesReported(t *testing.T) {
	b := ctrf.NewBuilder("sdk", "v", "deployment")
	b.AddSkipped("operator-health", "deployment", "skipped - no-cluster mode")
	b.AddSkipped("check-nvidia-smi", "deployment", "skipped - no-cluster mode")
	b.AddResult(&ctrf.ValidatorResult{Name: "gpu-operator-version", Phase: "deployment", ExitCode: 1, TerminationMsg: "boom"})
	sdkDeploy := &aicrclient.PhaseResult{Phase: aicrclient.PhaseDeployment, Report: b.Build()}
	require.Equal(t, 1, sdkDeploy.Report.Results.Summary.Failed, "fixture: one failed before merge")

	pre := []preSkippedCheck{
		{Name: "operator-health", Phase: "deployment", Reason: "ours-1"},
		{Name: "gpu-operator-version", Phase: "deployment", Reason: "ours-2"},
		{Name: "accelerator-metrics", Phase: "conformance", Reason: "ours-3"},
	}
	merged := mergePreSkipped([]*aicrclient.PhaseResult{sdkDeploy}, pre)
	require.Len(t, merged, 2, "one SDK result + one synthetic conformance result")
	assert.Same(t, sdkDeploy, merged[0])

	tests := merged[0].Report.Results.Tests
	require.Len(t, tests, 3, "override must not add entries")
	assert.Equal(t, "ours-1", tests[0].Message)
	assert.Equal(t, ctrf.StatusSkipped, tests[0].Status)
	assert.Equal(t, "skipped - no-cluster mode", tests[1].Message, "untouched check keeps its message")
	assert.Equal(t, ctrf.StatusSkipped, tests[2].Status, "failed → skipped")
	assert.Equal(t, "ours-2", tests[2].Message)
	sum := merged[0].Report.Results.Summary
	assert.Equal(t, 3, sum.Tests)
	assert.Equal(t, 3, sum.Skipped)
	assert.Zero(t, sum.Failed)

	assert.Equal(t, aicrclient.PhaseConformance, merged[1].Phase)
	require.Len(t, merged[1].Report.Results.Tests, 1)
	assert.Equal(t, "accelerator-metrics", merged[1].Report.Results.Tests[0].Name)
	assert.Equal(t, "ours-3", merged[1].Report.Results.Tests[0].Message)

	assert.Equal(t, []*aicrclient.PhaseResult{sdkDeploy}, mergePreSkipped([]*aicrclient.PhaseResult{sdkDeploy}, nil))
}

// TestValidateNoClusterSkipComponents drives the full adapter path offline
// with skipComponents: pre-skipped checks must appear as skipped with the
// provider's reason, the remaining checks must still be present (NoCluster
// skips them SDK-side), the rollup must pass, and the merged CTRF must agree
// with the counts — proving the synthetic results are first-class.
func TestValidateNoClusterSkipComponents(t *testing.T) {
	report, err := Validate(t.Context(), eksTrainingCriteria, ValidateOptions{
		NoCluster:      true,
		Phases:         []string{"deployment", "conformance"},
		SkipComponents: []string{"gpu-operator", "nvidia-dra-driver-gpu", "not-a-component"},
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, OutcomePassed, report.Outcome)

	byName := map[string]CheckOutcome{}
	for _, c := range report.Checks {
		byName[c.Name] = c
		assert.Equal(t, "skipped", c.Status, "check %s", c.Name)
	}
	for _, name := range []string{"operator-health", "gpu-operator-version", "gpu-operator-health", "accelerator-metrics", "cluster-autoscaling", "dra-support"} {
		c, ok := byName[name]
		require.True(t, ok, "pre-skipped check %s must still be reported", name)
		assert.Contains(t, c.Message, "skipComponents", "check %s must carry the provider reason", name)
		if name == "dra-support" {
			assert.Contains(t, c.Message, "nvidia-dra-driver-gpu", "check %s must name the skipped component", name)
		} else {
			assert.Contains(t, c.Message, "gpu-operator", "check %s must name the skipped component", name)
		}
	}
	for _, name := range []string{"check-nvidia-smi", "platform-health"} {
		c, ok := byName[name]
		require.True(t, ok, "check %s must still run (SDK-side skip in NoCluster)", name)
		assert.NotContains(t, c.Message, "skipComponents", "check %s must not be pre-skipped", name)
	}

	assert.Equal(t, len(report.Checks), report.Skipped)
	assert.Zero(t, report.Passed)
	assert.Zero(t, report.Failed)
	assert.Zero(t, report.Other)

	require.NotEmpty(t, report.CTRFReport)
	var merged ctrf.Report
	require.NoError(t, json.Unmarshal([]byte(report.CTRFReport), &merged))
	assert.Equal(t, report.Skipped, merged.Results.Summary.Skipped)
	assert.Equal(t, len(report.Checks), merged.Results.Summary.Tests)
	ctrfNames := make([]string, 0, len(merged.Results.Tests))
	for _, tr := range merged.Results.Tests {
		ctrfNames = append(ctrfNames, tr.Name)
	}
	sort.Strings(ctrfNames)
	reportNames := make([]string, 0, len(report.Checks))
	for _, c := range report.Checks {
		reportNames = append(reportNames, c.Name)
	}
	sort.Strings(reportNames)
	assert.Equal(t, reportNames, ctrfNames, "merged CTRF must list exactly the reported checks")

	// Same run without skipComponents: every check still present, no
	// provider reason anywhere — the baseline TestValidateNoClusterFullWiring
	// contract is untouched.
	baseline, err := Validate(t.Context(), eksTrainingCriteria, ValidateOptions{
		NoCluster: true,
		Phases:    []string{"deployment", "conformance"},
	})
	require.NoError(t, err)
	assert.Equal(t, len(baseline.Checks), len(report.Checks), "pre-skipping must not drop checks from the report")
	for _, c := range baseline.Checks {
		assert.NotContains(t, c.Message, "skipComponents")
	}
}
