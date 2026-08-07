// Package aicr adapts the official NVIDIA AICR Go SDK
// (github.com/NVIDIA/aicr/pkg/client/v1) to the shape the provider's
// deployment loop consumes. It replaces the previous hand-rolled recipe
// resolver and vendored recipe data: resolution and component bundling are
// delegated to the SDK against its embedded recipe data, and only a thin
// post-processing layer (user overrides, skips) lives here.
//
// The SDK is used strictly for resolution and bundling. Deployment stays in
// Pulumi (helm.Release / ConfigGroup / Namespace child resources) so that
// preview, update, destroy, and state tracking work as usual.
package aicr

import (
	"context"
	"fmt"
	"runtime/debug"

	aicrclient "github.com/NVIDIA/aicr/pkg/client/v1"
	"github.com/NVIDIA/aicr/pkg/recipe"
	"gopkg.in/yaml.v3"
)

// aicrModulePath is the SDK module whose version is reported as the
// recipe-data version: the recipe YAML is embedded in that module, so its
// version pins the data.
const aicrModulePath = "github.com/NVIDIA/aicr"

// Criteria specifies the dimensions used to select an AICR recipe.
// Values must already be canonicalized (trimmed, lower-cased).
type Criteria struct {
	// Service is the Kubernetes service: "aks", "eks", "gke", "kind", "oke".
	Service string
	// Accelerator is the GPU type: "h100", "gb200", "b200".
	Accelerator string
	// Intent is the workload intent: "training", "inference".
	Intent string
	// OS is the worker-node operating system. Empty means unspecified: the
	// SDK resolves the OS-agnostic recipe and skips OS-pinned overlays.
	OS string
	// Platform is the optional ML platform: "kubeflow", "dynamo", "nim".
	Platform string
	// Nodes is the worker-node count hint. Zero means unspecified (the SDK
	// picks the default-sized recipe).
	Nodes int32
}

// Component is a single deployable component in SDK deployment order,
// pre-digested for the provider's deployment loop.
type Component struct {
	Name      string
	Chart     string
	Repo      string
	Version   string
	Namespace string
	// Values are the recipe-resolved Helm values (values file + recipe
	// overrides already merged by the SDK).
	Values map[string]interface{}
	// DependsOn lists names of components that must deploy first.
	DependsOn []string
	// PreManifests is raw (Helm-template) manifest text that must be applied
	// BEFORE the component's chart (e.g. a privileged Namespace the chart's
	// pods land in). Stitched multi-file, un-rendered.
	PreManifests string
	// Manifests is raw (Helm-template) manifest text applied alongside/after
	// the chart. Stitched multi-file, un-rendered.
	Manifests string
}

// Recipe is a fully resolved and bundled AICR recipe.
type Recipe struct {
	// Name is the leaf recipe identifier (e.g.
	// "h100-eks-ubuntu-training-kubeflow").
	Name string
	// Version is the AICR SDK module version providing the embedded recipe
	// data (e.g. "v0.18.0").
	Version string
	// Components is the deployable component list in SDK deployment order.
	Components []Component
}

// ComponentOverride lets callers customize a single component's chart
// version, target namespace, or Helm values. Values are deep-merged with
// the recipe defaults.
type ComponentOverride struct {
	Version   *string
	Namespace *string
	Values    map[string]interface{}
}

// Resolve resolves and bundles the AICR recipe for the given criteria using
// the SDK's embedded recipe data. It is pure and offline: no network or
// cluster access. The returned components are in the SDK's topologically
// sorted deployment order.
func Resolve(ctx context.Context, criteria Criteria) (*Recipe, error) {
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

	bundles, err := client.BundleComponents(ctx, result)
	if err != nil {
		return nil, fmt.Errorf("bundling AICR components: %w", err)
	}

	internal := result.Resolved()

	// BundleComponents mirrors result.Components 1:1 by index; key both by
	// name so components can be emitted in DeploymentOrder.
	bundleByName := make(map[string]aicrclient.ComponentBundle, len(bundles))
	for _, b := range bundles {
		bundleByName[b.Component.Name] = b
	}

	components := make([]Component, 0, len(bundles))
	for _, compName := range internal.DeploymentOrder {
		bundle, ok := bundleByName[compName]
		if !ok {
			// DeploymentOrder and ComponentRefs come from the same resolved
			// recipe; a mismatch means the SDK broke its own invariant.
			return nil, fmt.Errorf("component %q is in the recipe deployment order but has no bundle", compName)
		}
		delete(bundleByName, compName)

		comp, err := buildComponent(ctx, internal, bundle)
		if err != nil {
			return nil, err
		}
		components = append(components, comp)
	}
	if len(bundleByName) > 0 {
		names := make([]string, 0, len(bundleByName))
		for n := range bundleByName {
			names = append(names, n)
		}
		return nil, fmt.Errorf("components %v are missing from the recipe deployment order", names)
	}

	return &Recipe{
		Name:       recipeName(internal, result.Name),
		Version:    sdkModuleVersion(),
		Components: components,
	}, nil
}

// buildComponent translates one SDK component bundle into the deployment
// shape, loading pre-manifests the facade bundle does not carry.
func buildComponent(ctx context.Context, internal *recipe.RecipeResult, bundle aicrclient.ComponentBundle) (Component, error) {
	facade := bundle.Component
	comp := Component{
		Name:      facade.Name,
		Chart:     facade.Chart,
		Repo:      facade.Source,
		Version:   facade.Version,
		Namespace: facade.Namespace,
		Manifests: string(bundle.Manifests),
	}

	if len(bundle.HelmValues) > 0 {
		if err := yaml.Unmarshal(bundle.HelmValues, &comp.Values); err != nil {
			return Component{}, fmt.Errorf("decoding Helm values for %s: %w", facade.Name, err)
		}
	}

	ref := internal.GetComponentRef(facade.Name)
	if ref == nil {
		return Component{}, fmt.Errorf("component %q missing from resolved recipe", facade.Name)
	}
	comp.DependsOn = append([]string(nil), ref.DependencyRefs...)

	// The facade's BundleComponents stitches ManifestFiles but not
	// PreManifestFiles (those are handled only by the SDK's own deployers,
	// which we don't use), so load them here through the same per-Client
	// DataProvider the rest of the bundle used.
	if len(ref.PreManifestFiles) > 0 {
		var combined []byte
		for _, path := range ref.PreManifestFiles {
			content, err := recipe.GetManifestContentWithContext(ctx, internal.DataProvider(), path)
			if err != nil {
				return Component{}, fmt.Errorf("reading pre-manifest %s for %s: %w", path, facade.Name, err)
			}
			if len(combined) > 0 {
				combined = append(combined, []byte("\n---\n")...)
			}
			combined = append(combined, content...)
		}
		comp.PreManifests = string(combined)
	}

	return comp, nil
}

// recipeName derives the human-readable recipe identifier. The facade Name is
// a criteria string ("criteria(service=eks, ...)"); the last applied overlay
// is the leaf recipe name (e.g. "h100-eks-ubuntu-training-kubeflow"), which
// matches the recipe names this provider has always reported.
func recipeName(internal *recipe.RecipeResult, fallback string) string {
	if n := len(internal.Metadata.AppliedOverlays); n > 0 {
		return internal.Metadata.AppliedOverlays[n-1]
	}
	return fallback
}

// readBuildInfo is debug.ReadBuildInfo, indirected so tests can inject
// fabricated build info: Go test binaries carry incomplete dependency
// build info, so the real function cannot exercise the version path there.
var readBuildInfo = debug.ReadBuildInfo

// sdkModuleVersion reports the pinned version of the AICR SDK module, which
// also versions the embedded recipe data. Falls back to "embedded" when build
// info is unavailable or carries no usable version (test binaries, and
// filesystem `replace` directives whose Replace.Version is empty or
// "(devel)"). CI asserts the shipped binary's build info resolves to a real
// version via `go version -m`.
func sdkModuleVersion() string {
	if info, ok := readBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path != aicrModulePath {
				continue
			}
			if dep.Replace != nil && dep.Replace.Version != "" && dep.Replace.Version != "(devel)" {
				return dep.Replace.Version
			}
			if dep.Version != "" {
				return dep.Version
			}
		}
	}
	return "embedded"
}

// ApplyOverrides applies per-component user overrides (version, namespace,
// deep-merged values) and removes skipped components. Skipping a component
// does not remove it from other components' DependsOn lists; the deployment
// loop treats dependencies on absent components as satisfied.
func ApplyOverrides(r *Recipe, overrides map[string]ComponentOverride, skipComponents []string) *Recipe {
	skipSet := make(map[string]bool, len(skipComponents))
	for _, s := range skipComponents {
		skipSet[s] = true
	}

	filtered := make([]Component, 0, len(r.Components))
	for _, comp := range r.Components {
		if skipSet[comp.Name] {
			continue
		}
		if override, ok := overrides[comp.Name]; ok {
			if override.Version != nil {
				comp.Version = *override.Version
			}
			if override.Namespace != nil {
				comp.Namespace = *override.Namespace
			}
			if override.Values != nil {
				comp.Values = DeepMergeMaps(comp.Values, override.Values)
			}
		}
		filtered = append(filtered, comp)
	}

	result := *r
	result.Components = filtered
	return &result
}

// DeepMergeMaps recursively merges src into dst. Values in src take
// precedence. A nil value in src deletes the key from dst.
func DeepMergeMaps(dst, src map[string]interface{}) map[string]interface{} {
	if dst == nil {
		dst = make(map[string]interface{})
	}
	if src == nil {
		return dst
	}
	for k, sv := range src {
		if sv == nil {
			delete(dst, k)
			continue
		}
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		dMap, dOk := toMap(dv)
		sMap, sOk := toMap(sv)
		if dOk && sOk {
			dst[k] = DeepMergeMaps(dMap, sMap)
		} else {
			dst[k] = sv
		}
	}
	return dst
}

// toMap attempts to convert an interface{} to map[string]interface{}.
func toMap(v interface{}) (map[string]interface{}, bool) {
	switch m := v.(type) {
	case map[string]interface{}:
		return m, true
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(m))
		for k, v := range m {
			result[fmt.Sprintf("%v", k)] = v
		}
		return result, true
	default:
		return nil, false
	}
}
