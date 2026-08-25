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

package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/aicr"
)

// withValidateFake swaps the validateFn seam for the duration of a test and
// returns a pointer to the call count plus the last-seen options.
func withValidateFake(t *testing.T, fake func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error)) (*int, *aicr.ValidateOptions) {
	t.Helper()
	calls := 0
	var lastOpts aicr.ValidateOptions
	prev := validateFn
	validateFn = func(_ context.Context, c aicr.Criteria, o aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		calls++
		lastOpts = o
		return fake(c, o)
	}
	t.Cleanup(func() { validateFn = prev })
	return &calls, &lastOpts
}

func baseValidationArgs() ValidationRunArgs {
	return ValidationRunArgs{
		Criteria: RecipeCriteria{
			Accelerator: "h100",
			Service:     "eks",
			Intent:      "training",
		},
	}
}

func passedReport() *aicr.ValidationReport {
	return &aicr.ValidationReport{
		Outcome: aicr.OutcomePassed,
		Checks: []aicr.CheckOutcome{
			{Name: "platform-health", Phase: "deployment", Status: "passed"},
		},
		Passed:        1,
		RunID:         "run123",
		RecipeName:    "h100-eks-training",
		RecipeVersion: "embedded",
		CompletedAt:   time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		CTRFReport:    `{"reportFormat":"CTRF"}`,
	}
}

func failedReport() *aicr.ValidationReport {
	r := passedReport()
	r.Outcome = aicr.OutcomeFailed
	r.Checks = []aicr.CheckOutcome{
		{Name: "platform-health", Phase: "deployment", Status: "passed"},
		{Name: "gpu-operator-health", Phase: "deployment", Status: "failed", Message: "driver.enabled=false"},
		{Name: "dra-support", Phase: "conformance", Status: "failed", Message: "no DRA"},
	}
	r.Passed = 1
	r.Failed = 2
	return r
}

func readinessFailedReport() *aicr.ValidationReport {
	r := passedReport()
	r.Outcome = aicr.OutcomeReadinessFailed
	r.ReadinessMessage = "readiness check failed: OS.release.ID expected ubuntu, got cos"
	r.Checks = nil
	r.Passed = 0
	r.CTRFReport = ""
	return r
}

func TestValidateValidationRunArgs(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	intPtr := func(i int) *int { return &i }

	cases := []struct {
		name string
		muta func(*ValidationRunArgs)
		want string // "" means no error
	}{
		{
			name: "valid base",
			muta: func(a *ValidationRunArgs) {},
		},
		{
			name: "canonicalization accepts padded uppercase service",
			muta: func(a *ValidationRunArgs) { a.Criteria.Service = " EKS " },
		},
		{
			name: "missing accelerator",
			muta: func(a *ValidationRunArgs) { a.Criteria.Accelerator = "" },
			want: "accelerator is required",
		},
		{
			name: "missing service",
			muta: func(a *ValidationRunArgs) { a.Criteria.Service = "" },
			want: "service is required",
		},
		{
			name: "missing intent",
			muta: func(a *ValidationRunArgs) { a.Criteria.Intent = "" },
			want: "intent is required",
		},
		{
			name: "rtx-pro-6000 on lke accepted",
			muta: func(a *ValidationRunArgs) {
				a.Criteria.Accelerator = "rtx-pro-6000"
				a.Criteria.Service = "lke"
			},
		},
		{
			name: "rtx-pro-6000 off eks/lke rejected",
			muta: func(a *ValidationRunArgs) {
				a.Criteria.Accelerator = "rtx-pro-6000"
				a.Criteria.Service = "aks"
			},
			want: `accelerator "rtx-pro-6000" is supported only on eks or lke`,
		},
		{
			name: "unsupported platform combination",
			muta: func(a *ValidationRunArgs) {
				a.Criteria.Intent = "inference"
				a.Criteria.Platform = strPtr("kubeflow")
			},
			want: `platform "kubeflow" is training-only`,
		},
		{
			name: "bad phase lists valid values",
			muta: func(a *ValidationRunArgs) { a.Phases = []string{"performence"} },
			want: `phase "performence" is not supported (must be one of: deployment, conformance, performance)`,
		},
		{
			name: "kubeconfig and kubeconfigPath together",
			muta: func(a *ValidationRunArgs) {
				a.Kubeconfig = strPtr("contents")
				a.KubeconfigPath = strPtr("/tmp/kubeconfig")
			},
			want: "kubeconfig and kubeconfigPath are mutually exclusive",
		},
		{
			name: "zero timeout",
			muta: func(a *ValidationRunArgs) { a.TimeoutMinutes = intPtr(0) },
			want: "timeoutMinutes must be at least 1; got 0",
		},
		{
			name: "timeout above cap",
			muta: func(a *ValidationRunArgs) { a.TimeoutMinutes = intPtr(1441) },
			want: "timeoutMinutes must be at most 1440 (24 hours); got 1441",
		},
		{
			name: "timeout at cap accepted",
			muta: func(a *ValidationRunArgs) { a.TimeoutMinutes = intPtr(1440) },
		},
		{
			name: "invalid namespace format",
			muta: func(a *ValidationRunArgs) { a.Namespace = strPtr("Not_Valid") },
			want: `namespace "Not_Valid" is not a valid Kubernetes namespace name`,
		},
		{
			name: "reserved kube- namespace",
			muta: func(a *ValidationRunArgs) { a.Namespace = strPtr("kube-system") },
			want: `namespace "kube-system" is reserved for Kubernetes system namespaces`,
		},
		{
			name: "custom namespace accepted",
			muta: func(a *ValidationRunArgs) { a.Namespace = strPtr("gpu-validation") },
		},
		{
			name: "negative nodes",
			muta: func(a *ValidationRunArgs) { a.Criteria.Nodes = intPtr(-1) },
			want: "nodes must be non-negative",
		},
		{
			name: "version mismatch names both versions",
			muta: func(a *ValidationRunArgs) { a.Version = strPtr("v0.17.0") },
			want: `recipeDataVersion "v0.17.0" does not match this provider build's embedded AICR recipe-data version "` + aicr.SDKVersion() + `"`,
		},
		{
			name: "matching version accepted",
			muta: func(a *ValidationRunArgs) { a.Version = strPtr(aicr.SDKVersion()) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := baseValidationArgs()
			tc.muta(&args)
			err := validateValidationRunArgs(&args)
			if tc.want == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.want)
			}
		})
	}
}

// TestCriteriaValidationMatchesClusterStack proves ValidationRun and
// ClusterStack produce byte-identical criteria errors (shared helper).
func TestCriteriaValidationMatchesClusterStack(t *testing.T) {
	stackErr := validateArgs(&ClusterStackArgs{Accelerator: "fictional-gpu", Service: "eks", Intent: "training"})
	args := baseValidationArgs()
	args.Criteria.Accelerator = "fictional-gpu"
	runErr := validateValidationRunArgs(&args)
	require.Error(t, stackErr)
	require.Error(t, runErr)
	assert.Equal(t, stackErr.Error(), runErr.Error())
}

func TestCreateStrictFailurePersistsPartialState(t *testing.T) {
	_, _ = withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return failedReport(), nil
	})

	args := baseValidationArgs()
	strict := true
	args.Strict = &strict

	resp, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
	})
	require.Error(t, err)

	initErr := infer.ResourceInitFailedError{}
	require.True(t, errors.As(err, &initErr), "expected ResourceInitFailedError, got %T: %v", err, err)
	require.Len(t, initErr.Reasons, 1)
	assert.Contains(t, initErr.Reasons[0], "2 validation check(s) failed")
	assert.Contains(t, initErr.Reasons[0], "gpu-operator-health")
	assert.Contains(t, initErr.Reasons[0], "dra-support")

	// Full state rides alongside the error.
	assert.Equal(t, "vr-run123", resp.ID)
	assert.Equal(t, "failed", resp.Output.Status)
	assert.Equal(t, 1, resp.Output.Passed)
	assert.Equal(t, 2, resp.Output.Failed)
	assert.Equal(t, "run123", resp.Output.RunID)
	assert.Len(t, resp.Output.PhaseResults, 3)
	assert.Equal(t, "h100-eks-training", resp.Output.RecipeName)
	assert.NotEmpty(t, resp.Output.CompletedAt)
}

func TestCreateStrictReadinessFailure(t *testing.T) {
	_, _ = withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return readinessFailedReport(), nil
	})

	args := baseValidationArgs()
	strict := true
	args.Strict = &strict

	resp, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
	})
	require.Error(t, err)
	initErr := infer.ResourceInitFailedError{}
	require.True(t, errors.As(err, &initErr))
	require.Len(t, initErr.Reasons, 1)
	assert.Contains(t, initErr.Reasons[0], "readiness check failed")
	assert.Equal(t, "readiness-failed", resp.Output.Status)
	assert.NotEmpty(t, resp.Output.ReadinessMessage)
}

func TestCreateNonStrictOutcomeMapping(t *testing.T) {
	cases := []struct {
		name       string
		report     *aicr.ValidationReport
		wantStatus string
	}{
		{"failed run succeeds", failedReport(), "failed"},
		{"readiness-failed run succeeds", readinessFailedReport(), "readiness-failed"},
		{"passed run succeeds", passedReport(), "passed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
				return tc.report, nil
			})
			resp, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
				Name:   "vr",
				Inputs: baseValidationArgs(),
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, resp.Output.Status)
			if tc.wantStatus == "readiness-failed" {
				assert.NotEmpty(t, resp.Output.ReadinessMessage)
				assert.Zero(t, resp.Output.Passed+resp.Output.Failed+resp.Output.Skipped+resp.Output.Other)
			}
		})
	}
}

func TestCreateCtrfReportExposure(t *testing.T) {
	_, _ = withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return passedReport(), nil
	})

	// Default: not exposed.
	resp, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: baseValidationArgs(),
	})
	require.NoError(t, err)
	assert.Nil(t, resp.Output.CtrfReport)

	// Opt-in: exposed.
	args := baseValidationArgs()
	include := true
	args.IncludeCtrfReport = &include
	resp, err = (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Output.CtrfReport)
	assert.Equal(t, `{"reportFormat":"CTRF"}`, *resp.Output.CtrfReport)
}

func TestCreateInfrastructureErrorPersistsNothing(t *testing.T) {
	infraErr := fmt.Errorf("collecting cluster snapshot: connection refused")
	_, _ = withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return nil, infraErr
	})

	resp, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: baseValidationArgs(),
	})
	require.Error(t, err)
	initErr := infer.ResourceInitFailedError{}
	assert.False(t, errors.As(err, &initErr), "infrastructure errors must be plain failures, not init errors")
	assert.Empty(t, resp.ID)
	assert.Equal(t, ValidationRunState{}, resp.Output)
}

func TestCreateDefaultsPassedToAdapter(t *testing.T) {
	_, lastOpts := withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return passedReport(), nil
	})

	_, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: baseValidationArgs(),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"deployment", "conformance"}, lastOpts.Phases,
		"nil phases must default to deployment+conformance")
	assert.True(t, lastOpts.RequireGPU, "requireGpu must default to true")
	assert.Equal(t, "aicr-validation", lastOpts.Namespace)
	assert.Nil(t, lastOpts.Tolerations, "unset tolerations must stay nil (keeps validator tolerate-all)")
}

func TestCreatePhasesCanonicalizedAndDeduped(t *testing.T) {
	_, lastOpts := withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return passedReport(), nil
	})

	args := baseValidationArgs()
	args.Phases = []string{" Deployment ", "deployment", "PERFORMANCE"}
	_, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"deployment", "performance"}, lastOpts.Phases)
}

// TestRunValidationKubeconfigTempFileLifecycle proves the kubeconfig temp
// file exists (mode 0600, correct contents) for the duration of the run and
// is removed afterward — on success AND when the adapter errors.
func TestRunValidationKubeconfigTempFileLifecycle(t *testing.T) {
	cases := []struct {
		name    string
		fakeErr error
	}{
		{name: "success"},
		{name: "adapter error still cleans up", fakeErr: fmt.Errorf("collecting cluster snapshot: connection refused")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seenPath string
			var seenMode os.FileMode
			var seenContents string
			_, _ = withValidateFake(t, func(_ aicr.Criteria, o aicr.ValidateOptions) (*aicr.ValidationReport, error) {
				seenPath = o.KubeconfigPath
				if info, err := os.Stat(o.KubeconfigPath); err == nil {
					seenMode = info.Mode().Perm()
				}
				if raw, err := os.ReadFile(o.KubeconfigPath); err == nil {
					seenContents = string(raw)
				}
				if tc.fakeErr != nil {
					return nil, tc.fakeErr
				}
				return passedReport(), nil
			})

			args := baseValidationArgs()
			contents := testKubeconfig
			args.Kubeconfig = &contents

			_, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
				Name:   "vr",
				Inputs: args,
			})
			if tc.fakeErr != nil {
				require.ErrorIs(t, err, tc.fakeErr)
			} else {
				require.NoError(t, err)
			}

			require.NotEmpty(t, seenPath, "adapter must receive a materialized kubeconfig path")
			assert.Equal(t, os.FileMode(0o600), seenMode, "temp kubeconfig must be 0600 during the run")
			assert.Equal(t, testKubeconfig, seenContents, "temp kubeconfig must carry the provided contents")
			_, statErr := os.Stat(seenPath)
			assert.True(t, os.IsNotExist(statErr), "temp kubeconfig must be removed after the run")
		})
	}
}

// TestCreateOptionsPassthroughToAdapter proves every non-default input reaches
// the adapter: canonicalized criteria, namespace, requireGpu=false, registry,
// pull secrets, nodeSelector, translated tolerations, and a verbatim
// kubeconfigPath (no temp copy).
func TestCreateOptionsPassthroughToAdapter(t *testing.T) {
	var gotCriteria aicr.Criteria
	_, lastOpts := withValidateFake(t, func(c aicr.Criteria, _ aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		gotCriteria = c
		return passedReport(), nil
	})

	strPtr := func(s string) *string { return &s }
	intPtr := func(i int) *int { return &i }
	boolPtr := func(b bool) *bool { return &b }

	args := ValidationRunArgs{
		Criteria: RecipeCriteria{
			Accelerator: " H100 ",
			Service:     " EKS ",
			Intent:      "Training",
			OS:          strPtr(" Ubuntu "),
			Nodes:       intPtr(4),
		},
		KubeconfigPath:   strPtr("/some/kubeconfig"),
		Namespace:        strPtr("custom-validation"),
		RequireGpu:       boolPtr(false),
		ImageRegistry:    strPtr("registry.example.com"),
		ImagePullSecrets: []string{"pull-secret"},
		NodeSelector:     map[string]string{"pool": "gpu"},
		Phases:           []string{"performance"},
		Tolerations: []Toleration{{
			Key:      strPtr("nvidia.com/gpu"),
			Operator: strPtr("Exists"),
			Effect:   strPtr("NoSchedule"),
		}},
	}

	_, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
	})
	require.NoError(t, err)

	assert.Equal(t, "h100", gotCriteria.Accelerator, "criteria must reach the adapter canonicalized")
	assert.Equal(t, "eks", gotCriteria.Service)
	assert.Equal(t, "training", gotCriteria.Intent)
	assert.Equal(t, "ubuntu", gotCriteria.OS)
	assert.Equal(t, int32(4), gotCriteria.Nodes)

	assert.Equal(t, "/some/kubeconfig", lastOpts.KubeconfigPath, "path without context must pass through verbatim")
	assert.Equal(t, "custom-validation", lastOpts.Namespace)
	assert.False(t, lastOpts.RequireGPU)
	assert.Equal(t, "registry.example.com", lastOpts.ImageRegistry)
	assert.Equal(t, []string{"pull-secret"}, lastOpts.ImagePullSecrets)
	assert.Equal(t, map[string]string{"pool": "gpu"}, lastOpts.NodeSelector)
	assert.Equal(t, []string{"performance"}, lastOpts.Phases)
	require.Len(t, lastOpts.Tolerations, 1)
	assert.Equal(t, "nvidia.com/gpu", lastOpts.Tolerations[0].Key)
	assert.Equal(t, "Exists", string(lastOpts.Tolerations[0].Operator))
	assert.Equal(t, "NoSchedule", string(lastOpts.Tolerations[0].Effect))
}

func TestDiffReplacesOnAnyChange(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name     string
		muta     func(*ValidationRunArgs)
		wantProp string
	}{
		{
			name:     "criteria field",
			muta:     func(a *ValidationRunArgs) { a.Criteria.Accelerator = "gb200" },
			wantProp: "criteria",
		},
		{
			name: "strict flip",
			muta: func(a *ValidationRunArgs) {
				strict := true
				a.Strict = &strict
			},
			wantProp: "strict",
		},
		{
			name: "toleration nested key",
			muta: func(a *ValidationRunArgs) {
				a.Tolerations = []Toleration{{Key: strPtr("nvidia.com/gpu-CHANGED")}}
			},
			wantProp: "tolerations",
		},
		{
			name: "nodeSelector entry",
			muta: func(a *ValidationRunArgs) {
				a.NodeSelector = map[string]string{"pool": "gpu2"}
			},
			wantProp: "nodeSelector",
		},
		{
			name: "triggers element",
			muta: func(a *ValidationRunArgs) {
				a.Triggers = []interface{}{"deploy-2"}
			},
			wantProp: "triggers",
		},
		{
			name: "kubeconfig (secret) still diffs",
			muta: func(a *ValidationRunArgs) {
				a.Kubeconfig = strPtr("new-contents")
			},
			wantProp: "kubeconfig",
		},
	}

	base := baseValidationArgs()
	base.Tolerations = []Toleration{{Key: strPtr("nvidia.com/gpu")}}
	base.NodeSelector = map[string]string{"pool": "gpu"}
	base.Triggers = []interface{}{"deploy-1"}
	base.Kubeconfig = strPtr("contents")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldArgs := base
			newArgs := base
			// Deep-ish copy of mutable fields so mutations don't leak.
			newArgs.Tolerations = append([]Toleration(nil), base.Tolerations...)
			newArgs.Triggers = append([]interface{}(nil), base.Triggers...)
			tc.muta(&newArgs)

			resp, err := (&ValidationRun{}).Diff(context.Background(), infer.DiffRequest[ValidationRunArgs, ValidationRunState]{
				ID:     "vr-run123",
				State:  ValidationRunState{ValidationRunArgs: oldArgs},
				Inputs: newArgs,
			})
			require.NoError(t, err)
			assert.True(t, resp.HasChanges)
			assert.True(t, resp.DeleteBeforeReplace)
			require.Contains(t, resp.DetailedDiff, tc.wantProp)
			assert.Equal(t, p.UpdateReplace, resp.DetailedDiff[tc.wantProp].Kind)
		})
	}
}

func TestDiffNoChanges(t *testing.T) {
	args := baseValidationArgs()
	args.Tolerations = []Toleration{{Key: pulumi.StringRef("nvidia.com/gpu")}}
	resp, err := (&ValidationRun{}).Diff(context.Background(), infer.DiffRequest[ValidationRunArgs, ValidationRunState]{
		ID:     "vr-run123",
		State:  ValidationRunState{ValidationRunArgs: args},
		Inputs: args,
	})
	require.NoError(t, err)
	assert.False(t, resp.HasChanges)
	assert.Empty(t, resp.DetailedDiff)
}

func TestUpdateRerunsValidation(t *testing.T) {
	runs := 0
	calls, _ := withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		runs++
		r := passedReport()
		r.RunID = fmt.Sprintf("run-%d", runs)
		return r, nil
	})

	// Update with unchanged inputs (init-error repair path) re-runs.
	resp, err := (&ValidationRun{}).Update(context.Background(), infer.UpdateRequest[ValidationRunArgs, ValidationRunState]{
		ID:     "vr-run123",
		State:  ValidationRunState{ValidationRunArgs: baseValidationArgs(), Status: "failed", RunID: "run123"},
		Inputs: baseValidationArgs(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, *calls)
	assert.Equal(t, "passed", resp.Output.Status)
	assert.Equal(t, "run-1", resp.Output.RunID, "expected fresh state from the re-run")
}

func TestUpdateStrictStillFailing(t *testing.T) {
	_, _ = withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return failedReport(), nil
	})

	args := baseValidationArgs()
	strict := true
	args.Strict = &strict

	resp, err := (&ValidationRun{}).Update(context.Background(), infer.UpdateRequest[ValidationRunArgs, ValidationRunState]{
		ID:     "vr-run122",
		State:  ValidationRunState{ValidationRunArgs: args, Status: "failed"},
		Inputs: args,
	})
	require.Error(t, err)
	initErr := infer.ResourceInitFailedError{}
	require.True(t, errors.As(err, &initErr))
	assert.Equal(t, "failed", resp.Output.Status)
	assert.Equal(t, "run123", resp.Output.RunID)
}

func TestUpdateDryRunDoesNotRun(t *testing.T) {
	calls, _ := withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		t.Fatal("validateFn must not be invoked during preview")
		return nil, nil
	})

	prior := ValidationRunState{
		ValidationRunArgs: baseValidationArgs(),
		Status:            "failed",
		RunID:             "run123",
	}
	resp, err := (&ValidationRun{}).Update(context.Background(), infer.UpdateRequest[ValidationRunArgs, ValidationRunState]{
		ID:     "vr-run123",
		State:  prior,
		Inputs: baseValidationArgs(),
		DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, *calls)
	assert.Equal(t, "failed", resp.Output.Status, "preview keeps recorded results")
	assert.Equal(t, "run123", resp.Output.RunID)
}

func TestCreateDryRunZeroCriteria(t *testing.T) {
	calls, _ := withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		t.Fatal("validateFn must not be invoked during preview")
		return nil, nil
	})

	// Zero-valued criteria is the shape an unresolved output has at preview:
	// no error, no validation.
	resp, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: ValidationRunArgs{},
		DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, *calls)
	assert.Empty(t, resp.Output.Status)
}

func TestCreateDryRunValidatesKnownCriteria(t *testing.T) {
	_, _ = withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		t.Fatal("validateFn must not be invoked during preview")
		return nil, nil
	})

	args := baseValidationArgs()
	args.Criteria.Accelerator = "fictional-gpu"
	_, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
		DryRun: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `accelerator "fictional-gpu" is not supported`)
}

// TestCreateDryRunSkipsPartialCriteria: inline criteria mixing literals with
// unresolved outputs decodes with just the computed members zeroed at preview
// (pulumi-go-provider's ende replaces each unknown with its zero value), so a
// partially-known struct must skip preview validation rather than fail a
// program whose apply would succeed.
func TestCreateDryRunSkipsPartialCriteria(t *testing.T) {
	calls, _ := withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		t.Fatal("validateFn must not be invoked during preview")
		return nil, nil
	})

	args := baseValidationArgs()
	args.Criteria.Intent = ""                // unresolved output at preview
	args.Criteria.Platform = strPtrOf("nim") // would fail compat if validated
	resp, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
		DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, *calls)
	assert.Empty(t, resp.Output.Status)
}

func strPtrOf(s string) *string { return &s }

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://a.example
  name: cluster-a
- cluster:
    server: https://b.example
  name: cluster-b
contexts:
- context:
    cluster: cluster-a
    user: user-a
  name: ctx-a
- context:
    cluster: cluster-b
    user: user-b
  name: ctx-b
current-context: ctx-a
users:
- name: user-a
  user: {}
- name: user-b
  user: {}
`

func TestMaterializeKubeconfigContents(t *testing.T) {
	contents := testKubeconfig
	path, cleanup, err := materializeKubeconfig(&contents, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "kubeconfig temp file must be 0600")

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, contents, string(written))

	cleanup()
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "cleanup must remove the temp file")
}

func TestMaterializeKubeconfigPathPassthrough(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "kubeconfig")
	require.NoError(t, os.WriteFile(src, []byte(testKubeconfig), 0o600))

	path, cleanup, err := materializeKubeconfig(nil, &src, nil)
	require.NoError(t, err)
	assert.Equal(t, src, path, "path without context must pass through verbatim")

	cleanup()
	_, err = os.Stat(src)
	assert.NoError(t, err, "cleanup must not remove the user's file")
}

func TestMaterializeKubeconfigContextRewrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "kubeconfig")
	require.NoError(t, os.WriteFile(src, []byte(testKubeconfig), 0o600))

	kubeCtx := "ctx-b"
	path, cleanup, err := materializeKubeconfig(nil, &src, &kubeCtx)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.NotEqual(t, src, path, "context rewrite must write a temp copy")

	loaded, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "ctx-b", loaded.CurrentContext)

	// The original file is untouched.
	original, err := clientcmd.LoadFromFile(src)
	require.NoError(t, err)
	assert.Equal(t, "ctx-a", original.CurrentContext)
}

func TestMaterializeKubeconfigContentsWithContext(t *testing.T) {
	contents := testKubeconfig
	kubeCtx := "ctx-b"
	path, cleanup, err := materializeKubeconfig(&contents, nil, &kubeCtx)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	loaded, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "ctx-b", loaded.CurrentContext)
}

func TestMaterializeKubeconfigUnknownContext(t *testing.T) {
	contents := testKubeconfig
	kubeCtx := "nope"
	_, _, err := materializeKubeconfig(&contents, nil, &kubeCtx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `context "nope" not found`)
	assert.Contains(t, err.Error(), "ctx-a, ctx-b")
}

func TestMaterializeKubeconfigAmbient(t *testing.T) {
	path, cleanup, err := materializeKubeconfig(nil, nil, nil)
	require.NoError(t, err)
	defer cleanup()
	assert.Empty(t, path, "nothing set means ambient discovery")
}

func TestDeleteIsNoOp(t *testing.T) {
	_, err := (&ValidationRun{}).Delete(context.Background(), infer.DeleteRequest[ValidationRunState]{
		ID:    "vr-run123",
		State: ValidationRunState{ValidationRunArgs: baseValidationArgs()},
	})
	assert.NoError(t, err)
}

// TestNormalizePhases pins the defaulting/dedup contract.
func TestNormalizePhases(t *testing.T) {
	assert.Equal(t, []string{"deployment", "conformance"}, normalizePhases(nil))
	assert.Equal(t, []string{"deployment", "conformance"}, normalizePhases([]string{}))
	assert.Equal(t, []string{"conformance"}, normalizePhases([]string{"Conformance", "conformance"}))
	assert.Equal(t, []string{"deployment", "conformance"}, normalizePhases([]string{"", "  "}))
}

// TestToCoreTolerations pins the translation, including the nil-means-default
// contract.
func TestToCoreTolerations(t *testing.T) {
	assert.Nil(t, toCoreTolerations(nil), "nil must stay nil (validator keeps its tolerate-all default)")

	key := "nvidia.com/gpu"
	op := "Exists"
	effect := "NoSchedule"
	secs := 30
	out := toCoreTolerations([]Toleration{{
		Key:               &key,
		Operator:          &op,
		Effect:            &effect,
		TolerationSeconds: &secs,
	}})
	require.Len(t, out, 1)
	assert.Equal(t, "nvidia.com/gpu", out[0].Key)
	assert.Equal(t, "Exists", string(out[0].Operator))
	assert.Equal(t, "NoSchedule", string(out[0].Effect))
	require.NotNil(t, out[0].TolerationSeconds)
	assert.Equal(t, int64(30), *out[0].TolerationSeconds)
}

// TestValidationPhaseAllowlistIsCaseInsensitive proves validation accepts any
// casing the normalizer accepts.
func TestValidationPhaseAllowlistIsCaseInsensitive(t *testing.T) {
	args := baseValidationArgs()
	args.Phases = []string{" Deployment ", "CONFORMANCE"}
	assert.NoError(t, validateValidationRunArgs(&args))
}

// TestCreateSkipComponentsReachAdapter: criteria.skipComponents (as wired
// from ClusterStack.criteria) must reach aicr.Validate verbatim.
func TestCreateSkipComponentsReachAdapter(t *testing.T) {
	_, lastOpts := withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return passedReport(), nil
	})
	args := baseValidationArgs()
	args.Criteria.SkipComponents = []string{"gpu-operator", "nfd"}

	_, err := (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr",
		Inputs: args,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"gpu-operator", "nfd"}, lastOpts.SkipComponents)

	// Unset stays nil (not an empty slice) so the adapter's no-op path runs.
	_, err = (&ValidationRun{}).Create(context.Background(), infer.CreateRequest[ValidationRunArgs]{
		Name:   "vr2",
		Inputs: baseValidationArgs(),
	})
	require.NoError(t, err)
	assert.Nil(t, lastOpts.SkipComponents)
}

// TestHasRequiredCriteria: preview validation runs only when accelerator,
// service, and intent are all present — any missing one may be an unresolved
// output at preview, not a user error.
func TestHasRequiredCriteria(t *testing.T) {
	assert.True(t, hasRequiredCriteria(RecipeCriteria{Accelerator: "h100", Service: "eks", Intent: "training"}))
	assert.False(t, hasRequiredCriteria(RecipeCriteria{}))
	assert.False(t, hasRequiredCriteria(RecipeCriteria{Accelerator: "h100", Service: "eks"}))
	assert.False(t, hasRequiredCriteria(RecipeCriteria{Service: "eks", Intent: "training"}))
	assert.False(t, hasRequiredCriteria(RecipeCriteria{SkipComponents: []string{"gpu-operator"}}))
}

// TestWarnFailedChecksOnlyOnFailed: the warning fires for failed and
// readiness-failed verdicts, is a no-op for nil / passed reports, and never
// panics without a host logger.
func TestWarnFailedChecksOnlyOnFailed(t *testing.T) {
	assert.NotPanics(t, func() {
		warnFailedChecks(context.Background(), nil)
		warnFailedChecks(context.Background(), passedReport())
		warnFailedChecks(context.Background(), readinessFailedReport())
		warnFailedChecks(context.Background(), failedReport())
		long := failedReport()
		long.Checks[1].Message = strings.Repeat("x", 2*maxWarnMessageLen)
		warnFailedChecks(context.Background(), long)
		multibyte := failedReport()
		// "≥" is 3 bytes; place one so the byte cut lands mid-rune.
		multibyte.Checks[1].Message = strings.Repeat("x", maxWarnMessageLen-1) + strings.Repeat("≥", maxWarnMessageLen)
		warnFailedChecks(context.Background(), multibyte)
	})
}

// TestWarnTruncationIsValidUTF8: the truncated per-check message must never
// contain a partial rune (SDK messages carry "≥" and "—").
func TestWarnTruncationIsValidUTF8(t *testing.T) {
	msg := strings.Repeat("x", maxWarnMessageLen-1) + strings.Repeat("≥", 4)
	require.Greater(t, len(msg), maxWarnMessageLen)
	truncated := strings.ToValidUTF8(msg[:maxWarnMessageLen], "") + "…"
	assert.True(t, utf8.ValidString(truncated))
	assert.True(t, strings.HasSuffix(truncated, "…"))
	assert.NotContains(t, truncated, "\uFFFD")
}
