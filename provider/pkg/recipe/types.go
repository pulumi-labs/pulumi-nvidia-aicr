// Package recipe implements the NVIDIA AICR recipe resolution engine.
//
// AICR recipes are validated combinations of Helm-deployed components for
// GPU-accelerated Kubernetes clusters. They are organized as a tree:
//
//   - "base.yaml" defines the cross-cutting components every recipe shares.
//   - Service overlays (eks.yaml, aks.yaml, gke-cos.yaml, oke.yaml, kind.yaml)
//     layer cloud-specific operators on top of base.
//   - Leaf overlays (h100-eks-ubuntu-training-kubeflow.yaml, ...) bind a
//     specific accelerator + service + os + intent + platform combination.
//   - Mixins (platform-kubeflow.yaml, platform-inference.yaml, os-ubuntu.yaml)
//     are reusable component bundles applied by name.
//
// Resolve picks the best-matching leaf for a given Criteria, walks its
// inheritance chain to base, merges component lists and overrides, applies
// mixins, and resolves each component reference against registry.yaml for
// chart metadata defaults.
package recipe

// Criteria specifies the dimensions used to select an AICR recipe.
// All fields are optional individually, but Resolve requires Service,
// Accelerator, and Intent to be set in practice (the empty value matches no
// recipe).
type Criteria struct {
	// Service is the Kubernetes service: "aks", "eks", "gke", "kind", "oke".
	Service string `yaml:"service" json:"service"`
	// Accelerator is the GPU type: "h100", "gb200", "b200".
	Accelerator string `yaml:"accelerator" json:"accelerator"`
	// Intent is the workload intent: "training", "inference".
	Intent string `yaml:"intent" json:"intent"`
	// OS is the operating system flavor: "ubuntu" (default) or "cos".
	OS string `yaml:"os" json:"os"`
	// Platform is the optional ML platform: "kubeflow", "dynamo", "nim".
	Platform string `yaml:"platform" json:"platform"`
}

// RecipeMetadata represents a single AICR recipe YAML file (base, overlay,
// mixin, or leaf). It mirrors the on-disk schema of AICR recipe documents.
type RecipeMetadata struct {
	Kind       string             `yaml:"kind"`
	APIVersion string             `yaml:"apiVersion"`
	Metadata   RecipeMetadataInfo `yaml:"metadata"`
	Spec       RecipeSpec         `yaml:"spec"`
}

// RecipeMetadataInfo carries the recipe identifier used for inheritance and
// mixin lookups.
type RecipeMetadataInfo struct {
	Name string `yaml:"name"`
}

// RecipeSpec is the body of a recipe document. Inheritance is expressed via
// Base; mixins are applied by name; ComponentRefs lists Helm releases to
// deploy.
type RecipeSpec struct {
	Base          string         `yaml:"base,omitempty"`
	Criteria      Criteria       `yaml:"criteria,omitempty"`
	Mixins        []string       `yaml:"mixins,omitempty"`
	Constraints   []Constraint   `yaml:"constraints,omitempty"`
	ComponentRefs []ComponentRef `yaml:"componentRefs,omitempty"`
}

// Constraint represents an AICR validation constraint (e.g. minimum
// Kubernetes version). Constraints are loaded into ResolvedRecipe but the
// provider does not yet enforce them at runtime.
type Constraint struct {
	Name        string `yaml:"name"`
	Value       string `yaml:"value"`
	Severity    string `yaml:"severity,omitempty"`
	Remediation string `yaml:"remediation,omitempty"`
	Unit        string `yaml:"unit,omitempty"`
}

// ComponentRef is a reference to a Helm chart component within a recipe.
// Most fields default in from the component registry (registry.yaml) when not
// set explicitly here.
type ComponentRef struct {
	Name           string                 `yaml:"name"`
	Namespace      string                 `yaml:"namespace,omitempty"`
	Chart          string                 `yaml:"chart,omitempty"`
	Type           string                 `yaml:"type,omitempty"`
	Source         string                 `yaml:"source,omitempty"`
	Version        string                 `yaml:"version,omitempty"`
	Tag            string                 `yaml:"tag,omitempty"`
	ValuesFile     string                 `yaml:"valuesFile,omitempty"`
	Overrides      map[string]interface{} `yaml:"overrides,omitempty"`
	DependencyRefs []string               `yaml:"dependencyRefs,omitempty"`
	ManifestFiles  []string               `yaml:"manifestFiles,omitempty"`
	Path           string                 `yaml:"path,omitempty"`
}

// RegistryComponent describes a component in registry.yaml. It carries the
// default Helm chart coordinates (repository, chart name, version, namespace)
// applied when a ComponentRef leaves them unset.
type RegistryComponent struct {
	Name           string          `yaml:"name"`
	DisplayName    string          `yaml:"displayName,omitempty"`
	Helm           *HelmConfig     `yaml:"helm,omitempty"`
	NodeScheduling *NodeScheduling `yaml:"nodeScheduling,omitempty"`
}

// HelmConfig is the registry's default Helm coordinates for a component.
type HelmConfig struct {
	DefaultRepository string `yaml:"defaultRepository"`
	DefaultChart      string `yaml:"defaultChart"`
	DefaultVersion    string `yaml:"defaultVersion,omitempty"`
	DefaultNamespace  string `yaml:"defaultNamespace"`
}

// NodeScheduling describes selector/toleration paths to apply to system vs.
// accelerated workloads. Currently informational; not enforced at runtime.
type NodeScheduling struct {
	System      *SchedulingConfig `yaml:"system,omitempty"`
	Accelerated *SchedulingConfig `yaml:"accelerated,omitempty"`
}

// SchedulingConfig points at the chart-values paths where node selectors and
// tolerations live for this component.
type SchedulingConfig struct {
	SelectorPath   string `yaml:"selectorPath,omitempty"`
	TolerationPath string `yaml:"tolerationPath,omitempty"`
}

// ComponentRegistry is the top-level structure of registry.yaml.
type ComponentRegistry struct {
	Kind       string              `yaml:"kind"`
	APIVersion string              `yaml:"apiVersion"`
	Components []RegistryComponent `yaml:"components"`
}

// ResolvedRecipe is the output of Resolve: a fully resolved set of
// components ready for deployment, with all inheritance, mixins, and
// registry defaults applied.
type ResolvedRecipe struct {
	// Name is a human-readable identifier built from Criteria
	// (e.g. "h100-eks-ubuntu-training-kubeflow").
	Name string
	// Version is the AICR recipe data version embedded in this provider.
	Version string
	// Criteria is the input that produced this resolution, with defaults
	// (such as OS=ubuntu) filled in.
	Criteria Criteria
	// Components is the deployable component list, in input order.
	// Use TopologicalSort to order it by DependsOn before deploying.
	Components []ResolvedComponent
}

// ResolvedComponent is a single component ready to be deployed. Most
// components are Helm releases; some are raw manifest bundles
// (Chart/Repo empty, ManifestFiles populated); a few are both
// (Helm release plus side-car manifests).
type ResolvedComponent struct {
	Name            string
	Chart           string
	Repo            string
	Version         string
	Namespace       string
	CreateNamespace bool
	Values          map[string]interface{}
	DependsOn       []string
	// ManifestFiles is the list of embedded manifest paths (relative to
	// recipes.FS) that should be applied alongside (or in lieu of) the
	// Helm release for this component.
	ManifestFiles []string
}

// ComponentOverride lets callers customize a single component's chart
// version, target namespace, or Helm values. Values are deep-merged with
// the recipe defaults.
type ComponentOverride struct {
	Version   *string                `json:"version,omitempty"`
	Namespace *string                `json:"namespace,omitempty"`
	Values    map[string]interface{} `json:"values,omitempty"`
}
