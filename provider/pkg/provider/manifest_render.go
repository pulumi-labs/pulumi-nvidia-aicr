package provider

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/recipe"
	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/recipes"
)

// renderManifestBundle renders a component's embedded manifest files through
// the Helm template engine. AICR ships these manifests as Helm templates
// (they reference .Values, .Release, .Chart, etc. — see e.g.
// components/skyhook-customizations/manifests/tuning-gke.yaml), so feeding
// the raw bytes to a Kubernetes ConfigGroup would either fail to parse or
// produce nonsense. We synthesize a tiny in-memory chart from the manifest
// files and render it with the same values context the matching Helm
// release uses, then concatenate the documents into a multi-doc YAML
// payload suitable for kubernetes:yaml/v2:ConfigGroup.
func renderManifestBundle(comp recipe.ResolvedComponent) (string, error) {
	if len(comp.ManifestFiles) == 0 {
		return "", nil
	}

	syntheticChart := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:       comp.Name,
			Version:    nonEmpty(comp.Version, "0.0.0"),
			APIVersion: chart.APIVersionV2,
		},
	}
	for _, p := range comp.ManifestFiles {
		data, err := recipes.FS.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("reading manifest %s: %w", p, err)
		}
		// Helm requires templates under "templates/" within the chart;
		// rebase to the base name to avoid path collisions.
		syntheticChart.Templates = append(syntheticChart.Templates, &chart.File{
			Name: "templates/" + path.Base(p),
			Data: data,
		})
	}

	// AICR templates dereference .Values keyed by component name (e.g.
	// `index .Values "gpu-operator"`), so wrap the component's resolved
	// values under that key. The component's own Helm release uses these
	// same values un-keyed, but the manifest templates were authored
	// against the multi-component AICR umbrella chart layout.
	values := map[string]interface{}{
		comp.Name: cloneValues(comp.Values),
	}

	releaseOpts := chartutil.ReleaseOptions{
		Name:      comp.Name,
		Namespace: nonEmpty(comp.Namespace, "default"),
		Revision:  1,
		IsInstall: true,
	}
	caps := chartutil.DefaultCapabilities
	renderVals, err := chartutil.ToRenderValues(syntheticChart, values, releaseOpts, caps)
	if err != nil {
		return "", fmt.Errorf("preparing render values for %s: %w", comp.Name, err)
	}

	rendered, err := engine.Engine{}.Render(syntheticChart, renderVals)
	if err != nil {
		return "", fmt.Errorf("rendering manifests for %s: %w", comp.Name, err)
	}

	// engine.Render returns a map; sort keys so output is deterministic.
	keys := make([]string, 0, len(rendered))
	for k := range rendered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		body := stripCommentOnly(rendered[k])
		if body == "" {
			// A template guarded by `{{- if ... }}` renders to just the
			// leading comments outside the conditional — for example,
			// skyhook-customizations when `enabled: false`. Drop those
			// "empty after stripping comments" outputs so we don't emit
			// a no-op ConfigGroup downstream.
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// stripCommentOnly returns the input with leading whitespace and comment-
// only lines removed. If nothing substantive remains, it returns "".
func stripCommentOnly(s string) string {
	lines := strings.Split(s, "\n")
	hasContent := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		hasContent = true
		break
	}
	if !hasContent {
		return ""
	}
	return strings.TrimSpace(s)
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// cloneValues returns a defensive copy so the renderer cannot mutate the
// recipe's resolved values map.
func cloneValues(v map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}
