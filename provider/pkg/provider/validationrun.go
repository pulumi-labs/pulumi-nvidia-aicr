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

// validationrun.go implements nvidia-aicr:index:ValidationRun, a custom
// resource that runs an AICR recipe's empirical validation (snapshot +
// deployment/conformance/performance checks) against a live cluster at
// create time and records structured per-check results in Pulumi state.
//
// Lifecycle semantics (see docs/design/002-validation-run-implementation.md):
//   - Preview never runs anything.
//   - Any input change replaces the resource (fresh run) — owned by the
//     explicit Diff, which marks every changed top-level property
//     UpdateReplace. Delete is free (no-op), so DeleteBeforeReplace is set.
//   - strict: true failures return infer.ResourceInitFailedError WITH full
//     state, so results persist while the update fails. The engine then
//     forces an Update on every subsequent `pulumi up` (init-error repair)
//     until validation passes — which is why Update is implemented and
//     re-runs validation via the same runValidation body as Create.
//   - Delete is a deliberate no-op: the SDK reaps its own cluster-side
//     artifacts per run; the aicr-validation namespace (and a leftover
//     aicr-snapshot ConfigMap) persist by SDK design.
package provider

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	corev1 "k8s.io/api/core/v1"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/aicr"
)

// supportedPhases is the allowlist for the `phases` input.
var supportedPhases = []string{"deployment", "conformance", "performance"}

// maxTimeoutMinutes caps the run timeout at 24 hours — far above any real
// validation run, and small enough that the minutes-to-Duration conversion
// can never overflow into an already-expired deadline.
const maxTimeoutMinutes = 24 * 60

// defaultPhases is what runs when `phases` is unset: performance is an
// explicit opt-in (materially longer and hardware-bound).
var defaultPhases = []string{"deployment", "conformance"}

// Compile-time interface checks.
var (
	_ infer.CustomResource[ValidationRunArgs, ValidationRunState] = (*ValidationRun)(nil)
	_ infer.CustomDiff[ValidationRunArgs, ValidationRunState]     = (*ValidationRun)(nil)
	_ infer.CustomUpdate[ValidationRunArgs, ValidationRunState]   = (*ValidationRun)(nil)
	_ infer.CustomDelete[ValidationRunState]                      = (*ValidationRun)(nil)
	_ infer.Annotated                                             = (*ValidationRun)(nil)
	_ infer.Annotated                                             = (*ValidationRunArgs)(nil)
	_ infer.Annotated                                             = (*ValidationRunState)(nil)
	_ infer.Annotated                                             = (*RecipeCriteria)(nil)
	_ infer.Annotated                                             = (*Toleration)(nil)
	_ infer.Annotated                                             = (*CheckResult)(nil)
)

// validateFn is a package seam over aicr.Validate for lifecycle tests.
var validateFn = aicr.Validate

// RecipeCriteria mirrors ClusterStack's recipe-selection inputs. It serves
// both as ClusterStack's `criteria` output and ValidationRun's `criteria`
// input — one schema type, no drift.
type RecipeCriteria struct {
	Accelerator string  `pulumi:"accelerator"`
	Service     string  `pulumi:"service"`
	Intent      string  `pulumi:"intent"`
	OS          *string `pulumi:"os,optional"`
	Platform    *string `pulumi:"platform,optional"`
	Nodes       *int    `pulumi:"nodes,optional"`
	// SkipComponents is the deployed-subset dimension: a recipe minus these
	// components is a different stack, and validation must see the same one
	// ClusterStack deployed (issue #22). Carried on the criteria (rather than
	// a separate ValidationRun input) so the documented wiring —
	// `criteria: stack.criteria` — stays the single source of truth.
	SkipComponents []string `pulumi:"skipComponents,optional"`
}

// Annotate populates schema metadata for RecipeCriteria.
func (c *RecipeCriteria) Annotate(an infer.Annotator) {
	an.Describe(c, `Recipe-selection criteria, mirroring ClusterStack's accelerator / service /
intent / os / platform / nodes inputs, plus the skipComponents the stack
deployed without. Wire a ClusterStack's `+"`criteria`"+` output here so deployment
and validation resolve the identical recipe and agree on which of its
components are in scope.`)
	an.Describe(&c.Accelerator, `GPU accelerator type. Supported values: "h100", "gb200", "b200", "rtx-pro-6000".`)
	an.Describe(&c.Service, `Kubernetes service. Supported values: "aks", "bcm", "eks", "gke", "kind", "lke", "oke".`)
	an.Describe(&c.Intent, `Workload intent. Supported values: "training", "inference".`)
	an.Describe(&c.OS, `Operating system flavor of the worker nodes. Leave unset for OS-agnostic
resolution. Supported values: "ubuntu", "cos", "ol".`)
	an.Describe(&c.Platform, `ML platform/framework. Supported values: "kubeflow" (training),
"dynamo" (inference), "nim" (inference, EKS+H100 only).`)
	an.Describe(&c.Nodes, `Worker-node count hint used to size the recipe.`)
	an.Describe(&c.SkipComponents, `Recipe components the stack intentionally did not deploy (ClusterStack's
`+"`skipComponents`"+`). ValidationRun treats them as out of scope rather than
missing: checks that presuppose one of them (e.g. the gpu-operator health,
DCGM metrics, and GPU-HPA checks when "gpu-operator" is skipped) are reported
"skipped" with a reason instead of failing, and the SDK's component-aware
checks see the components as disabled. A ClusterStack's `+"`criteria`"+` output
carries its own skipComponents, so wiring it keeps validation aligned with
the deployed subset automatically. Skipping does not verify a replacement
you run yourself — those checks are simply not made.`)
}

// Toleration is the standard Kubernetes toleration shape, provider-owned so
// the schema does not depend on the kubernetes package's types.
type Toleration struct {
	Key               *string `pulumi:"key,optional"`
	Operator          *string `pulumi:"operator,optional"`
	Value             *string `pulumi:"value,optional"`
	Effect            *string `pulumi:"effect,optional"`
	TolerationSeconds *int    `pulumi:"tolerationSeconds,optional"`
}

// Annotate populates schema metadata for Toleration.
func (t *Toleration) Annotate(an infer.Annotator) {
	an.Describe(t, `A Kubernetes pod toleration applied to validation workload pods.`)
	an.Describe(&t.Key, `The taint key the toleration applies to. Empty means match all keys
(with operator "Exists").`)
	an.Describe(&t.Operator, `Key-value relationship: "Exists" or "Equal". Default: "Equal".`)
	an.Describe(&t.Value, `The taint value to match (with operator "Equal").`)
	an.Describe(&t.Effect, `The taint effect to match: "NoSchedule", "PreferNoSchedule", or
"NoExecute". Empty matches all effects.`)
	an.Describe(&t.TolerationSeconds, `How long the pod tolerates a "NoExecute" taint, in seconds.`)
}

// CheckResult is one validator check's outcome, parsed from the CTRF report.
type CheckResult struct {
	Name    string `pulumi:"name"`
	Phase   string `pulumi:"phase"`
	Status  string `pulumi:"status"`
	Message string `pulumi:"message"`
}

// Annotate populates schema metadata for CheckResult.
func (c *CheckResult) Annotate(an infer.Annotator) {
	an.Describe(c, `The outcome of one validator check.`)
	an.Describe(&c.Name, `The check's name (e.g. "gpu-operator-health").`)
	an.Describe(&c.Phase, `The validation phase the check ran in.`)
	an.Describe(&c.Status, `The check's status: "passed", "failed", "skipped", "pending", or "other".`)
	an.Describe(&c.Message, `The check's failure or diagnostic message, if any.`)
}

// ValidationRunArgs defines the inputs for the ValidationRun resource.
type ValidationRunArgs struct {
	// Recipe criteria; wire from a ClusterStack's `criteria` output or build inline.
	Criteria RecipeCriteria `pulumi:"criteria"`
	// Optional recipe-data version ASSERTION (not a pin): mismatch with this
	// provider build's embedded AICR data version fails Create fast.
	// NOTE: this input must NOT be named "version": pulumi-go-provider's
	// infer diff path strips any input property with that name before
	// decoding (infer/resource.go, "We ignore version input from the
	// engine"), which made every live diff see old-set vs new-nil and
	// report a perpetual spurious replace. Guarded by a wire-level test.
	Version *string `pulumi:"recipeDataVersion,optional"`

	// Cluster access — same conventions as ClusterStack, but plain *string
	// (custom resources resolve inputs at create time; outputs still wire).
	Kubeconfig     *string `pulumi:"kubeconfig,optional" provider:"secret"`
	KubeconfigPath *string `pulumi:"kubeconfigPath,optional"`
	Context        *string `pulumi:"context,optional"`

	// Scheduling — required in practice on tainted GPU node groups.
	Tolerations  []Toleration      `pulumi:"tolerations,optional"`
	NodeSelector map[string]string `pulumi:"nodeSelector,optional"`

	// Images — provider pins agent/validator defaults; overrides for air-gap.
	ImageRegistry    *string  `pulumi:"imageRegistry,optional"`
	ImagePullSecrets []string `pulumi:"imagePullSecrets,optional"`

	// What to run. Default (nil): ["deployment", "conformance"].
	Phases []string `pulumi:"phases,optional"`

	Strict            *bool   `pulumi:"strict,optional"`
	RequireGpu        *bool   `pulumi:"requireGpu,optional"`
	Namespace         *string `pulumi:"namespace,optional"`
	TimeoutMinutes    *int    `pulumi:"timeoutMinutes,optional"`
	IncludeCtrfReport *bool   `pulumi:"includeCtrfReport,optional"`

	// Any change replaces the resource (fresh run) — command.local convention.
	Triggers []interface{} `pulumi:"triggers,optional"`
}

// Annotate populates schema metadata and defaults for ValidationRunArgs.
func (a *ValidationRunArgs) Annotate(an infer.Annotator) {
	an.Describe(&a.Criteria, `Recipe criteria. Wire a ClusterStack's `+"`criteria`"+` output here, or build
the object inline.`)
	an.Describe(&a.Version, `Recipe-data version assertion (not a pin). When set, it must equal this
provider build's embedded AICR recipe-data version or Create fails before
any cluster work — wire `+"`recipeDataVersion: stack.recipeVersion`"+` to catch a
provider upgrade between deploy and validate.`)
	an.Describe(&a.Kubeconfig, `Kubeconfig contents for the target cluster. Mutually exclusive with
`+"`kubeconfigPath`"+`. If neither is set, the ambient kubeconfig (KUBECONFIG env
var or ~/.kube/config) is used. Written to a mode-0600 temp file for the
duration of the run and removed afterward.`)
	an.Describe(&a.KubeconfigPath, `Path to a kubeconfig file on disk. Mutually exclusive with `+"`kubeconfig`"+`.`)
	an.Describe(&a.Context, `Kubeconfig context to select. Defaults to the kubeconfig's current-context.`)
	an.Describe(&a.Tolerations, `Tolerations applied to the validation workload pods (e.g. NCCL benchmark
pods). Required in practice on tainted GPU node groups; when unset, the
validator's default tolerate-all is kept.`)
	an.Describe(&a.NodeSelector, `Node selector applied to the validation workload pods. Use when GPU nodes
carry non-standard labels.`)
	an.Describe(&a.ImageRegistry, `Registry override for the snapshot-agent and validator images (air-gapped
mirrors). Unset keeps the default registry (ghcr.io).`)
	an.Describe(&a.ImagePullSecrets, `Names of image pull Secrets (in the validation namespace) for the
validation pods.`)
	an.Describe(&a.Phases, `Validation phases to run: any of "deployment", "conformance",
"performance". Default: ["deployment", "conformance"] — "performance" is an
explicit opt-in (long-running and hardware-bound).`)
	an.Describe(&a.Strict, `If true, a failed (or readiness-failed) validation fails the update while
still persisting the full results in state. The engine then re-runs
validation on every subsequent `+"`pulumi up`"+` until it passes. Default: false —
results are recorded and the update succeeds; read the `+"`status`"+` output.`)
	an.SetDefault(&a.Strict, false)
	an.Describe(&a.RequireGpu, `Whether the snapshot agent requires GPU nodes. Set false for
hardware-free clusters (kind). Default: true.`)
	an.SetDefault(&a.RequireGpu, true)
	an.Describe(&a.Namespace, `Namespace for the validation harness — use a dedicated one: it hosts the
privileged agent and cluster-admin-bound validator Jobs. The SDK creates it
on first run and deliberately never deletes it. Must be a valid DNS-1123
label and not a "kube-" system namespace. Default: "aicr-validation".`)
	an.SetDefault(&a.Namespace, "aicr-validation")
	an.Describe(&a.TimeoutMinutes, `Overall run timeout in minutes. Default: 30. Maximum: 1440 (24 hours).`)
	an.SetDefault(&a.TimeoutMinutes, 30)
	an.Describe(&a.IncludeCtrfReport, `If true, the merged CTRF JSON report is stored in the `+"`ctrfReport`"+` output.
Default: false (state-size control; per-check results are always in
`+"`phaseResults`"+`).`)
	an.SetDefault(&a.IncludeCtrfReport, false)
	an.Describe(&a.Triggers, `Arbitrary values; changing any of them replaces the resource and re-runs
validation (every input change does). Wire `+"`triggers: [stack.deployedComponents]`"+`
for re-validation cadence.`)
}

// ValidationRunState is the output state of the ValidationRun resource. The
// anonymous args embed flattens the input properties into state — required
// for the explicit Diff and for state inspection.
type ValidationRunState struct {
	ValidationRunArgs

	// Rollup: "passed" | "failed" | "readiness-failed". Infrastructure
	// errors have NO status value — they fail Create/Update outright.
	Status           string        `pulumi:"status"`
	ReadinessMessage string        `pulumi:"readinessMessage"`
	PhaseResults     []CheckResult `pulumi:"phaseResults"`
	Passed           int           `pulumi:"passed"`
	Failed           int           `pulumi:"failed"`
	Skipped          int           `pulumi:"skipped"`
	Other            int           `pulumi:"other"`
	RunID            string        `pulumi:"runId"`
	CompletedAt      string        `pulumi:"completedAt"`
	RecipeName       string        `pulumi:"recipeName"`
	RecipeVersion    string        `pulumi:"recipeVersion"`
	CtrfReport       *string       `pulumi:"ctrfReport,optional"`
}

// Annotate populates schema metadata for ValidationRunState outputs.
func (s *ValidationRunState) Annotate(an infer.Annotator) {
	an.Describe(&s.Status, `Rollup verdict: "passed", "failed", or "readiness-failed". Checks with
status "other" never flip the rollup (they are counted and listed).
Infrastructure errors have no status — they fail the update outright.`)
	an.Describe(&s.ReadinessMessage, `The readiness pre-flight failure message; empty unless status is
"readiness-failed".`)
	an.Describe(&s.PhaseResults, `Per-check results across all phases run.`)
	an.Describe(&s.Passed, `Number of checks that passed.`)
	an.Describe(&s.Failed, `Number of checks that failed.`)
	an.Describe(&s.Skipped, `Number of checks that were skipped.`)
	an.Describe(&s.Other, `Number of checks with an inconclusive outcome (crash, OOM, timeout;
includes CTRF "pending").`)
	an.Describe(&s.RunID, `The run identifier labeling this run's cluster-side artifacts.`)
	an.Describe(&s.CompletedAt, `When the run completed, RFC 3339.`)
	an.Describe(&s.RecipeName, `The resolved AICR recipe name that was validated.`)
	an.Describe(&s.RecipeVersion, `The AICR recipe data version that was validated.`)
	an.Describe(&s.CtrfReport, `The merged CTRF JSON report. Set only when `+"`includeCtrfReport`"+` is true.`)
}

// ValidationRun is the nvidia-aicr:index:ValidationRun custom resource.
type ValidationRun struct{}

// Annotate documents the resource.
func (r *ValidationRun) Annotate(an infer.Annotator) {
	an.Describe(r, `Runs an AICR recipe's empirical validation (cluster snapshot +
deployment/conformance/performance checks) against a live cluster and
records structured per-check results in Pulumi state.

The run happens at deploy time (never at preview). The validation harness
deploys short-lived agent and validator Jobs plus per-run RBAC on the target
cluster and cleans them up afterward; the validation namespace (default
"aicr-validation") is created on first run and deliberately persists.

Privilege level: the snapshot-agent Job runs as a privileged container
(bound to a read-only node-inspection ClusterRole), and validator Jobs run
under a per-run cluster-admin ClusterRoleBinding — both removed after the
run, including on cancellation. The kubeconfig identity needs RBAC to
create/patch Namespaces, ClusterRoles, and ClusterRoleBindings.

Any input change replaces the resource, which re-runs validation (delete is
a no-op). With `+"`strict: true`"+`, a failed run fails the update while persisting
full results, and validation re-runs on every subsequent `+"`pulumi up`"+` until it
passes — use `+"`dependsOn`"+` to gate downstream resources on it.`)
}

// Create runs validation at apply time. Preview is a no-op that echoes the
// inputs with zero-valued results.
func (r *ValidationRun) Create(ctx context.Context, req infer.CreateRequest[ValidationRunArgs]) (infer.CreateResponse[ValidationRunState], error) {
	if req.DryRun {
		// Validate opportunistically, but skip when criteria is zero-valued:
		// criteria wired from an unresolved output arrives as the zero
		// struct at preview. Full validation always re-runs at apply.
		if !isZeroCriteria(req.Inputs.Criteria) {
			if err := validateValidationRunArgs(&req.Inputs); err != nil {
				return infer.CreateResponse[ValidationRunState]{}, err
			}
		}
		return infer.CreateResponse[ValidationRunState]{
			Output: ValidationRunState{ValidationRunArgs: req.Inputs},
		}, nil
	}

	id, state, err := runValidation(ctx, req.Name, req.Inputs)
	return infer.CreateResponse[ValidationRunState]{ID: id, Output: state}, err
}

// Diff is the single authoritative replace-on-any-change mechanism: every
// changed top-level input property is reported UpdateReplace. An explicit
// Diff is required because Update is implemented (strict-mode init-error
// repair), which would otherwise degrade nested-property changes (e.g.
// tolerations[0].key) to in-place updates under infer's default diff.
func (r *ValidationRun) Diff(ctx context.Context, req infer.DiffRequest[ValidationRunArgs, ValidationRunState]) (infer.DiffResponse, error) {
	detailed := map[string]p.PropertyDiff{}
	oldArgs := reflect.ValueOf(req.State.ValidationRunArgs)
	newArgs := reflect.ValueOf(req.Inputs)
	argsType := oldArgs.Type()
	for i := 0; i < argsType.NumField(); i++ {
		field := argsType.Field(i)
		tag, ok := field.Tag.Lookup("pulumi")
		if !ok {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		if !reflect.DeepEqual(oldArgs.Field(i).Interface(), newArgs.Field(i).Interface()) {
			// Diagnostic escape hatch: property-level compare tracing for
			// spurious-replace investigations. Set the env var to a writable
			// file path; the values logged are the DECODED Go values, which
			// can differ from the engine's display.
			if path := os.Getenv("PULUMI_NVIDIA_AICR_DEBUG_DIFF"); path != "" {
				if f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); ferr == nil {
					fmt.Fprintf(f, "nvidia-aicr diff: property %q unequal:\n  old=%#v\n  new=%#v\n",
						name, oldArgs.Field(i).Interface(), newArgs.Field(i).Interface())
					_ = f.Close()
				}
			}
			detailed[name] = p.PropertyDiff{Kind: p.UpdateReplace, InputDiff: true}
		}
	}
	return p.DiffResponse{
		// Delete is a no-op, so deleting first is free and avoids two
		// concurrent validation runs against one cluster.
		DeleteBeforeReplace: true,
		HasChanges:          len(detailed) > 0,
		DetailedDiff:        detailed,
	}, nil
}

// Update is only reachable through the engine's init-error repair path (a
// strict-mode failure with no input diff — any input diff replaces instead).
// It re-runs validation with the same body as Create.
func (r *ValidationRun) Update(ctx context.Context, req infer.UpdateRequest[ValidationRunArgs, ValidationRunState]) (infer.UpdateResponse[ValidationRunState], error) {
	if req.DryRun {
		// Echo the inputs, keep the recorded results.
		state := req.State
		state.ValidationRunArgs = req.Inputs
		return infer.UpdateResponse[ValidationRunState]{Output: state}, nil
	}
	_, state, err := runValidation(ctx, req.ID, req.Inputs)
	return infer.UpdateResponse[ValidationRunState]{Output: state}, err
}

// Delete is a deliberate no-op: the SDK cleans its per-run cluster
// artifacts itself, and the validation namespace (plus a leftover
// aicr-snapshot ConfigMap) persists by SDK design.
func (r *ValidationRun) Delete(ctx context.Context, req infer.DeleteRequest[ValidationRunState]) (infer.DeleteResponse, error) {
	return infer.DeleteResponse{}, nil
}

// runValidation is the shared Create/Update body: validate args →
// materialize kubeconfig → aicr.Validate → translate to state (+ strict-mode
// error).
func runValidation(ctx context.Context, name string, args ValidationRunArgs) (string, ValidationRunState, error) {
	if err := validateValidationRunArgs(&args); err != nil {
		return "", ValidationRunState{}, err
	}

	kubeconfigPath, cleanup, err := materializeKubeconfig(args.Kubeconfig, args.KubeconfigPath, args.Context)
	if err != nil {
		return "", ValidationRunState{}, err
	}
	defer cleanup()

	timeoutMinutes := derefInt(args.TimeoutMinutes, 30)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()

	criteria := aicr.Criteria{
		Service:     canonical(args.Criteria.Service),
		Accelerator: canonical(args.Criteria.Accelerator),
		Intent:      canonical(args.Criteria.Intent),
		OS:          canonicalOr(args.Criteria.OS, ""),
		Platform:    canonicalOr(args.Criteria.Platform, ""),
	}
	if args.Criteria.Nodes != nil {
		criteria.Nodes = int32(*args.Criteria.Nodes)
	}

	report, err := validateFn(ctx, criteria, aicr.ValidateOptions{
		KubeconfigPath:   kubeconfigPath,
		Namespace:        derefString(args.Namespace, "aicr-validation"),
		Tolerations:      toCoreTolerations(args.Tolerations),
		NodeSelector:     args.NodeSelector,
		ImageRegistry:    derefString(args.ImageRegistry, ""),
		ImagePullSecrets: args.ImagePullSecrets,
		Phases:           normalizePhases(args.Phases),
		RequireGPU:       derefBool(args.RequireGpu, true),
		SkipComponents:   args.Criteria.SkipComponents,
	})
	if err != nil {
		// Infrastructure error: the run couldn't happen. Plain failure,
		// nothing persisted.
		return "", ValidationRunState{}, err
	}

	state := ValidationRunState{
		ValidationRunArgs: args,
		Status:            string(report.Outcome),
		ReadinessMessage:  report.ReadinessMessage,
		Passed:            report.Passed,
		Failed:            report.Failed,
		Skipped:           report.Skipped,
		Other:             report.Other,
		RunID:             report.RunID,
		CompletedAt:       report.CompletedAt.UTC().Format(time.RFC3339),
		RecipeName:        report.RecipeName,
		RecipeVersion:     report.RecipeVersion,
	}
	state.PhaseResults = make([]CheckResult, 0, len(report.Checks))
	for _, c := range report.Checks {
		state.PhaseResults = append(state.PhaseResults, CheckResult{
			Name:    c.Name,
			Phase:   c.Phase,
			Status:  c.Status,
			Message: c.Message,
		})
	}
	if derefBool(args.IncludeCtrfReport, false) && report.CTRFReport != "" {
		ctrf := report.CTRFReport
		state.CtrfReport = &ctrf
	}

	id := name + "-" + report.RunID

	if derefBool(args.Strict, false) {
		switch report.Outcome {
		case aicr.OutcomeFailed:
			failing := make([]string, 0, report.Failed)
			for _, c := range report.Checks {
				if c.Status == "failed" {
					failing = append(failing, c.Name)
				}
			}
			return id, state, infer.ResourceInitFailedError{Reasons: []string{
				fmt.Sprintf("%d validation check(s) failed: %s",
					report.Failed, strings.Join(failing, ", ")),
			}}
		case aicr.OutcomeReadinessFailed:
			return id, state, infer.ResourceInitFailedError{Reasons: []string{
				report.ReadinessMessage,
			}}
		}
	}
	warnFailedChecks(ctx, report)
	return id, state, nil
}

// maxWarnMessageLen bounds each per-check message in the failed-checks
// warning; the full text is always in state (phaseResults).
const maxWarnMessageLen = 240

// warnFailedChecks surfaces a non-strict failed verdict as a warning
// diagnostic. Without it the only terminal signal is a `status: failed`
// output and the per-check detail sits silently in state — operators had to
// `pulumi stack export` to learn WHICH checks failed (issue #22).
func warnFailedChecks(ctx context.Context, report *aicr.ValidationReport) {
	if report == nil || report.Outcome != aicr.OutcomeFailed {
		return
	}
	lines := make([]string, 0, report.Failed)
	for _, c := range report.Checks {
		if c.Status != "failed" {
			continue
		}
		msg := strings.TrimSpace(c.Message)
		if len(msg) > maxWarnMessageLen {
			// Byte slicing can cut a multi-byte rune in half (SDK messages
			// carry "≥" and "—"); drop any trailing partial rune.
			msg = strings.ToValidUTF8(msg[:maxWarnMessageLen], "") + "…"
		}
		if msg == "" {
			lines = append(lines, fmt.Sprintf("%s (%s)", c.Name, c.Phase))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s (%s): %s", c.Name, c.Phase, msg))
	}
	p.GetLogger(ctx).Warningf(
		"validation of recipe %s failed: %d check(s) failed, %d passed, %d skipped "+
			"(recorded in state as status \"failed\"; set strict: true to fail the update):\n  %s",
		report.RecipeName, report.Failed, report.Passed, report.Skipped, strings.Join(lines, "\n  "))
}

// validateValidationRunArgs rejects invalid inputs with friendly errors
// before any SDK or cluster work, mirroring validateArgs' style. Criteria
// checks go through the shared validateCriteria so ValidationRun and
// ClusterStack produce byte-identical messages.
func validateValidationRunArgs(args *ValidationRunArgs) error {
	if err := validateCriteria(
		args.Criteria.Accelerator, args.Criteria.Service, args.Criteria.Intent,
		args.Criteria.OS, args.Criteria.Platform, args.Criteria.Nodes,
	); err != nil {
		return err
	}
	for _, phase := range args.Phases {
		if !contains(supportedPhases, canonical(phase)) {
			return fmt.Errorf("phase %q is not supported (must be one of: %s)",
				phase, strings.Join(supportedPhases, ", "))
		}
	}
	if args.Kubeconfig != nil && args.KubeconfigPath != nil {
		return fmt.Errorf("kubeconfig and kubeconfigPath are mutually exclusive; set only one")
	}
	if args.TimeoutMinutes != nil && *args.TimeoutMinutes < 1 {
		return fmt.Errorf("timeoutMinutes must be at least 1; got %d", *args.TimeoutMinutes)
	}
	if args.TimeoutMinutes != nil && *args.TimeoutMinutes > maxTimeoutMinutes {
		return fmt.Errorf("timeoutMinutes must be at most %d (24 hours); got %d",
			maxTimeoutMinutes, *args.TimeoutMinutes)
	}
	if args.Namespace != nil {
		if errs := k8svalidation.IsDNS1123Label(*args.Namespace); len(errs) > 0 {
			return fmt.Errorf("namespace %q is not a valid Kubernetes namespace name (%s)",
				*args.Namespace, strings.Join(errs, "; "))
		}
		if strings.HasPrefix(*args.Namespace, "kube-") {
			return fmt.Errorf("namespace %q is reserved for Kubernetes system namespaces; "+
				"use a dedicated namespace for the validation harness (default %q)",
				*args.Namespace, "aicr-validation")
		}
	}
	if args.Version != nil && *args.Version != "" {
		embedded := aicr.SDKVersion()
		if *args.Version != embedded {
			return fmt.Errorf(
				"recipeDataVersion %q does not match this provider build's embedded AICR recipe-data version %q; "+
					"the deployed stack and this validation would use different recipe data — "+
					"align the provider version (or drop the recipeDataVersion input to skip the assertion)",
				*args.Version, embedded)
		}
	}
	return nil
}

// normalizePhases canonicalizes and dedupes the phases input, defaulting to
// deployment+conformance when unset. Order is preserved.
func normalizePhases(phases []string) []string {
	if len(phases) == 0 {
		return append([]string(nil), defaultPhases...)
	}
	seen := make(map[string]bool, len(phases))
	out := make([]string, 0, len(phases))
	for _, phase := range phases {
		c := canonical(phase)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		return append([]string(nil), defaultPhases...)
	}
	return out
}

// materializeKubeconfig returns a kubeconfig PATH for the SDK plus a cleanup
// func (the SDK takes only paths). Contents are written to a mode-0600 temp
// file; a path without a context override passes through verbatim; a context
// override loads the config (contents, path, or ambient), sets
// current-context, and writes a temp file; nothing set means "" (ambient
// discovery). cleanup is never nil and removes any temp file.
func materializeKubeconfig(kubeconfig, kubeconfigPath, kubeContext *string) (string, func(), error) {
	noop := func() {}

	hasContents := kubeconfig != nil && *kubeconfig != ""
	hasPath := kubeconfigPath != nil && *kubeconfigPath != ""
	hasContext := kubeContext != nil && *kubeContext != ""

	if !hasContents && !hasPath && !hasContext {
		return "", noop, nil
	}
	if hasPath && !hasContext {
		return *kubeconfigPath, noop, nil
	}

	var raw []byte
	switch {
	case hasContents:
		raw = []byte(*kubeconfig)
	case hasPath:
		content, err := os.ReadFile(*kubeconfigPath)
		if err != nil {
			return "", noop, fmt.Errorf("reading kubeconfig %s: %w", *kubeconfigPath, err)
		}
		raw = content
	default:
		// Context override with ambient discovery: load the default chain.
		config, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
		if err != nil {
			return "", noop, fmt.Errorf("loading ambient kubeconfig: %w", err)
		}
		out, err := rewriteContext(config, *kubeContext)
		if err != nil {
			return "", noop, err
		}
		return writeTempKubeconfig(out)
	}

	if !hasContext {
		// Contents without a context override: write them out verbatim.
		return writeTempKubeconfig(raw)
	}

	config, err := clientcmd.Load(raw)
	if err != nil {
		return "", noop, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	out, err := rewriteContext(config, *kubeContext)
	if err != nil {
		return "", noop, err
	}
	return writeTempKubeconfig(out)
}

// rewriteContext sets the config's current-context, erroring with the
// available contexts when the requested one does not exist.
func rewriteContext(config *clientcmdapi.Config, kubeContext string) ([]byte, error) {
	if _, ok := config.Contexts[kubeContext]; !ok {
		available := make([]string, 0, len(config.Contexts))
		for name := range config.Contexts {
			available = append(available, name)
		}
		sort.Strings(available)
		return nil, fmt.Errorf("context %q not found in kubeconfig (available: %s)",
			kubeContext, strings.Join(available, ", "))
	}
	config.CurrentContext = kubeContext
	out, err := clientcmd.Write(*config)
	if err != nil {
		return nil, fmt.Errorf("serializing kubeconfig: %w", err)
	}
	return out, nil
}

// writeTempKubeconfig writes kubeconfig bytes to a mode-0600 temp file
// (os.CreateTemp's default) and returns its path plus a remover.
func writeTempKubeconfig(raw []byte) (string, func(), error) {
	noop := func() {}
	f, err := os.CreateTemp("", "pulumi-nvidia-aicr-kubeconfig-*")
	if err != nil {
		return "", noop, fmt.Errorf("creating temp kubeconfig: %w", err)
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		cleanup()
		return "", noop, fmt.Errorf("writing temp kubeconfig: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("closing temp kubeconfig: %w", err)
	}
	return path, cleanup, nil
}

// toCoreTolerations translates the provider-owned toleration shape into
// k8s.io/api/core/v1 for the SDK. nil in, nil out — nil means "keep the
// validator's default tolerate-all".
func toCoreTolerations(tolerations []Toleration) []corev1.Toleration {
	if tolerations == nil {
		return nil
	}
	out := make([]corev1.Toleration, 0, len(tolerations))
	for _, t := range tolerations {
		ct := corev1.Toleration{
			Key:   derefString(t.Key, ""),
			Value: derefString(t.Value, ""),
		}
		if t.Operator != nil {
			ct.Operator = corev1.TolerationOperator(*t.Operator)
		}
		if t.Effect != nil {
			ct.Effect = corev1.TaintEffect(*t.Effect)
		}
		if t.TolerationSeconds != nil {
			secs := int64(*t.TolerationSeconds)
			ct.TolerationSeconds = &secs
		}
		out = append(out, ct)
	}
	return out
}

// isZeroCriteria reports whether criteria is entirely zero-valued — the
// shape an unresolved output has at preview.
func isZeroCriteria(c RecipeCriteria) bool {
	return c.Accelerator == "" && c.Service == "" && c.Intent == "" &&
		c.OS == nil && c.Platform == nil && c.Nodes == nil && len(c.SkipComponents) == 0
}

func derefString(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

func derefInt(i *int, def int) int {
	if i != nil {
		return *i
	}
	return def
}
