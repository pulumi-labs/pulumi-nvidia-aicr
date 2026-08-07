package provider

import (
	"fmt"
	"sort"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/aicr"
)

// renderManifestBundle renders a component's raw manifest content through
// the Helm template engine. AICR ships these manifests as Helm templates
// (they reference .Values, .Release, .Chart, etc. — see e.g.
// components/nodewright-customizations/manifests/tuning.yaml in the AICR
// module), and the SDK's BundleComponents returns them un-rendered (its own
// deployers wrap them as local charts and let Helm render at install time),
// so feeding the raw bytes to a Kubernetes ConfigGroup would either fail to
// parse or produce nonsense. We synthesize a tiny in-memory chart from the
// manifest content and render it with the same values context the matching
// Helm release uses, then concatenate the documents into a multi-doc YAML
// payload suitable for kubernetes:yaml/v2:ConfigGroup.
func renderManifestBundle(comp aicr.Component, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	syntheticChart := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:       comp.Name,
			Version:    nonEmpty(comp.Version, "0.0.0"),
			APIVersion: chart.APIVersionV2,
		},
		Templates: []*chart.File{{
			// The stitched multi-file content renders fine as a single
			// template: each source file's template code is self-contained
			// and "---" separators pass through as text.
			Name: "templates/manifests.yaml",
			Data: []byte(raw),
		}},
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
		// The stitched input can hold several YAML documents (the SDK joins
		// the recipe's manifest files with "---"). Strip comment-only
		// content per document, not per template: a guarded-off section
		// (`{{- if ... }}` rendering to just its comment header — e.g.
		// nodewright-customizations when `enabled: false`) inside a bundle
		// that also has real content would otherwise leave a stray comment
		// document in the output.
		for _, doc := range splitYAMLDocs(rendered[k]) {
			body := stripCommentOnly(doc)
			if body == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n---\n")
			}
			b.WriteString(body)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// splitYAMLDocs splits a multi-document YAML string on document separator
// lines ("---" alone on a line). This is a line-level split, not a YAML
// parse — sufficient here because the inputs are recipe manifest files
// joined with bare separators, and a rendered document never contains a
// bare "---" line of its own.
func splitYAMLDocs(s string) []string {
	var docs []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "---" {
			docs = append(docs, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	docs = append(docs, cur.String())
	return docs
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
