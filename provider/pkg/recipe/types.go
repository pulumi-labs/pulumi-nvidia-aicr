package recipe

// Criteria specifies the dimensions used to select an AICR recipe.
type Criteria struct {
	Service     string `yaml:"service" json:"service"`
	Accelerator string `yaml:"accelerator" json:"accelerator"`
	Intent      string `yaml:"intent" json:"intent"`
	OS          string `yaml:"os" json:"os"`
	Platform    string `yaml:"platform" json:"platform"`
}

// RecipeMetadata represents an AICR recipe YAML file (base, overlay, or leaf).
type RecipeMetadata struct {
	Kind       string             `yaml:"kind"`
	APIVersion string             `yaml:"apiVersion"`
	Metadata   RecipeMetadataInfo `yaml:"metadata"`
	Spec       RecipeSpec         `yaml:"spec"`
}

type RecipeMetadataInfo struct {
	Name string `yaml:"name"`
}

type RecipeSpec struct {
	Base          string         `yaml:"base,omitempty"`
	Criteria      Criteria       `yaml:"criteria,omitempty"`
	Mixins        []string       `yaml:"mixins,omitempty"`
	Constraints   []Constraint   `yaml:"constraints,omitempty"`
	ComponentRefs []ComponentRef `yaml:"componentRefs,omitempty"`
}

// Constraint represents a validation constraint from a recipe.
type Constraint struct {
	Name        string `yaml:"name"`
	Value       string `yaml:"value"`
	Severity    string `yaml:"severity,omitempty"`
	Remediation string `yaml:"remediation,omitempty"`
	Unit        string `yaml:"unit,omitempty"`
}

// ComponentRef is a reference to a Helm chart component in a recipe.
type ComponentRef struct {
	Name            string                 `yaml:"name"`
	Namespace       string                 `yaml:"namespace,omitempty"`
	Chart           string                 `yaml:"chart,omitempty"`
	Type            string                 `yaml:"type,omitempty"`
	Source          string                 `yaml:"source,omitempty"`
	Version         string                 `yaml:"version,omitempty"`
	Tag             string                 `yaml:"tag,omitempty"`
	ValuesFile      string                 `yaml:"valuesFile,omitempty"`
	Overrides       map[string]interface{} `yaml:"overrides,omitempty"`
	DependencyRefs  []string               `yaml:"dependencyRefs,omitempty"`
	ManifestFiles   []string               `yaml:"manifestFiles,omitempty"`
	Path            string                 `yaml:"path,omitempty"`
}

// RegistryComponent represents a component definition in registry.yaml.
type RegistryComponent struct {
	Name            string            `yaml:"name"`
	DisplayName     string            `yaml:"displayName,omitempty"`
	Helm            *HelmConfig       `yaml:"helm,omitempty"`
	NodeScheduling  *NodeScheduling   `yaml:"nodeScheduling,omitempty"`
}

type HelmConfig struct {
	DefaultRepository string `yaml:"defaultRepository"`
	DefaultChart      string `yaml:"defaultChart"`
	DefaultVersion    string `yaml:"defaultVersion,omitempty"`
	DefaultNamespace  string `yaml:"defaultNamespace"`
}

type NodeScheduling struct {
	System      *SchedulingConfig `yaml:"system,omitempty"`
	Accelerated *SchedulingConfig `yaml:"accelerated,omitempty"`
}

type SchedulingConfig struct {
	SelectorPath   string `yaml:"selectorPath,omitempty"`
	TolerationPath string `yaml:"tolerationPath,omitempty"`
}

// ComponentRegistry represents the top-level registry.yaml structure.
type ComponentRegistry struct {
	Kind       string              `yaml:"kind"`
	APIVersion string              `yaml:"apiVersion"`
	Components []RegistryComponent `yaml:"components"`
}

// ResolvedRecipe is the output of recipe resolution — a fully resolved set of
// components ready for deployment.
type ResolvedRecipe struct {
	Name       string
	Version    string
	Criteria   Criteria
	Components []ResolvedComponent
}

// ResolvedComponent is a single component ready for Helm deployment.
type ResolvedComponent struct {
	Name            string
	Chart           string
	Repo            string
	Version         string
	Namespace       string
	CreateNamespace bool
	Values          map[string]interface{}
	DependsOn       []string
}

// ComponentOverride allows users to customize individual components.
type ComponentOverride struct {
	Version   *string                `json:"version,omitempty"`
	Namespace *string                `json:"namespace,omitempty"`
	Values    map[string]interface{} `json:"values,omitempty"`
}
