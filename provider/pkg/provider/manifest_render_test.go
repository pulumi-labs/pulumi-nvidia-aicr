package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/aicr"
)

// TestRenderManifestBundleProducesValidYAML guards a long-standing finding:
// AICR ships component manifests as Helm templates, and the SDK's
// BundleComponents returns them un-rendered, so the renderer must execute
// them through the Helm engine rather than handing the raw bytes to a Pulumi
// ConfigGroup. We verify by rendering every manifest bundle a recipe
// produces and parsing the result back as a stream of Kubernetes objects.
func TestRenderManifestBundleProducesValidYAML(t *testing.T) {
	resolved, err := aicr.Resolve(context.Background(), aicr.Criteria{
		Service: "gke", Accelerator: "h100", Intent: "training",
		OS: "cos", Platform: "kubeflow",
	})
	require.NoError(t, err)

	rendered := 0
	for _, comp := range resolved.Components {
		for _, raw := range []string{comp.PreManifests, comp.Manifests} {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			out, renderErr := renderManifestBundle(comp, raw)
			require.NoErrorf(t, renderErr, "rendering manifests for %s", comp.Name)
			if strings.TrimSpace(out) == "" {
				continue
			}

			// Every rendered document must round-trip through YAML and have the
			// shape of a Kubernetes object (kind + apiVersion).
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
	}

	assert.Greater(t, rendered, 0, "expected at least one rendered Kubernetes object across the bundle")
}

func TestRenderManifestBundleHandlesEnabledFalse(t *testing.T) {
	// nodewright-customizations' tuning manifest is wrapped in
	// `{{- if ne (toString (index $cust "enabled")) "false" }}` — when the
	// caller disables the component via overrides, the template renders to
	// nothing and we must produce an empty bundle (not malformed YAML).
	resolved, err := aicr.Resolve(context.Background(), aicr.Criteria{
		Service: "eks", Accelerator: "h100", Intent: "training", OS: "ubuntu",
	})
	require.NoError(t, err)

	var comp *aicr.Component
	for i := range resolved.Components {
		if resolved.Components[i].Name == "nodewright-customizations" {
			comp = &resolved.Components[i]
			break
		}
	}
	require.NotNil(t, comp, "recipe no longer contains nodewright-customizations")
	require.NotEmpty(t, comp.Manifests)

	disabled := *comp
	disabled.Values = aicr.DeepMergeMaps(disabled.Values, map[string]interface{}{
		"enabled": "false",
	})
	out, err := renderManifestBundle(disabled, disabled.Manifests)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(out), "disabled bundle must render to empty string")
}
