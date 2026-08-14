// validate.go adapts the AICR SDK's empirical validation surface
// (CollectSnapshot + ValidateState) for the ValidationRun resource.
//
// Contract: Validate returns (report, nil) for every run that produced a
// verdict — passed, failed, or readiness-failed — and (nil, err) only for
// infrastructure failures (client init, resolve, snapshot deploy/timeout,
// non-readiness ValidateState errors). The resource layer turns the report
// into Pulumi state and decides strict-mode semantics.
//
// Unlike recipe-component deployment (which must stay in Pulumi), the SDK's
// validation harness deliberately deploys its own short-lived agent and
// validator Jobs — that IS the validation mechanism. The SDK reaps its RBAC,
// ConfigMaps, and Jobs itself (with fresh contexts on cancellation), except
// for an in-flight validator Job mid-check, which sweepValidatorJobs handles.
package aicr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	aicrclient "github.com/NVIDIA/aicr/pkg/client/v1"
	aicrerrors "github.com/NVIDIA/aicr/pkg/errors"
	k8sclient "github.com/NVIDIA/aicr/pkg/k8s/client"
	"github.com/NVIDIA/aicr/pkg/measurement"
	"github.com/NVIDIA/aicr/pkg/snapshotter"
	"github.com/NVIDIA/aicr/pkg/validator/ctrf"
	vlabels "github.com/NVIDIA/aicr/pkg/validator/labels"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Readiness pre-flight failures carry no typed sentinel in SDK v0.18.0:
// checkReadiness (aicr/pkg/validator/validator.go) returns a generic
// *errors.StructuredError with ErrCodeInvalidRequest and exactly these two
// message prefixes. The prefixes are pinned here and guarded by a regression
// test that drives the real checkReadiness path offline, so an SDK bump that
// changes the strings breaks CI rather than silently misclassifying.
const (
	readinessFailedPrefix   = "readiness check failed: "
	readinessEvaluatePrefix = "readiness check could not evaluate: "
)

// agentImageBase mirrors the CLI's agent image scheme
// (aicr/pkg/cli/root.go): the snapshot agent ships in the ghcr.io/nvidia/aicr
// image, tagged with the release version.
const agentImageBase = "ghcr.io/nvidia/aicr"

// agentImageEnvVar overrides the snapshot-agent image with a verbatim image
// reference. Dev-build escape hatch: builds whose AICR module version is
// unresolvable (filesystem replace) have no trustworthy tag to pin, and
// agentImage fails closed rather than defaulting to a mutable :latest.
const agentImageEnvVar = "PULUMI_NVIDIA_AICR_AGENT_IMAGE"

// snapshotTimeout bounds the snapshot-agent deploy/collect step. The overall
// run is additionally bounded by the caller's context deadline
// (timeoutMinutes on the resource), whichever is smaller.
const snapshotTimeout = 10 * time.Minute

// sweepTimeout bounds the best-effort orphaned-Job sweep after a
// canceled/expired run. Fresh context: the parent is already dead.
const sweepTimeout = 30 * time.Second

// ValidateOptions carries everything beyond criteria that a validation run
// needs. Phases is required non-empty: the SDK's default is to run ALL
// phases including performance, which must never happen implicitly.
type ValidateOptions struct {
	// KubeconfigPath is a kubeconfig file path; "" means ambient discovery
	// (KUBECONFIG, ~/.kube/config, then in-cluster).
	KubeconfigPath string
	// Namespace is the validation namespace. "" defaults to "aicr-validation"
	// (the SDK default; the SDK creates it and never deletes it).
	Namespace string
	// Tolerations are applied to the validation workload pods. nil keeps the
	// validator's default (tolerate-all), so validation Jobs schedule onto
	// tainted GPU node groups without configuration; a non-nil slice
	// explicitly overrides that default.
	Tolerations []corev1.Toleration
	// NodeSelector is applied to the validation workload pods.
	NodeSelector map[string]string
	// ImageRegistry overrides the registry for agent and validator images
	// (air-gapped mirrors). "" keeps the default registry.
	ImageRegistry string
	// ImagePullSecrets name Secrets in the validation namespace.
	ImagePullSecrets []string
	// Phases lists the validation phases to run, canonical values, REQUIRED
	// non-empty. The adapter errors on an empty list as defense against the
	// SDK's run-everything default.
	Phases []string
	// RequireGPU makes the snapshot agent require GPU nodes. Set false for
	// hardware-free clusters (kind).
	RequireGPU bool
	// NoCluster is a test seam: no Kubernetes resources are created, the
	// snapshot-agent step is skipped, and every check reports skipped.
	NoCluster bool
	// RunID identifies the run's cluster-side artifacts. "" generates one.
	// Always passed to WithValidationRunID.
	RunID string
}

// Outcome is the rollup verdict of a validation run.
type Outcome string

// Validation run outcomes. Infrastructure failures have no Outcome — they
// surface as errors from Validate.
const (
	OutcomePassed          Outcome = "passed"
	OutcomeFailed          Outcome = "failed"
	OutcomeReadinessFailed Outcome = "readiness-failed"
)

// CheckOutcome is one validator check's result.
type CheckOutcome struct {
	Name    string
	Phase   string
	Status  string
	Message string
}

// ValidationReport is the translated result of a validation run.
type ValidationReport struct {
	Outcome          Outcome
	ReadinessMessage string
	Checks           []CheckOutcome
	Passed           int
	Failed           int
	Skipped          int
	Other            int
	RunID            string
	RecipeName       string
	RecipeVersion    string
	CompletedAt      time.Time
	// CTRFReport is the merged CTRF JSON across phases. Always captured;
	// the resource decides whether to expose it in state.
	CTRFReport string
}

// SDKVersion reports the AICR SDK module version embedded in this build,
// which also versions the embedded recipe data ("embedded" when build info
// is unavailable, e.g. test binaries). Exported for the ValidationRun
// resource's version-assertion check.
func SDKVersion() string {
	return sdkModuleVersion()
}

// Validate resolves the recipe for criteria on a fresh client, collects a
// cluster snapshot via the SDK's agent Job, runs the selected validation
// phases, and translates the results.
//
// Contract: (report, nil) for passed / failed / readiness-failed;
// (nil, err) only for infrastructure errors.
func Validate(ctx context.Context, criteria Criteria, opts ValidateOptions) (*ValidationReport, error) {
	if len(opts.Phases) == 0 {
		return nil, fmt.Errorf("validation phases must be non-empty (the SDK would otherwise run every phase, including performance)")
	}
	phases := make([]aicrclient.Phase, len(opts.Phases))
	for i, p := range opts.Phases {
		phases[i] = aicrclient.Phase(p)
	}

	namespace := opts.Namespace
	if namespace == "" {
		namespace = "aicr-validation"
	}

	runID := opts.RunID
	if runID == "" {
		var err error
		runID, err = generateRunID()
		if err != nil {
			return nil, fmt.Errorf("generating validation run ID: %w", err)
		}
	}

	// The client must stay open across resolve → validate: ValidateState
	// requires a RecipeResult produced by the SAME client (it carries
	// unexported internal state), so aicr.Resolve — which closes its client —
	// cannot be reused here.
	client, err := aicrclient.NewClient(
		aicrclient.WithRecipeSource(aicrclient.EmbeddedSource()),
	)
	if err != nil {
		return nil, fmt.Errorf("initializing AICR client: %w", err)
	}
	defer client.Close()

	result, err := client.ResolveRecipe(ctx, aicrclient.RecipeRequest{
		Service:     criteria.Service,
		Accelerator: criteria.Accelerator,
		Intent:      criteria.Intent,
		OS:          criteria.OS,
		Platform:    criteria.Platform,
		Nodes:       criteria.Nodes,
	})
	if err != nil {
		return nil, fmt.Errorf("resolving AICR recipe: %w", err)
	}

	report := &ValidationReport{
		RunID:         runID,
		RecipeName:    recipeName(result.Resolved(), result.Name),
		RecipeVersion: sdkModuleVersion(),
	}

	var snap *aicrclient.Snapshot
	if opts.NoCluster {
		// No cluster to snapshot in test mode; synthesize a snapshot that
		// satisfies the recipes' K8s-version readiness constraints so the
		// full skipped-checks path is exercised.
		snap = noClusterSnapshot()
	} else {
		image, imgErr := agentImage(opts.ImageRegistry)
		if imgErr != nil {
			return nil, imgErr
		}
		snap, err = client.CollectSnapshot(ctx, &aicrclient.AgentConfig{
			Kubeconfig:         opts.KubeconfigPath,
			Namespace:          namespace,
			Image:              image,
			ServiceAccountName: "aicr-agent",
			JobName:            "aicr-snapshot-" + runID,
			ImagePullSecrets:   opts.ImagePullSecrets,
			NodeSelector:       opts.NodeSelector,
			Tolerations:        opts.Tolerations,
			Privileged:         true,
			RequireGPU:         opts.RequireGPU,
			Cleanup:            true,
			Timeout:            snapshotTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("collecting cluster snapshot: %w", err)
		}
	}

	// Always pass WithValidationPhases: the SDK default runs ALL phases
	// (including performance). No WithValidationTimeout — the caller's ctx
	// deadline governs (the facade honors the smaller of the parent deadline
	// and its 75m default cap).
	valOpts := []aicrclient.ValidateOption{
		aicrclient.WithValidationPhases(phases...),
		aicrclient.WithValidationNamespace(namespace),
		aicrclient.WithValidationRunID(runID),
		aicrclient.WithValidationCleanup(true),
		aicrclient.WithValidationKubeconfig(opts.KubeconfigPath),
		aicrclient.WithValidationNodeSelector(opts.NodeSelector),
		aicrclient.WithValidationImagePullSecrets(opts.ImagePullSecrets),
	}
	if opts.Tolerations != nil {
		// Only override when the caller supplied tolerations: calling
		// WithValidationTolerations with nil would CLEAR the validator's
		// default tolerate-all, silently making validation pods
		// unschedulable on tainted GPU node groups.
		valOpts = append(valOpts, aicrclient.WithValidationTolerations(opts.Tolerations))
	}
	if opts.ImageRegistry != "" {
		valOpts = append(valOpts, aicrclient.WithValidationImageRegistryOverride(opts.ImageRegistry))
	}
	if opts.NoCluster {
		valOpts = append(valOpts, aicrclient.WithValidationNoCluster(true))
	}

	results, err := client.ValidateState(ctx, result, snap, valOpts...)
	if err != nil {
		if msg, ok := isReadinessError(err); ok {
			// The cluster does not meet the recipe's readiness constraints:
			// a verdict, not an infrastructure failure.
			report.Outcome = OutcomeReadinessFailed
			report.ReadinessMessage = msg
			report.CompletedAt = time.Now().UTC()
			return report, nil
		}
		if ctx.Err() != nil && !opts.NoCluster {
			// Cancellation/timeout mid-run: the SDK reaps RBAC, CRBs, and
			// ConfigMaps with fresh contexts, but an in-flight validator Job
			// is cleaned with the (dead) parent ctx and can orphan. Sweep it
			// best-effort; never fatal.
			if sweepErr := sweepValidatorJobs(opts.KubeconfigPath, namespace, runID); sweepErr != nil {
				slog.Warn("failed to sweep orphaned validator Jobs after canceled run",
					"namespace", namespace, "runID", runID, "error", sweepErr)
			}
		}
		return nil, fmt.Errorf("running AICR validation: %w", err)
	}

	checks, counts, mergedCTRF, err := buildReport(results)
	if err != nil {
		return nil, err
	}
	report.Checks = checks
	report.Passed = counts[countPassed]
	report.Failed = counts[countFailed]
	report.Skipped = counts[countSkipped]
	report.Other = counts[countOther]
	report.CTRFReport = mergedCTRF
	report.CompletedAt = time.Now().UTC()
	report.Outcome = rollupOutcome(counts)
	return report, nil
}

// rollupOutcome derives the run verdict from buildReport's counts. Pure.
// Any failed check fails the run. "other" (crash/OOM/timeout) never flips
// the rollup — a deliberate divergence from the SDK's fail-fast gate
// semantics — but is always counted and listed.
func rollupOutcome(counts [4]int) Outcome {
	if counts[countFailed] > 0 {
		return OutcomeFailed
	}
	return OutcomePassed
}

// isReadinessError classifies err as a readiness pre-flight failure. It
// matches on the unformatted StructuredError.Message field (Error() prepends
// "[INVALID_REQUEST]") against the two pinned prefixes; the shared
// ErrCodeInvalidRequest code alone is not sufficient (nil/closed client,
// foreign RecipeResult, invalid phases, and dependency-affinity failures use
// it too).
func isReadinessError(err error) (string, bool) {
	var se *aicrerrors.StructuredError
	if !errors.As(err, &se) {
		return "", false
	}
	if se.Code != aicrerrors.ErrCodeInvalidRequest {
		return "", false
	}
	if strings.HasPrefix(se.Message, readinessFailedPrefix) ||
		strings.HasPrefix(se.Message, readinessEvaluatePrefix) {
		return se.Message, true
	}
	return "", false
}

// Indices into buildReport's counts array.
const (
	countPassed = iota
	countFailed
	countSkipped
	countOther
)

// buildReport translates facade PhaseResults (typed *ctrf.Report) into
// checks, counts, and merged CTRF JSON. Pure. "pending" is folded into the
// Other COUNT (CTRF allows it but the validator never emits it in practice;
// four counts is the output contract), while each check's Status is
// reported verbatim.
func buildReport(results []*aicrclient.PhaseResult) (checks []CheckOutcome, counts [4]int, mergedCTRF string, err error) {
	reports := make([]*ctrf.Report, 0, len(results))
	for _, pr := range results {
		if pr == nil {
			continue
		}
		if pr.Report == nil {
			// Defensive: a phase with no CTRF report contributes nothing.
			slog.Warn("validation phase returned no CTRF report; skipping it in the rollup",
				"phase", string(pr.Phase))
			continue
		}
		reports = append(reports, pr.Report)
		for _, t := range pr.Report.Results.Tests {
			checks = append(checks, CheckOutcome{
				Name:    t.Name,
				Phase:   string(pr.Phase),
				Status:  t.Status,
				Message: t.Message,
			})
		}
		s := pr.Report.Results.Summary
		counts[countPassed] += s.Passed
		counts[countFailed] += s.Failed
		counts[countSkipped] += s.Skipped
		counts[countOther] += s.Other + s.Pending
	}

	if len(reports) > 0 {
		merged := ctrf.MergeReports("pulumi-nvidia-aicr", sdkModuleVersion(), reports)
		out, mErr := json.Marshal(merged)
		if mErr != nil {
			return nil, [4]int{}, "", fmt.Errorf("encoding merged CTRF report: %w", mErr)
		}
		mergedCTRF = string(out)
	}
	return checks, counts, mergedCTRF, nil
}

// agentImage derives the pinned snapshot-agent image, matching the CLI's
// scheme (ghcr.io/nvidia/aicr:<module version>). registryOverride replaces
// the registry host and keeps the repository and tag. When the module
// version is unresolvable there is no trustworthy tag to pin, so agentImage
// fails closed instead of silently using the mutable :latest tag; the
// PULUMI_NVIDIA_AICR_AGENT_IMAGE env var supplies a verbatim image
// reference for dev builds (it bypasses registryOverride).
func agentImage(registryOverride string) (string, error) {
	if override := os.Getenv(agentImageEnvVar); override != "" {
		return override, nil
	}
	tag := sdkModuleVersion()
	if tag == "embedded" {
		return "", fmt.Errorf(
			"cannot pin the snapshot-agent image: this build's AICR SDK module version is unresolvable "+
				"(filesystem replace or missing build info), and the floating %s:latest tag will not be used; "+
				"set %s to an explicit image reference for dev builds",
			agentImageBase, agentImageEnvVar)
	}
	image := agentImageBase + ":" + tag
	if registryOverride != "" {
		registry := strings.TrimSuffix(registryOverride, "/")
		image = registry + strings.TrimPrefix(image, "ghcr.io")
	}
	return image, nil
}

// sweepValidatorJobs best-effort deletes validator Jobs labeled with runID in
// the validation namespace after a canceled/expired run. Fresh context: the
// parent ctx is already dead. Errors are returned for logging only.
func sweepValidatorJobs(kubeconfigPath, namespace, runID string) error {
	clientset, _, err := k8sclient.BuildKubeClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("building Kubernetes client for validator Job sweep: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sweepTimeout)
	defer cancel()
	policy := metav1.DeletePropagationBackground
	err = clientset.BatchV1().Jobs(namespace).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: vlabels.RunID + "=" + runID},
	)
	if err != nil {
		return fmt.Errorf("deleting validator Jobs labeled %s=%s in %s: %w",
			vlabels.RunID, runID, namespace, err)
	}
	return nil
}

// noClusterSnapshot synthesizes a snapshot for NoCluster test runs. It
// carries a current Kubernetes server version so the recipes' K8s-version
// readiness constraints pass and the run reaches the skipped-checks path.
func noClusterSnapshot() *aicrclient.Snapshot {
	return aicrclient.WrapSnapshot(&snapshotter.Snapshot{
		Measurements: []*measurement.Measurement{
			measurement.NewMeasurement(measurement.TypeK8s).
				WithSubtypeBuilder(
					measurement.NewSubtypeBuilder("server").
						SetString("version", "v1.34.0"),
				).
				Build(),
		},
	})
}

// generateRunID returns a short random lowercase-hex identifier, safe for
// Kubernetes names and label values.
func generateRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
