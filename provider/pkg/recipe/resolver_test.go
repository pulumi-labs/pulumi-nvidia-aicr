package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveEksH100Training(t *testing.T) {
	resolved, err := Resolve(Criteria{
		Service:     "eks",
		Accelerator: "h100",
		Intent:      "training",
		OS:          "ubuntu",
		Platform:    "kubeflow",
	})
	require.NoError(t, err)
	require.NotNil(t, resolved)

	assert.Equal(t, "h100-eks-ubuntu-training-kubeflow", resolved.Name)
	assert.True(t, len(resolved.Components) > 0, "expected at least one component")

	// Verify base components are present
	componentNames := componentNameSet(resolved.Components)
	assert.Contains(t, componentNames, "cert-manager")
	assert.Contains(t, componentNames, "gpu-operator")
	assert.Contains(t, componentNames, "kube-prometheus-stack")
	assert.Contains(t, componentNames, "kai-scheduler")

	// Verify EKS-specific components
	assert.Contains(t, componentNames, "aws-ebs-csi-driver")
	assert.Contains(t, componentNames, "aws-efa")

	// Verify training platform component (from kubeflow mixin)
	assert.Contains(t, componentNames, "kubeflow-trainer")
}

func TestResolveEksH100Inference(t *testing.T) {
	resolved, err := Resolve(Criteria{
		Service:     "eks",
		Accelerator: "h100",
		Intent:      "inference",
		OS:          "ubuntu",
		Platform:    "dynamo",
	})
	require.NoError(t, err)
	require.NotNil(t, resolved)

	componentNames := componentNameSet(resolved.Components)

	// Verify base components
	assert.Contains(t, componentNames, "cert-manager")
	assert.Contains(t, componentNames, "gpu-operator")

	// Verify EKS-specific components
	assert.Contains(t, componentNames, "aws-ebs-csi-driver")

	// Verify inference platform components (from inference mixin)
	assert.Contains(t, componentNames, "dynamo-platform")
	assert.Contains(t, componentNames, "dynamo-crds")
	assert.Contains(t, componentNames, "kgateway")
	assert.Contains(t, componentNames, "kgateway-crds")

	// Training-specific should NOT be present
	assert.NotContains(t, componentNames, "kubeflow-trainer")
}

func TestResolveDefaultOS(t *testing.T) {
	// OS defaults to "ubuntu" when empty
	resolved, err := Resolve(Criteria{
		Service:     "eks",
		Accelerator: "h100",
		Intent:      "training",
		Platform:    "kubeflow",
	})
	require.NoError(t, err)
	assert.Equal(t, "ubuntu", resolved.Criteria.OS)
}

func TestResolveGB200(t *testing.T) {
	resolved, err := Resolve(Criteria{
		Service:     "eks",
		Accelerator: "gb200",
		Intent:      "training",
		OS:          "ubuntu",
		Platform:    "kubeflow",
	})
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.True(t, len(resolved.Components) > 0)

	componentNames := componentNameSet(resolved.Components)
	assert.Contains(t, componentNames, "gpu-operator")
}

func TestResolveStripsChartPrefix(t *testing.T) {
	// Registry stores chart names as "<repo-alias>/<chart>" (e.g.,
	// "jetstack/cert-manager"). The Pulumi Helm provider expects just
	// the chart name when an explicit repository URL is set.
	resolved, err := Resolve(Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training",
		OS: "ubuntu", Platform: "kubeflow",
	})
	require.NoError(t, err)

	cm := findComponent(resolved.Components, "cert-manager")
	require.NotNil(t, cm)
	assert.Equal(t, "cert-manager", cm.Chart, "chart prefix should be stripped")
	assert.Contains(t, cm.Repo, "jetstack.io")

	gpu := findComponent(resolved.Components, "gpu-operator")
	require.NotNil(t, gpu)
	assert.Equal(t, "gpu-operator", gpu.Chart, "chart prefix should be stripped")
}

func TestResolveKeepsManifestOnlyComponents(t *testing.T) {
	// Manifest-only AICR components (skyhook-customizations on GKE,
	// gke-nccl-tcpxo) carry no Helm chart but still need to be deployed
	// as raw Kubernetes manifests. Resolve must surface them with
	// ManifestFiles populated rather than silently dropping them.
	resolved, err := Resolve(Criteria{
		Service: "gke", Accelerator: "h100", Intent: "training",
		OS: "cos", Platform: "kubeflow",
	})
	require.NoError(t, err)

	cust := findComponent(resolved.Components, "skyhook-customizations")
	require.NotNil(t, cust, "skyhook-customizations should be resolved on gke/h100/training")
	assert.NotEmpty(t, cust.ManifestFiles, "manifest-only component must carry manifest paths")
	assert.Empty(t, cust.Chart, "skyhook-customizations has no Helm chart")

	tcpxo := findComponent(resolved.Components, "gke-nccl-tcpxo")
	require.NotNil(t, tcpxo, "gke-nccl-tcpxo should be resolved on gke/h100/training")
	assert.NotEmpty(t, tcpxo.ManifestFiles, "manifest-only component must carry manifest paths")

	// Components that only appear as inheritance scaffolding (no chart and
	// no manifests) must still be skipped — verify every survivor carries
	// at least one of the two.
	for _, c := range resolved.Components {
		hasChart := c.Chart != "" && c.Repo != ""
		hasManifests := len(c.ManifestFiles) > 0
		assert.True(t, hasChart || hasManifests,
			"resolved component %q must have a chart or manifests", c.Name)
	}
}

func TestResolveAttachesManifestsToHelmComponents(t *testing.T) {
	// gpu-operator on GKE COS ships a Helm chart *and* a pair of raw
	// manifests (dcgm-exporter, gke-resource-quota). The resolver must
	// preserve both so the deployer can apply each side-by-side.
	resolved, err := Resolve(Criteria{
		Service: "gke", Accelerator: "h100", Intent: "training",
		OS: "cos", Platform: "kubeflow",
	})
	require.NoError(t, err)

	gpu := findComponent(resolved.Components, "gpu-operator")
	require.NotNil(t, gpu)
	assert.NotEmpty(t, gpu.Chart)
	assert.NotEmpty(t, gpu.ManifestFiles, "gpu-operator on GKE COS includes side-car manifests")
}

func TestResolveInvalidCriteria(t *testing.T) {
	_, err := Resolve(Criteria{
		Service:     "nonexistent",
		Accelerator: "fake-gpu",
		Intent:      "unknown",
	})
	assert.Error(t, err)
}

func TestResolveRejectsSpecifiedQueryAgainstUnboundRecipe(t *testing.T) {
	// b200/eks/inference has no leaf in the recipe set. It must NOT
	// silently fall back to eks-inference.yaml (which has accelerator
	// unset) — that would deploy a recipe that ignores the b200 request.
	_, err := Resolve(Criteria{
		Service: "eks", Accelerator: "b200", Intent: "inference", OS: "ubuntu",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no matching recipe found")
}

func TestResolveUnsetPlatformExcludesPlatformComponents(t *testing.T) {
	// Public contract (per README and schema): leaving platform unset
	// returns the base recipe with no platform-specific runtime. The
	// resolver must not silently land on the kubeflow leaf and pull in
	// kubeflow-trainer, nor on the dynamo leaf and pull in the dynamo
	// stack, just because the scoring tied or wildcard-matched empty
	// against a concrete platform. (kgateway is *not* a platform-specific
	// runtime in the recipe data — eks-inference.yaml unconditionally
	// applies the platform-inference mixin as part of the inference base
	// — so it's expected to ship even when platform is unset.)
	training, err := Resolve(Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training", OS: "ubuntu",
	})
	require.NoError(t, err)
	trainingNames := componentNameSet(training.Components)
	assert.NotContains(t, trainingNames, "kubeflow-trainer",
		"unset platform must not pull in the kubeflow training mixin")

	inference, err := Resolve(Criteria{
		Service: "eks", Accelerator: "h100", Intent: "inference", OS: "ubuntu",
	})
	require.NoError(t, err)
	inferenceNames := componentNameSet(inference.Components)
	assert.NotContains(t, inferenceNames, "dynamo-platform",
		"unset platform must not pull in the dynamo platform stack")
	assert.NotContains(t, inferenceNames, "dynamo-crds",
		"unset platform must not pull in the dynamo platform stack")
	// kgateway IS expected — the recipe data ships it as part of the
	// inference base stack (eks-inference.yaml applies platform-inference
	// unconditionally), and the schema/README document this behavior.
	assert.Contains(t, inferenceNames, "kgateway",
		"intent=inference always includes the kgateway inference gateway")
	assert.Contains(t, inferenceNames, "kgateway-crds",
		"intent=inference always includes the kgateway CRDs")
}

func TestResolveRejectsPlatformAgainstUnboundRecipe(t *testing.T) {
	// h100/eks/inference/kubeflow has no leaf — kubeflow is training-only.
	// It must NOT silently fall back to h100-eks-ubuntu-inference.yaml
	// (which has platform unset) and quietly drop the platform request.
	_, err := Resolve(Criteria{
		Service: "eks", Accelerator: "h100", Intent: "inference",
		OS: "ubuntu", Platform: "kubeflow",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no matching recipe found")
}

func TestApplyOverrides(t *testing.T) {
	resolved := &ResolvedRecipe{
		Name:    "test-recipe",
		Version: "0.1.0",
		Components: []ResolvedComponent{
			{Name: "gpu-operator", Version: "v25.10.1", Values: map[string]interface{}{"driver": map[string]interface{}{"enabled": true}}},
			{Name: "cert-manager", Version: "v1.17.2", Values: map[string]interface{}{}},
			{Name: "to-skip", Version: "v1.0.0", Values: map[string]interface{}{}},
		},
	}

	newVersion := "v25.11.0"
	result := ApplyOverrides(resolved, map[string]ComponentOverride{
		"gpu-operator": {
			Version: &newVersion,
			Values:  map[string]interface{}{"driver": map[string]interface{}{"version": "535.0.0"}},
		},
	}, []string{"to-skip"})

	assert.Len(t, result.Components, 2)

	// Check gpu-operator was overridden
	gpuOp := findComponent(result.Components, "gpu-operator")
	require.NotNil(t, gpuOp)
	assert.Equal(t, "v25.11.0", gpuOp.Version)
	driver, ok := gpuOp.Values["driver"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "535.0.0", driver["version"])
	assert.Equal(t, true, driver["enabled"]) // preserved from base

	// Check to-skip was removed
	assert.Nil(t, findComponent(result.Components, "to-skip"))
}

func TestLoadRegistry(t *testing.T) {
	registry, err := LoadRegistry()
	require.NoError(t, err)
	require.NotNil(t, registry)

	// Verify known components exist
	gpu, ok := registry.LookupComponent("gpu-operator")
	assert.True(t, ok)
	assert.NotNil(t, gpu.Helm)
	assert.Contains(t, gpu.Helm.DefaultRepository, "nvidia")

	cert, ok := registry.LookupComponent("cert-manager")
	assert.True(t, ok)
	assert.NotNil(t, cert.Helm)
	assert.Contains(t, cert.Helm.DefaultRepository, "jetstack")
}

func TestDeepMergeMaps(t *testing.T) {
	dst := map[string]interface{}{
		"a": "1",
		"b": map[string]interface{}{
			"c": "2",
			"d": "3",
		},
	}
	src := map[string]interface{}{
		"b": map[string]interface{}{
			"c": "overridden",
			"e": "new",
		},
		"f": "added",
	}

	result := DeepMergeMaps(dst, src)
	assert.Equal(t, "1", result["a"])
	assert.Equal(t, "added", result["f"])

	b := result["b"].(map[string]interface{})
	assert.Equal(t, "overridden", b["c"])
	assert.Equal(t, "3", b["d"])
	assert.Equal(t, "new", b["e"])
}

func TestDeepMergeNilDelete(t *testing.T) {
	dst := map[string]interface{}{
		"keep": "yes",
		"del":  "this",
	}
	src := map[string]interface{}{
		"del": nil, // should delete
	}

	result := DeepMergeMaps(dst, src)
	assert.Equal(t, "yes", result["keep"])
	_, exists := result["del"]
	assert.False(t, exists)
}

func TestBuildRecipeName(t *testing.T) {
	name := buildRecipeName(Criteria{
		Service:     "eks",
		Accelerator: "h100",
		Intent:      "training",
		OS:          "ubuntu",
		Platform:    "kubeflow",
	})
	assert.Equal(t, "h100-eks-ubuntu-training-kubeflow", name)

	name = buildRecipeName(Criteria{
		Service:     "gke",
		Accelerator: "gb200",
		Intent:      "inference",
		OS:          "ubuntu",
	})
	assert.Equal(t, "gb200-gke-ubuntu-inference", name)
}

func componentNameSet(components []ResolvedComponent) map[string]bool {
	set := make(map[string]bool, len(components))
	for _, c := range components {
		set[c.Name] = true
	}
	return set
}

func findComponent(components []ResolvedComponent, name string) *ResolvedComponent {
	for i, c := range components {
		if c.Name == name {
			return &components[i]
		}
	}
	return nil
}
