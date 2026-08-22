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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolve is a test helper that resolves criteria against the SDK's embedded
// recipe data.
func resolve(t *testing.T, c Criteria) *Recipe {
	t.Helper()
	r, err := Resolve(context.Background(), c)
	require.NoError(t, err)
	return r
}

func componentNames(r *Recipe) []string {
	names := make([]string, 0, len(r.Components))
	for _, c := range r.Components {
		names = append(names, c.Name)
	}
	return names
}

func findComponent(t *testing.T, r *Recipe, name string) Component {
	t.Helper()
	for _, c := range r.Components {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("component %q not found in %v", name, componentNames(r))
	return Component{}
}

func TestResolveEKSH100TrainingKubeflow(t *testing.T) {
	r := resolve(t, Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training",
		OS: "ubuntu", Platform: "kubeflow",
	})

	assert.Equal(t, "h100-eks-ubuntu-training-kubeflow", r.Name)
	// Module binaries report the pinned SDK version (e.g. "v0.18.0"); test
	// binaries carry incomplete dependency build info and land on the
	// "embedded" fallback. Accept exactly those shapes — never "".
	// TestSDKModuleVersion covers each path against fabricated build info,
	// and CI asserts the shipped binary via `go version -m`.
	assert.Regexp(t, `^(v\d+\.\d+\.\d+.*|embedded)$`, r.Version,
		"recipe version must be a semver SDK version or the embedded fallback")

	names := componentNames(r)
	for _, expected := range []string{
		"cert-manager", "gpu-operator", "nvsentinel", "kube-prometheus-stack",
		"k8s-ephemeral-storage-metrics", "nvidia-dra-driver-gpu", "kai-scheduler",
		"aws-ebs-csi-driver", "aws-efa", "kubeflow-trainer",
	} {
		assert.Contains(t, names, expected)
	}

	// Every Helm component must carry chart coordinates; the trainer must
	// also carry recipe-resolved values.
	gpuOperator := findComponent(t, r, "gpu-operator")
	assert.NotEmpty(t, gpuOperator.Chart)
	assert.NotEmpty(t, gpuOperator.Repo)
	assert.NotEmpty(t, gpuOperator.Version)
	assert.NotEmpty(t, gpuOperator.Namespace)
	assert.NotEmpty(t, gpuOperator.Values)
}

func TestResolveDeploymentOrderRespectsDependencies(t *testing.T) {
	r := resolve(t, Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training",
		OS: "ubuntu", Platform: "kubeflow",
	})

	position := make(map[string]int, len(r.Components))
	for i, c := range r.Components {
		position[c.Name] = i
	}
	for _, c := range r.Components {
		for _, dep := range c.DependsOn {
			depPos, ok := position[dep]
			require.Truef(t, ok, "%s depends on %s which is not in the recipe", c.Name, dep)
			assert.Lessf(t, depPos, position[c.Name],
				"%s must deploy after its dependency %s", c.Name, dep)
		}
	}
}

func TestResolveEKSH100Inference(t *testing.T) {
	r := resolve(t, Criteria{
		Service: "eks", Accelerator: "h100", Intent: "inference", OS: "ubuntu",
	})

	names := componentNames(r)
	// The inference gateway is part of the base inference stack even when no
	// platform is selected; platform runtimes must not sneak in.
	assert.Contains(t, names, "agentgateway")
	assert.NotContains(t, names, "dynamo-platform")
	assert.NotContains(t, names, "kubeflow-trainer")
}

func TestResolveTrainingWithoutPlatformExcludesTrainer(t *testing.T) {
	r := resolve(t, Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training", OS: "ubuntu",
	})
	assert.NotContains(t, componentNames(r), "kubeflow-trainer")
	assert.Equal(t, "h100-eks-ubuntu-training", r.Name)
}

func TestResolveGB200LoadsPreManifests(t *testing.T) {
	// gb200-eks recipes attach kernel-module pre-manifests to gpu-operator.
	// The SDK facade's BundleComponents does not carry PreManifestFiles, so
	// this guards the adapter's own pre-manifest loading.
	r := resolve(t, Criteria{Service: "eks", Accelerator: "gb200", Intent: "training"})

	gpuOperator := findComponent(t, r, "gpu-operator")
	assert.NotEmpty(t, gpuOperator.PreManifests, "gb200 gpu-operator must carry pre-manifests")
	assert.NotEmpty(t, gpuOperator.Chart, "gpu-operator is still a Helm component")
}

func TestResolveManifestOnlyComponent(t *testing.T) {
	r := resolve(t, Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training", OS: "ubuntu",
	})
	// nodewright-customizations is a manifest-only component: no chart, raw
	// (Helm-template) manifest content instead.
	comp := findComponent(t, r, "nodewright-customizations")
	assert.Empty(t, comp.Chart)
	assert.NotEmpty(t, comp.Manifests)
}

// TestResolveKind proves the migration fixed the old custom resolver's bug
// where service=kind never resolved: the provider defaulted os to "ubuntu"
// while kind recipes bind no OS, so every kind resolution failed with "no
// matching recipe found". The SDK resolves kind with OS unset.
func TestResolveKind(t *testing.T) {
	cases := []struct {
		name     string
		criteria Criteria
		expect   []string
	}{
		{
			name:     "inference",
			criteria: Criteria{Service: "kind", Accelerator: "h100", Intent: "inference"},
			expect:   []string{"gpu-operator", "agentgateway"},
		},
		{
			name:     "training",
			criteria: Criteria{Service: "kind", Accelerator: "h100", Intent: "training"},
			expect:   []string{"gpu-operator"},
		},
		{
			name:     "inference with dynamo",
			criteria: Criteria{Service: "kind", Accelerator: "h100", Intent: "inference", Platform: "dynamo"},
			expect:   []string{"gpu-operator", "agentgateway", "dynamo-platform"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := resolve(t, tc.criteria)
			names := componentNames(r)
			for _, expected := range tc.expect {
				assert.Contains(t, names, expected)
			}
		})
	}
}

func TestResolveKindRejectsExplicitOS(t *testing.T) {
	// kind recipes bind no OS; an explicit os must fail with the SDK's
	// criteria error rather than resolving something unexpected.
	_, err := Resolve(context.Background(), Criteria{
		Service: "kind", Accelerator: "h100", Intent: "inference", OS: "ubuntu",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "os 'ubuntu'")
}

func TestResolvePlatformRequiresOS(t *testing.T) {
	// eks platform leaves are OS-pinned; the SDK's error names the valid
	// values so users know what to set.
	_, err := Resolve(context.Background(), Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training", Platform: "kubeflow",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires os")
	assert.Contains(t, err.Error(), "ubuntu")
}

func TestResolveUnknownCombinationFails(t *testing.T) {
	_, err := Resolve(context.Background(), Criteria{
		Service: "kind", Accelerator: "gb200", Intent: "training",
	})
	require.Error(t, err)
}

func TestApplyOverrides(t *testing.T) {
	newVersion := "v99.9.9"
	newNamespace := "custom-ns"
	base := &Recipe{
		Name:    "test",
		Version: "v0.0.0",
		Components: []Component{
			{
				Name: "gpu-operator", Chart: "gpu-operator", Version: "v1.0.0",
				Namespace: "gpu-operator",
				Values: map[string]interface{}{
					"driver":  map[string]interface{}{"enabled": true, "version": "550"},
					"toolkit": map[string]interface{}{"enabled": true},
				},
			},
			{Name: "cert-manager", Chart: "cert-manager", Version: "v1.2.3", Namespace: "cert-manager"},
		},
	}

	result := ApplyOverrides(base, map[string]ComponentOverride{
		"gpu-operator": {
			Version:   &newVersion,
			Namespace: &newNamespace,
			Values: map[string]interface{}{
				"driver":  map[string]interface{}{"version": "570"},
				"toolkit": nil,
			},
		},
	}, []string{"cert-manager"})

	require.Len(t, result.Components, 1)
	comp := result.Components[0]
	assert.Equal(t, "gpu-operator", comp.Name)
	assert.Equal(t, newVersion, comp.Version)
	assert.Equal(t, newNamespace, comp.Namespace)

	driver, ok := comp.Values["driver"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "570", driver["version"], "override wins")
	assert.Equal(t, true, driver["enabled"], "untouched sibling key survives the deep merge")
	assert.NotContains(t, comp.Values, "toolkit", "nil override deletes the key")
}

func TestApplyOverridesDoesNotMutateInput(t *testing.T) {
	// ApplyOverrides reads as copy-on-write; this pins that the merge
	// really does write into copies, not the maps the input recipe still
	// references (a trap if resolution is ever cached or reused).
	base := &Recipe{
		Components: []Component{{
			Name: "gpu-operator",
			Values: map[string]interface{}{
				"driver":  map[string]interface{}{"enabled": true, "version": "550"},
				"toolkit": map[string]interface{}{"enabled": true},
			},
		}},
	}

	_ = ApplyOverrides(base, map[string]ComponentOverride{
		"gpu-operator": {
			Values: map[string]interface{}{
				"driver":  map[string]interface{}{"version": "570"},
				"toolkit": nil,
				"extra":   "added",
			},
		},
	}, nil)

	orig := base.Components[0].Values
	driver := orig["driver"].(map[string]interface{})
	assert.Equal(t, "550", driver["version"], "nested merge must not leak into the input")
	assert.Contains(t, orig, "toolkit", "nil-delete must not remove keys from the input")
	assert.NotContains(t, orig, "extra", "added keys must not appear in the input")
}

func TestApplyOverridesOnResolvedRecipe(t *testing.T) {
	r := resolve(t, Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training", OS: "ubuntu",
	})
	before := len(r.Components)

	result := ApplyOverrides(r, map[string]ComponentOverride{
		"gpu-operator": {Values: map[string]interface{}{"extra": "value"}},
	}, []string{"cert-manager"})

	assert.Len(t, result.Components, before-1)
	assert.NotContains(t, componentNames(result), "cert-manager")
	gpuOperator := findComponent(t, result, "gpu-operator")
	assert.Equal(t, "value", gpuOperator.Values["extra"])
}

func TestDeepMergeMaps(t *testing.T) {
	dst := map[string]interface{}{
		"a": map[string]interface{}{"x": 1, "y": 2},
		"b": "keep",
		"c": "drop",
	}
	src := map[string]interface{}{
		"a": map[string]interface{}{"y": 3, "z": 4},
		"c": nil,
		"d": "new",
	}
	out := DeepMergeMaps(dst, src)

	a := out["a"].(map[string]interface{})
	assert.Equal(t, 1, a["x"])
	assert.Equal(t, 3, a["y"])
	assert.Equal(t, 4, a["z"])
	assert.Equal(t, "keep", out["b"])
	assert.NotContains(t, out, "c")
	assert.Equal(t, "new", out["d"])
}
