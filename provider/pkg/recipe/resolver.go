package recipe

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/recipes"
)

const recipeVersion = "0.1.0"

// Resolve takes recipe criteria and returns a fully resolved recipe with all
// components ready for deployment.
func Resolve(criteria Criteria) (*ResolvedRecipe, error) {
	if criteria.OS == "" {
		criteria.OS = "ubuntu"
	}

	// Load all recipe data
	allRecipes, err := loadAllRecipes()
	if err != nil {
		return nil, fmt.Errorf("loading recipes: %w", err)
	}

	// Load component registry
	registry, err := LoadRegistry()
	if err != nil {
		return nil, fmt.Errorf("loading registry: %w", err)
	}

	// Find the best matching recipe
	match, err := findBestMatch(allRecipes, criteria)
	if err != nil {
		return nil, err
	}

	// Resolve the inheritance chain
	chain, err := resolveInheritanceChain(allRecipes, match)
	if err != nil {
		return nil, err
	}

	// Merge the chain bottom-up (base first, leaf last)
	merged := mergeChain(chain)

	// Apply mixins
	for _, mixinName := range merged.Spec.Mixins {
		if mixin, ok := allRecipes[mixinName]; ok {
			merged.Spec.ComponentRefs = mergeComponentRefs(merged.Spec.ComponentRefs, mixin.Spec.ComponentRefs)
			merged.Spec.Constraints = mergeConstraints(merged.Spec.Constraints, mixin.Spec.Constraints)
		}
	}

	// Resolve componentRefs against the registry to fill in chart details
	resolved, err := resolveComponents(merged.Spec.ComponentRefs, registry)
	if err != nil {
		return nil, err
	}

	return &ResolvedRecipe{
		Name:       buildRecipeName(criteria),
		Version:    recipeVersion,
		Criteria:   criteria,
		Components: resolved,
	}, nil
}

// findBestMatch finds the recipe that best matches the given criteria.
// Uses AICR's asymmetric matching: recipe "any" matches any query value,
// but query-specific values require exact match or recipe "any".
// The "base" recipe is excluded from direct matching — it is only used
// as an inherited parent in the inheritance chain.
func findBestMatch(allRecipes map[string]*RecipeMetadata, criteria Criteria) (*RecipeMetadata, error) {
	var bestMatch *RecipeMetadata
	bestScore := -1

	for _, r := range allRecipes {
		// Skip the base recipe — it should only be inherited, not matched directly
		if r.Metadata.Name == "base" {
			continue
		}
		// Skip recipes with no criteria (pure parent/mixin recipes)
		if r.Spec.Criteria.Service == "" && r.Spec.Criteria.Accelerator == "" &&
			r.Spec.Criteria.Intent == "" {
			continue
		}

		score := scoreCriteriaMatch(r.Spec.Criteria, criteria)
		if score > bestScore {
			bestScore = score
			bestMatch = r
		}
	}

	if bestMatch == nil || bestScore < 0 {
		return nil, fmt.Errorf(
			"no matching recipe found for criteria: service=%s, accelerator=%s, intent=%s, os=%s, platform=%s",
			criteria.Service, criteria.Accelerator, criteria.Intent, criteria.OS, criteria.Platform,
		)
	}

	return bestMatch, nil
}

// scoreCriteriaMatch scores how well a recipe's criteria match the query.
// Returns -1 if there is no match. Higher scores are better matches.
// Each exact match scores 2 points, each "any" wildcard scores 1 point.
// At least one exact match on a non-wildcard field is required.
func scoreCriteriaMatch(recipe, query Criteria) int {
	score := 0
	exactMatches := 0

	fields := []struct {
		recipeVal string
		queryVal  string
	}{
		{recipe.Service, query.Service},
		{recipe.Accelerator, query.Accelerator},
		{recipe.Intent, query.Intent},
		{recipe.OS, query.OS},
		{recipe.Platform, query.Platform},
	}

	for _, f := range fields {
		rv := strings.ToLower(f.recipeVal)
		qv := strings.ToLower(f.queryVal)

		if rv == "" || rv == "any" {
			// Recipe wildcard — matches anything
			score += 1
			continue
		}
		if qv == "" || qv == "any" {
			// Query wildcard — matches any recipe value
			score += 1
			continue
		}
		if rv == qv {
			// Exact match
			score += 2
			exactMatches++
			continue
		}
		// Mismatch
		return -1
	}

	// Require at least one exact match to avoid pure-wildcard matches
	if exactMatches == 0 {
		return -1
	}

	return score
}

// resolveInheritanceChain walks the `spec.base` references to build the
// full inheritance chain from root (base) to leaf.
// If a recipe has no explicit base and is not the "base" recipe itself,
// it implicitly inherits from "base".
func resolveInheritanceChain(allRecipes map[string]*RecipeMetadata, leaf *RecipeMetadata) ([]*RecipeMetadata, error) {
	var chain []*RecipeMetadata
	visited := make(map[string]bool)
	current := leaf

	for current != nil {
		if visited[current.Metadata.Name] {
			return nil, fmt.Errorf("cycle detected in recipe inheritance at %q", current.Metadata.Name)
		}
		visited[current.Metadata.Name] = true
		chain = append([]*RecipeMetadata{current}, chain...) // prepend

		if current.Spec.Base != "" {
			parent, ok := allRecipes[current.Spec.Base]
			if !ok {
				break
			}
			current = parent
		} else if current.Metadata.Name != "base" {
			// Implicit inheritance from "base" recipe
			if base, ok := allRecipes["base"]; ok && !visited["base"] {
				current = base
			} else {
				break
			}
		} else {
			break
		}
	}

	return chain, nil
}

// mergeChain merges a chain of recipes from base (index 0) to leaf (last index).
func mergeChain(chain []*RecipeMetadata) *RecipeMetadata {
	if len(chain) == 0 {
		return &RecipeMetadata{}
	}

	result := &RecipeMetadata{
		Kind:       chain[0].Kind,
		APIVersion: chain[0].APIVersion,
		Metadata:   chain[len(chain)-1].Metadata, // Use leaf's metadata
		Spec: RecipeSpec{
			Criteria: chain[len(chain)-1].Spec.Criteria, // Use leaf's criteria
		},
	}

	for _, r := range chain {
		result.Spec.ComponentRefs = mergeComponentRefs(result.Spec.ComponentRefs, r.Spec.ComponentRefs)
		result.Spec.Constraints = mergeConstraints(result.Spec.Constraints, r.Spec.Constraints)

		// Collect mixins from all levels
		for _, m := range r.Spec.Mixins {
			if !contains(result.Spec.Mixins, m) {
				result.Spec.Mixins = append(result.Spec.Mixins, m)
			}
		}
	}

	return result
}

// resolveComponents maps recipe componentRefs to fully resolved components
// using the component registry for chart metadata defaults and loading
// embedded values files.
func resolveComponents(refs []ComponentRef, registry *ComponentRegistry) ([]ResolvedComponent, error) {
	var resolved []ResolvedComponent

	for _, ref := range refs {
		rc := ResolvedComponent{
			Name:            ref.Name,
			Chart:           ref.Chart,
			Repo:            ref.Source,
			Version:         ref.Version,
			Namespace:       ref.Namespace,
			CreateNamespace: true,
			DependsOn:       ref.DependencyRefs,
		}

		// Fill in defaults from registry if available
		if regComp, ok := registry.LookupComponent(ref.Name); ok {
			if regComp.Helm != nil {
				if rc.Chart == "" {
					rc.Chart = regComp.Helm.DefaultChart
				}
				if rc.Repo == "" {
					rc.Repo = regComp.Helm.DefaultRepository
				}
				if rc.Version == "" {
					rc.Version = regComp.Helm.DefaultVersion
				}
				if rc.Namespace == "" {
					rc.Namespace = regComp.Helm.DefaultNamespace
				}
			}
		}

		// Registry stores chart names as "<repo-alias>/<chart>" (e.g.,
		// "jetstack/cert-manager"). The Pulumi Helm provider expects just
		// the chart name when an explicit repository URL is set.
		if idx := strings.Index(rc.Chart, "/"); idx >= 0 && rc.Repo != "" {
			rc.Chart = rc.Chart[idx+1:]
		}

		// Load base values from the embedded values file
		var baseValues map[string]interface{}
		if ref.ValuesFile != "" {
			data, err := recipes.FS.ReadFile(ref.ValuesFile)
			if err != nil {
				return nil, fmt.Errorf("reading values file %s for component %s: %w", ref.ValuesFile, ref.Name, err)
			}
			var vals map[string]interface{}
			if yamlErr := yaml.Unmarshal(data, &vals); yamlErr != nil {
				return nil, fmt.Errorf("parsing values file %s for component %s: %w", ref.ValuesFile, ref.Name, yamlErr)
			}
			baseValues = vals
		}

		// Merge: base values → recipe overrides (overrides win)
		if baseValues != nil {
			rc.Values = DeepMergeMaps(baseValues, ref.Overrides)
		} else if ref.Overrides != nil {
			rc.Values = ref.Overrides
		} else {
			rc.Values = make(map[string]interface{})
		}

		// Default namespace to the component name if still empty
		if rc.Namespace == "" {
			rc.Namespace = ref.Name
		}

		// Skip manifest-only components that have no Helm chart.
		// AICR uses these for raw Kubernetes manifests (skyhook-customizations,
		// gke-nccl-tcpxo); deploying as Helm would fail.
		if rc.Chart == "" || rc.Repo == "" {
			continue
		}

		resolved = append(resolved, rc)
	}

	return resolved, nil
}

// ApplyOverrides applies user-specified overrides and skip list to a resolved recipe.
func ApplyOverrides(recipe *ResolvedRecipe, overrides map[string]ComponentOverride, skipComponents []string) *ResolvedRecipe {
	skipSet := make(map[string]bool, len(skipComponents))
	for _, s := range skipComponents {
		skipSet[s] = true
	}

	var filtered []ResolvedComponent
	for _, comp := range recipe.Components {
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

	result := *recipe
	result.Components = filtered
	return &result
}

// buildRecipeName constructs a human-readable recipe name from criteria.
func buildRecipeName(c Criteria) string {
	parts := []string{c.Accelerator, c.Service, c.OS, c.Intent}
	if c.Platform != "" {
		parts = append(parts, c.Platform)
	}
	return strings.Join(parts, "-")
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
