package aicr

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"testing"

	aicrclient "github.com/NVIDIA/aicr/pkg/client/v1"
	aicrerrors "github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/pkg/validator/ctrf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kindCriteria is the local-development recipe used by the offline wiring
// tests: kind binds no OS and resolves entirely from embedded data.
var kindCriteria = Criteria{
	Service:     "kind",
	Accelerator: "h100",
	Intent:      "training",
}

// TestValidateNoClusterFullWiring drives the full adapter path offline:
// resolve → (synthetic snapshot) → ValidateState in NoCluster mode →
// report translation. Every check must report skipped, and no performance
// checks may appear — proving WithValidationPhases is always passed (the
// SDK default would run performance too).
func TestValidateNoClusterFullWiring(t *testing.T) {
	report, err := Validate(t.Context(), kindCriteria, ValidateOptions{
		NoCluster: true,
		Phases:    []string{"deployment", "conformance"},
		Namespace: "aicr-validation",
	})
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, OutcomePassed, report.Outcome)
	assert.Empty(t, report.ReadinessMessage)
	require.NotEmpty(t, report.Checks, "expected the catalog to contribute checks")
	for _, c := range report.Checks {
		assert.Equal(t, "skipped", c.Status, "check %s in %s", c.Name, c.Phase)
		assert.NotEqual(t, "performance", c.Phase,
			"performance phase must not run without explicit opt-in")
	}
	assert.Equal(t, len(report.Checks), report.Skipped)
	assert.Zero(t, report.Passed)
	assert.Zero(t, report.Failed)
	assert.Zero(t, report.Other)
	assert.NotEmpty(t, report.RunID)
	assert.NotEmpty(t, report.RecipeName)
	assert.NotEmpty(t, report.RecipeVersion)
	assert.False(t, report.CompletedAt.IsZero())

	// The merged CTRF report must be valid JSON whose summary matches the
	// translated counts.
	require.NotEmpty(t, report.CTRFReport)
	var merged ctrf.Report
	require.NoError(t, json.Unmarshal([]byte(report.CTRFReport), &merged))
	s := merged.Results.Summary
	assert.Equal(t, report.Passed, s.Passed)
	assert.Equal(t, report.Failed, s.Failed)
	assert.Equal(t, report.Skipped, s.Skipped)
	assert.Equal(t, report.Other, s.Other+s.Pending)
}

// TestValidateRunIDPassthroughAndGeneration covers the RunID contract:
// caller-provided IDs pass through; empty generates a non-empty one.
func TestValidateRunIDPassthroughAndGeneration(t *testing.T) {
	passed, err := Validate(t.Context(), kindCriteria, ValidateOptions{
		NoCluster: true,
		Phases:    []string{"deployment"},
		RunID:     "my-run-id",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-run-id", passed.RunID)

	generated, err := Validate(t.Context(), kindCriteria, ValidateOptions{
		NoCluster: true,
		Phases:    []string{"deployment"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, generated.RunID)
	assert.NotEqual(t, "my-run-id", generated.RunID)
}

// TestValidateRejectsEmptyPhases guards against the SDK's run-everything
// default: an empty phase list must never reach ValidateState.
func TestValidateRejectsEmptyPhases(t *testing.T) {
	_, err := Validate(t.Context(), kindCriteria, ValidateOptions{NoCluster: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phases must be non-empty")
}

// TestReadinessClassificationLiveSDKPath is the pinned-prefix tripwire: it
// drives the REAL checkReadiness path offline. The readiness pre-flight runs
// before the NoCluster short-circuit, so a bare Snapshot (no measurements)
// fails the resolved recipe's K8s-version constraint without any cluster.
// If an SDK bump changes the readiness message prefixes, this test breaks —
// in CI, not in production classification.
func TestReadinessClassificationLiveSDKPath(t *testing.T) {
	client, err := aicrclient.NewClient(
		aicrclient.WithRecipeSource(aicrclient.EmbeddedSource()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.ResolveRecipe(t.Context(), aicrclient.RecipeRequest{
		Service:     "eks",
		Accelerator: "h100",
		Intent:      "training",
	})
	require.NoError(t, err)

	_, err = client.ValidateState(t.Context(), result, &aicrclient.Snapshot{},
		aicrclient.WithValidationNoCluster(true),
		aicrclient.WithValidationPhases(aicrclient.PhaseDeployment),
	)
	require.Error(t, err, "a bare snapshot must fail the readiness pre-flight")

	msg, ok := isReadinessError(err)
	assert.True(t, ok, "readiness pre-flight failure not classified as readiness: %v", err)
	assert.NotEmpty(t, msg)
}

// TestValidateTranslatesReadinessFailure proves the adapter's own Validate
// maps a readiness failure to a report (not an error): a NoCluster run whose
// synthetic snapshot cannot satisfy an OS-pinned recipe's constraints.
func TestValidateTranslatesReadinessFailure(t *testing.T) {
	report, err := Validate(t.Context(), Criteria{
		Service:     "eks",
		Accelerator: "h100",
		Intent:      "training",
		OS:          "ubuntu", // OS mixin constraints cannot be satisfied by the K8s-only synthetic snapshot
	}, ValidateOptions{
		NoCluster: true,
		Phases:    []string{"deployment"},
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, OutcomeReadinessFailed, report.Outcome)
	assert.NotEmpty(t, report.ReadinessMessage)
	assert.Empty(t, report.Checks)
	assert.Zero(t, report.Passed+report.Failed+report.Skipped+report.Other)
	assert.NotEmpty(t, report.RunID)
	assert.NotEmpty(t, report.RecipeName)
	assert.False(t, report.CompletedAt.IsZero())
}

func TestIsReadinessError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantOK  bool
		wantMsg string
	}{
		{
			name: "readiness check failed",
			err: aicrerrors.New(aicrerrors.ErrCodeInvalidRequest,
				"readiness check failed: OS.release.ID expected ubuntu, got cos"),
			wantOK:  true,
			wantMsg: "readiness check failed: OS.release.ID expected ubuntu, got cos",
		},
		{
			name: "readiness check could not evaluate (with context)",
			err: aicrerrors.WrapWithContext(aicrerrors.ErrCodeInvalidRequest,
				"readiness check could not evaluate: K8s.server.version",
				fmt.Errorf("value not found in snapshot"),
				map[string]any{"constraint": "K8s.server.version", "expected": ">= 1.32"}),
			wantOK:  true,
			wantMsg: "readiness check could not evaluate: K8s.server.version",
		},
		{
			name: "shared code, non-readiness message",
			err: aicrerrors.New(aicrerrors.ErrCodeInvalidRequest,
				"aicr client not initialized"),
			wantOK: false,
		},
		{
			name: "readiness-looking message with wrong code",
			err: aicrerrors.New(aicrerrors.ErrCodeInternal,
				"readiness check failed: X expected 1, got 2"),
			wantOK: false,
		},
		{
			name: "wrapped chain still classified",
			err: fmt.Errorf("running AICR validation: %w",
				aicrerrors.New(aicrerrors.ErrCodeInvalidRequest,
					"readiness check failed: X expected 1, got 2")),
			wantOK:  true,
			wantMsg: "readiness check failed: X expected 1, got 2",
		},
		{
			name:   "plain error",
			err:    fmt.Errorf("readiness check failed: not structured"),
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := isReadinessError(tc.err)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantMsg, msg)
			} else {
				assert.Empty(t, msg)
			}
		})
	}
}

// fabricateReport builds a minimal CTRF report for buildReport tests.
func fabricateReport(tests []ctrf.TestResult) *ctrf.Report {
	summary := ctrf.Summary{Tests: len(tests)}
	for _, tr := range tests {
		switch tr.Status {
		case ctrf.StatusPassed:
			summary.Passed++
		case ctrf.StatusFailed:
			summary.Failed++
		case ctrf.StatusSkipped:
			summary.Skipped++
		case ctrf.StatusPending:
			summary.Pending++
		default:
			summary.Other++
		}
	}
	return &ctrf.Report{
		ReportFormat: "CTRF",
		SpecVersion:  "0.0.0",
		Results: ctrf.Results{
			Tool:    ctrf.Tool{Name: "test"},
			Summary: summary,
			Tests:   tests,
		},
	}
}

func phaseResult(phase string, report *ctrf.Report) *aicrclient.PhaseResult {
	return &aicrclient.PhaseResult{Phase: aicrclient.Phase(phase), Report: report}
}

func TestBuildReport(t *testing.T) {
	cases := []struct {
		name       string
		results    []*aicrclient.PhaseResult
		wantCounts [4]int // passed, failed, skipped, other
		wantChecks int
	}{
		{
			name: "all passed",
			results: []*aicrclient.PhaseResult{
				phaseResult("deployment", fabricateReport([]ctrf.TestResult{
					{Name: "a", Status: ctrf.StatusPassed},
					{Name: "b", Status: ctrf.StatusPassed},
				})),
			},
			wantCounts: [4]int{2, 0, 0, 0},
			wantChecks: 2,
		},
		{
			name: "one failed among passed",
			results: []*aicrclient.PhaseResult{
				phaseResult("deployment", fabricateReport([]ctrf.TestResult{
					{Name: "a", Status: ctrf.StatusPassed},
					{Name: "b", Status: ctrf.StatusFailed, Message: "driver not loaded"},
				})),
			},
			wantCounts: [4]int{1, 1, 0, 0},
			wantChecks: 2,
		},
		{
			name: "only other",
			results: []*aicrclient.PhaseResult{
				phaseResult("conformance", fabricateReport([]ctrf.TestResult{
					{Name: "a", Status: ctrf.StatusOther},
				})),
			},
			wantCounts: [4]int{0, 0, 0, 1},
			wantChecks: 1,
		},
		{
			name: "skipped and passed mix",
			results: []*aicrclient.PhaseResult{
				phaseResult("deployment", fabricateReport([]ctrf.TestResult{
					{Name: "a", Status: ctrf.StatusPassed},
					{Name: "b", Status: ctrf.StatusSkipped},
				})),
			},
			wantCounts: [4]int{1, 0, 1, 0},
			wantChecks: 2,
		},
		{
			name: "pending folded into other",
			results: []*aicrclient.PhaseResult{
				phaseResult("deployment", fabricateReport([]ctrf.TestResult{
					{Name: "a", Status: ctrf.StatusPending},
				})),
			},
			wantCounts: [4]int{0, 0, 0, 1},
			wantChecks: 1,
		},
		{
			name: "two phases merge",
			results: []*aicrclient.PhaseResult{
				phaseResult("deployment", fabricateReport([]ctrf.TestResult{
					{Name: "a", Status: ctrf.StatusPassed},
				})),
				phaseResult("conformance", fabricateReport([]ctrf.TestResult{
					{Name: "b", Status: ctrf.StatusFailed},
				})),
			},
			wantCounts: [4]int{1, 1, 0, 0},
			wantChecks: 2,
		},
		{
			name: "nil report skipped without error",
			results: []*aicrclient.PhaseResult{
				phaseResult("deployment", nil),
				phaseResult("conformance", fabricateReport([]ctrf.TestResult{
					{Name: "b", Status: ctrf.StatusPassed},
				})),
			},
			wantCounts: [4]int{1, 0, 0, 0},
			wantChecks: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checks, counts, mergedCTRF, err := buildReport(tc.results)
			require.NoError(t, err)
			assert.Equal(t, tc.wantCounts, counts)
			assert.Len(t, checks, tc.wantChecks)

			// Each check carries the owning phase.
			for _, c := range checks {
				assert.NotEmpty(t, c.Phase)
			}

			// Merged CTRF summary equals the summed counts (pending folds
			// into the other COUNT but stays pending in CTRF).
			require.NotEmpty(t, mergedCTRF)
			var merged ctrf.Report
			require.NoError(t, json.Unmarshal([]byte(mergedCTRF), &merged))
			s := merged.Results.Summary
			assert.Equal(t, tc.wantCounts[countPassed], s.Passed)
			assert.Equal(t, tc.wantCounts[countFailed], s.Failed)
			assert.Equal(t, tc.wantCounts[countSkipped], s.Skipped)
			assert.Equal(t, tc.wantCounts[countOther], s.Other+s.Pending)
		})
	}
}

func TestBuildReportPropagatesFailureMessage(t *testing.T) {
	checks, _, _, err := buildReport([]*aicrclient.PhaseResult{
		phaseResult("deployment", fabricateReport([]ctrf.TestResult{
			{Name: "gpu-operator-health", Status: ctrf.StatusFailed, Message: "driver.enabled=false"},
		})),
	})
	require.NoError(t, err)
	require.Len(t, checks, 1)
	assert.Equal(t, "gpu-operator-health", checks[0].Name)
	assert.Equal(t, "deployment", checks[0].Phase)
	assert.Equal(t, "failed", checks[0].Status)
	assert.Equal(t, "driver.enabled=false", checks[0].Message)
}

// TestRollupOutcome covers the rollup verdict derivation: any failed check
// fails the run; "other" (and everything else) never flips the rollup.
func TestRollupOutcome(t *testing.T) {
	cases := []struct {
		name   string
		counts [4]int // passed, failed, skipped, other
		want   Outcome
	}{
		{name: "all zero", counts: [4]int{0, 0, 0, 0}, want: OutcomePassed},
		{name: "only passed", counts: [4]int{3, 0, 0, 0}, want: OutcomePassed},
		{name: "only skipped", counts: [4]int{0, 0, 5, 0}, want: OutcomePassed},
		{name: "other never flips", counts: [4]int{2, 0, 1, 4}, want: OutcomePassed},
		{name: "single failed", counts: [4]int{0, 1, 0, 0}, want: OutcomeFailed},
		{name: "failed among passed and other", counts: [4]int{7, 2, 1, 3}, want: OutcomeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, rollupOutcome(tc.counts))
		})
	}
}

func TestBuildReportEmptyInput(t *testing.T) {
	checks, counts, mergedCTRF, err := buildReport(nil)
	require.NoError(t, err)
	assert.Empty(t, checks)
	assert.Equal(t, [4]int{}, counts)
	assert.Empty(t, mergedCTRF)
}

func TestAgentImage(t *testing.T) {
	cases := []struct {
		name     string
		info     *debug.BuildInfo
		ok       bool
		registry string
		envImage string
		want     string
		wantErr  string
	}{
		{
			name: "no override uses module version",
			info: depInfo(debug.Module{Path: aicrModulePath, Version: "v0.18.0"}),
			ok:   true,
			want: "ghcr.io/nvidia/aicr:v0.18.0",
		},
		{
			name:    "unresolvable version fails closed instead of :latest",
			info:    nil,
			ok:      false,
			wantErr: "cannot pin the snapshot-agent image",
		},
		{
			name:     "env override wins verbatim, bypassing registry override",
			info:     nil,
			ok:       false,
			envImage: "registry.dev.example/aicr-agent:pr-123",
			registry: "registry.example.com",
			want:     "registry.dev.example/aicr-agent:pr-123",
		},
		{
			name:     "registry override keeps repo and tag",
			info:     depInfo(debug.Module{Path: aicrModulePath, Version: "v0.18.0"}),
			ok:       true,
			registry: "registry.example.com",
			want:     "registry.example.com/nvidia/aicr:v0.18.0",
		},
		{
			name:     "registry override with trailing slash",
			info:     depInfo(debug.Module{Path: aicrModulePath, Version: "v0.18.0"}),
			ok:       true,
			registry: "registry.example.com/",
			want:     "registry.example.com/nvidia/aicr:v0.18.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withBuildInfo(t, tc.info, tc.ok)
			t.Setenv(agentImageEnvVar, tc.envImage)
			got, err := agentImage(tc.registry)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestValidateFailsClosedOnUnresolvableAgentImage proves the fail-closed
// contract propagates through Validate itself: on a real (non-NoCluster) run
// with an unresolvable AICR module version and no env override, Validate
// errors before any cluster contact (resolve is offline; agentImage runs
// before CollectSnapshot), and the error names the env-var escape hatch.
func TestValidateFailsClosedOnUnresolvableAgentImage(t *testing.T) {
	withBuildInfo(t, nil, false)
	t.Setenv(agentImageEnvVar, "")
	_, err := Validate(t.Context(), kindCriteria, ValidateOptions{
		Phases: []string{"deployment"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot pin the snapshot-agent image")
	assert.Contains(t, err.Error(), agentImageEnvVar,
		"the fail-closed error must name the env-var escape hatch")
}

func TestSDKVersionExported(t *testing.T) {
	withBuildInfo(t, depInfo(debug.Module{Path: aicrModulePath, Version: "v0.18.0"}), true)
	assert.Equal(t, "v0.18.0", SDKVersion())
}
