package recipe

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/recipes"
)

var (
	recipesOnce sync.Once
	allRecipes  map[string]*RecipeMetadata
	recipesErr  error
)

// loadAllRecipes reads and parses all embedded recipe YAML files.
func loadAllRecipes() (map[string]*RecipeMetadata, error) {
	recipesOnce.Do(func() {
		allRecipes = make(map[string]*RecipeMetadata)

		// Load all overlay files (including base.yaml which is in overlays/)
		if err := fs.WalkDir(recipes.FS, "overlays", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
				return nil
			}
			data, err := recipes.FS.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			var r RecipeMetadata
			if err := yaml.Unmarshal(data, &r); err != nil {
				return fmt.Errorf("parsing %s: %w", path, err)
			}
			if r.Metadata.Name == "" {
				// Derive name from filename
				r.Metadata.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
			}
			allRecipes[r.Metadata.Name] = &r
			return nil
		}); err != nil {
			recipesErr = fmt.Errorf("loading overlays: %w", err)
			return
		}

		// Load all mixin files
		if err := fs.WalkDir(recipes.FS, "mixins", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// mixins dir may not exist
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
				return nil
			}
			data, readErr := recipes.FS.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("reading mixin %s: %w", path, readErr)
			}
			var r RecipeMetadata
			if parseErr := yaml.Unmarshal(data, &r); parseErr != nil {
				return fmt.Errorf("parsing mixin %s: %w", path, parseErr)
			}
			if r.Metadata.Name == "" {
				r.Metadata.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
			}
			allRecipes[r.Metadata.Name] = &r
			return nil
		}); err != nil {
			recipesErr = fmt.Errorf("loading mixins: %w", err)
			return
		}
	})
	return allRecipes, recipesErr
}

// mergeComponentRefs merges overlay componentRefs onto a base set.
// Components with the same name are merged (overlay wins for non-empty fields);
// new components are appended.
func mergeComponentRefs(base, overlay []ComponentRef) []ComponentRef {
	result := make([]ComponentRef, len(base))
	copy(result, base)

	for _, oc := range overlay {
		found := false
		for i, bc := range result {
			if bc.Name == oc.Name {
				result[i] = mergeComponentRef(bc, oc)
				found = true
				break
			}
		}
		if !found {
			result = append(result, oc)
		}
	}
	return result
}

// mergeComponentRef merges a single overlay ComponentRef onto a base.
func mergeComponentRef(base, overlay ComponentRef) ComponentRef {
	result := base
	if overlay.Namespace != "" {
		result.Namespace = overlay.Namespace
	}
	if overlay.Chart != "" {
		result.Chart = overlay.Chart
	}
	if overlay.Type != "" {
		result.Type = overlay.Type
	}
	if overlay.Source != "" {
		result.Source = overlay.Source
	}
	if overlay.Version != "" {
		result.Version = overlay.Version
	}
	if overlay.Tag != "" {
		result.Tag = overlay.Tag
	}
	if overlay.ValuesFile != "" {
		result.ValuesFile = overlay.ValuesFile
	}
	if overlay.Path != "" {
		result.Path = overlay.Path
	}
	if len(overlay.DependencyRefs) > 0 {
		result.DependencyRefs = overlay.DependencyRefs
	}
	if len(overlay.ManifestFiles) > 0 {
		result.ManifestFiles = overlay.ManifestFiles
	}
	if overlay.Overrides != nil {
		result.Overrides = DeepMergeMaps(base.Overrides, overlay.Overrides)
	}
	return result
}

// DeepMergeMaps recursively merges src into dst. Values in src take precedence.
// A nil value in src deletes the key from dst.
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
		// If both are maps, merge recursively
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

// mergeConstraints merges overlay constraints onto base. Overlay constraints
// with the same name override base constraints.
func mergeConstraints(base, overlay []Constraint) []Constraint {
	result := make([]Constraint, len(base))
	copy(result, base)
	for _, oc := range overlay {
		found := false
		for i, bc := range result {
			if bc.Name == oc.Name {
				result[i] = oc
				found = true
				break
			}
		}
		if !found {
			result = append(result, oc)
		}
	}
	return result
}
