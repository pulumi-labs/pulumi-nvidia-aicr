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

func TestRenderManifestBundleDropsCommentOnlyDocuments(t *testing.T) {
	// A stitched bundle can mix a guarded-off section (rendering to only
	// its comment header) with real content. The comment-only document must
	// be dropped individually, not survive because a sibling document has
	// content.
	raw := `# This section is guarded off by values.
{{- if .Values.missing }}
kind: Never
{{- end }}
---
# A real resource follows.
apiVersion: v1
kind: ConfigMap
metadata:
  name: real
`
	out, err := renderManifestBundle(aicr.Component{Name: "mixed", Namespace: "default"}, raw)
	require.NoError(t, err)

	assert.Contains(t, out, "kind: ConfigMap")
	assert.NotContains(t, out, "guarded off", "comment-only document must be dropped")
	assert.False(t, strings.HasPrefix(strings.TrimSpace(out), "---"),
		"output must not lead with a stray separator")
	assert.Equal(t, 1, strings.Count(out, "kind:"), "exactly one document expected")
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
