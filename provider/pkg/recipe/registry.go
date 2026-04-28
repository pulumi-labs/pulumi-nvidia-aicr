package recipe

import (
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/recipes"
)

var (
	registryOnce sync.Once
	registryData *ComponentRegistry
	registryErr  error
)

// LoadRegistry loads the component registry from embedded data.
func LoadRegistry() (*ComponentRegistry, error) {
	registryOnce.Do(func() {
		data, err := recipes.FS.ReadFile("registry.yaml")
		if err != nil {
			registryErr = fmt.Errorf("failed to read registry.yaml: %w", err)
			return
		}
		var reg ComponentRegistry
		if err := yaml.Unmarshal(data, &reg); err != nil {
			registryErr = fmt.Errorf("failed to parse registry.yaml: %w", err)
			return
		}
		registryData = &reg
	})
	return registryData, registryErr
}

// LookupComponent finds a component in the registry by name.
func (r *ComponentRegistry) LookupComponent(name string) (*RegistryComponent, bool) {
	for i := range r.Components {
		if r.Components[i].Name == name {
			return &r.Components[i], true
		}
	}
	return nil, false
}
