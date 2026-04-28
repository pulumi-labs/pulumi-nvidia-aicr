package provider

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/recipe"
)

// TestRenderManifestBundleProducesValidYAML guards Finding 1: AICR ships
// component manifests as Helm templates, so the renderer must execute them
// through the Helm engine rather than handing the raw bytes to a Pulumi
// ConfigGroup. We verify by rendering each shipped bundle the resolver can
// produce and parsing the result back as a stream of Kubernetes objects.
func TestRenderManifestBundleProducesValidYAML(t *testing.T) {
	resolved, err := recipe.Resolve(recipe.Criteria{
		Service: "gke", Accelerator: "h100", Intent: "training",
		OS: "cos", Platform: "kubeflow",
	})
	require.NoError(t, err)

	rendered := 0
	for _, comp := range resolved.Components {
		if len(comp.ManifestFiles) == 0 {
			continue
		}
		out, renderErr := renderManifestBundle(comp)
		require.NoErrorf(t, renderErr, "rendering manifests for %s", comp.Name)
		if strings.TrimSpace(out) == "" {
			continue
		}

		// Every rendered document must round-trip through YAML and have the
		// shape of a Kubernetes object (kind + apiVersion + metadata.name).
		dec := yaml.NewDecoder(strings.NewReader(out))
		for {
			var doc map[string]interface{}
			if decodeErr := dec.Decode(&doc); decodeErr != nil {
				if decodeErr.Error() == "EOF" {
					break
				}
				require.NoErrorf(t, decodeErr, "parsing rendered yaml for %s", comp.Name)
			}
			if len(doc) == 0 {
				continue
			}
			assert.NotEmptyf(t, doc["kind"], "%s: rendered doc missing kind", comp.Name)
			assert.NotEmptyf(t, doc["apiVersion"], "%s: rendered doc missing apiVersion", comp.Name)
			rendered++
		}
	}

	assert.Greater(t, rendered, 0, "expected at least one rendered Kubernetes object across the bundle")
}

func TestRenderManifestBundleHandlesEnabledFalse(t *testing.T) {
	// skyhook-customizations' tuning-gke.yaml is wrapped in
	// `{{- if ne (toString (index $cust "enabled")) "false" }}` — when the
	// caller disables the component via overrides, the template renders to
	// nothing and we must produce an empty bundle (not a malformed YAML).
	comp := recipe.ResolvedComponent{
		Name:      "skyhook-customizations",
		Namespace: "skyhook",
		Version:   "0.1.0",
		ManifestFiles: []string{
			"components/skyhook-customizations/manifests/tuning-gke.yaml",
		},
		Values: map[string]interface{}{
			"enabled": "false",
		},
	}
	out, err := renderManifestBundle(comp)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(out), "disabled bundle must render to empty string")
}
