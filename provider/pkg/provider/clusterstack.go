package provider

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv3 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/recipe"
)

// Compile-time interface checks: these types contribute schema metadata via
// infer.Annotated. Verifying at compile time avoids silent drift.
var (
	_ infer.Annotated = (*ClusterStackArgs)(nil)
	_ infer.Annotated = (*ClusterStack)(nil)
	_ infer.Annotated = (*ComponentOverride)(nil)
)

// ClusterStackArgs defines the inputs for the ClusterStack component.
type ClusterStackArgs struct {
	// The GPU accelerator type. Required.
	// Supported values: "h100", "gb200", "b200".
	Accelerator string `pulumi:"accelerator"`

	// The Kubernetes service. Required.
	// Supported values: "aks", "eks", "gke", "kind", "oke".
	// Use "kind" for local hardware-free development of the deployment pipeline.
	Service string `pulumi:"service"`

	// The workload intent. Required.
	// Supported values: "training", "inference".
	Intent string `pulumi:"intent"`

	// The operating system. Optional, defaults to "ubuntu".
	// Supported values: "ubuntu", "cos" (cos applies to gke).
	OS *string `pulumi:"os,optional"`

	// The ML platform/framework. Optional.
	// Supported values: "kubeflow" (training), "dynamo" (inference), "nim" (inference).
	Platform *string `pulumi:"platform,optional"`

	// The kubeconfig contents for the target Kubernetes cluster.
	// Accepts computed outputs from cluster resources (e.g., EKS cluster kubeconfig).
	// If neither kubeconfig nor kubeconfigPath is set, the ambient kubeconfig is used.
	Kubeconfig pulumi.StringPtrInput `pulumi:"kubeconfig,optional"`

	// Path to a kubeconfig file on disk. Mutually exclusive with kubeconfig.
	KubeconfigPath *string `pulumi:"kubeconfigPath,optional"`

	// The kubeconfig context to use. Optional.
	Context *string `pulumi:"context,optional"`

	// Per-component overrides. Map of component name to override configuration.
	// Use this to customize Helm values, versions, or namespaces for specific components.
	ComponentOverrides map[string]ComponentOverride `pulumi:"componentOverrides,optional"`

	// List of component names to exclude from deployment.
	SkipComponents []string `pulumi:"skipComponents,optional"`

	// Whether to skip waiting for Helm releases to become ready. Default: false.
	SkipAwait *bool `pulumi:"skipAwait,optional"`
}

// ComponentOverride allows customizing individual AICR components.
type ComponentOverride struct {
	// Override the Helm chart version.
	Version *string `pulumi:"version,optional"`
	// Override the target namespace.
	Namespace *string `pulumi:"namespace,optional"`
	// Additional or override Helm values (deep-merged with recipe defaults).
	Values map[string]interface{} `pulumi:"values,optional"`
}

// ClusterStack is the output state of the ClusterStack component.
type ClusterStack struct {
	pulumi.ResourceState

	// The resolved AICR recipe name.
	RecipeName pulumi.StringOutput `pulumi:"recipeName"`
	// The AICR recipe version used.
	RecipeVersion pulumi.StringOutput `pulumi:"recipeVersion"`
	// The names of all deployed components.
	DeployedComponents pulumi.StringArrayOutput `pulumi:"deployedComponents"`
	// The number of deployed components.
	ComponentCount pulumi.IntOutput `pulumi:"componentCount"`
}

// Annotate populates the Pulumi schema with descriptions, defaults, and
// supported value lists for each input/output property. These annotations
// surface in the Pulumi Registry resource page and in language-SDK docs.
func (a *ClusterStackArgs) Annotate(an infer.Annotator) {
	an.Describe(&a.Accelerator, `GPU accelerator type. Selects the AICR recipe family.

Supported values: "h100", "gb200", "b200".`)
	an.Describe(&a.Service, `Kubernetes service. Selects cloud-specific operators and storage drivers.

Supported values: "aks", "eks", "gke", "kind", "oke". Use "kind" for local
hardware-free development of the deployment pipeline.`)
	an.Describe(&a.Intent, `Workload intent. Selects between training-oriented and inference-oriented
component sets.

Supported values: "training", "inference".`)
	an.Describe(&a.OS, `Operating system flavor.

Supported values: "ubuntu" (default), "cos" (Container-Optimized OS, GKE only).`)
	an.SetDefault(&a.OS, "ubuntu")
	an.Describe(&a.Platform, `ML platform/framework to layer on top of the base recipe.

Supported values: "kubeflow" (training), "dynamo" (inference), "nim" (inference, EKS+H100 only).
Leave unset for a base recipe with no platform components.`)
	an.Describe(&a.Kubeconfig, `Kubeconfig contents (or path to a kubeconfig file) for the target cluster.
Accepts computed outputs from cluster resources (e.g., an EKS cluster's
KubeconfigJson). Mutually exclusive with `+"`kubeconfigPath`"+`.

If neither `+"`kubeconfig`"+` nor `+"`kubeconfigPath`"+` is set, the ambient kubeconfig
(KUBECONFIG env var or ~/.kube/config) is used.`)
	an.Describe(&a.KubeconfigPath, `Path to a kubeconfig file on disk. Mutually exclusive with `+"`kubeconfig`"+`.
Prefer `+"`kubeconfig`"+` when chaining off a cluster resource's output.`)
	an.Describe(&a.Context, `Kubeconfig context to select. Defaults to the current-context in the kubeconfig.`)
	an.Describe(&a.ComponentOverrides, `Per-component overrides. Map of AICR component name to override settings
(version, namespace, Helm values). Values are deep-merged with the recipe
defaults; only the keys you specify are changed.`)
	an.Describe(&a.SkipComponents, `Component names to exclude from the deployment. Useful for swapping in your
own installation of a component (e.g., bring-your-own cert-manager) or for
deploying onto bare-metal where cloud-specific operators are not relevant.`)
	an.Describe(&a.SkipAwait, `If true, do not wait for each Helm release to become ready before continuing.
Faster previews/updates at the cost of losing readiness signal. Default: false.`)
	an.SetDefault(&a.SkipAwait, false)
}

// Annotate populates schema metadata for ComponentOverride fields.
func (c *ComponentOverride) Annotate(an infer.Annotator) {
	an.Describe(c, `Per-component override settings. Each field is optional; only the fields
you set are applied on top of the recipe defaults.`)
	an.Describe(&c.Version, `Override the Helm chart version. If unset, the recipe-pinned version is used.`)
	an.Describe(&c.Namespace, `Override the target Kubernetes namespace.`)
	an.Describe(&c.Values, `Additional or override Helm values, deep-merged with the recipe defaults.`)
}

// Annotate populates schema metadata for the ClusterStack output state.
func (s *ClusterStack) Annotate(an infer.Annotator) {
	an.Describe(&s.RecipeName, `The resolved AICR recipe name (e.g., "h100-eks-ubuntu-training-kubeflow").`)
	an.Describe(&s.RecipeVersion, `The AICR recipe data version embedded in this provider build.`)
	an.Describe(&s.DeployedComponents, `Names of all components deployed as part of this stack, in topological order.`)
	an.Describe(&s.ComponentCount, `Number of components deployed.`)
}

// NewClusterStack creates a new NVIDIA AICR ClusterStack component.
// It resolves the AICR recipe from the given criteria and deploys each component
// as a Helm release on the target Kubernetes cluster.
func NewClusterStack(ctx *pulumi.Context, name string, args *ClusterStackArgs, opts ...pulumi.ResourceOption) (*ClusterStack, error) {
	if args == nil {
		return nil, fmt.Errorf("ClusterStackArgs is required")
	}
	if err := validateArgs(args); err != nil {
		return nil, err
	}

	state := &ClusterStack{}
	err := ctx.RegisterComponentResource("nvidia-aicr:index:ClusterStack", name, state, opts...)
	if err != nil {
		return nil, err
	}

	// Build recipe criteria from inputs
	criteria := recipe.Criteria{
		Service:     args.Service,
		Accelerator: args.Accelerator,
		Intent:      args.Intent,
		OS:          derefStr(args.OS, "ubuntu"),
		Platform:    derefStr(args.Platform, ""),
	}

	// Resolve the AICR recipe
	resolved, err := recipe.Resolve(criteria)
	if err != nil {
		return nil, fmt.Errorf("resolving AICR recipe: %w", err)
	}

	// Apply user overrides
	if args.ComponentOverrides != nil || len(args.SkipComponents) > 0 {
		overrides := make(map[string]recipe.ComponentOverride)
		for k, v := range args.ComponentOverrides {
			overrides[k] = recipe.ComponentOverride{
				Version:   v.Version,
				Namespace: v.Namespace,
				Values:    v.Values,
			}
		}
		resolved = recipe.ApplyOverrides(resolved, overrides, args.SkipComponents)
	}

	// Topologically sort components by dependencies
	sorted, err := recipe.TopologicalSort(resolved.Components)
	if err != nil {
		return nil, fmt.Errorf("sorting components: %w", err)
	}

	// Create a Kubernetes provider for the target cluster
	var k8sProvider *kubernetes.Provider
	providerArgs := &kubernetes.ProviderArgs{}

	if args.Kubeconfig != nil {
		providerArgs.Kubeconfig = args.Kubeconfig.ToStringPtrOutput().Elem()
	} else if args.KubeconfigPath != nil {
		// The Pulumi Kubernetes provider's Kubeconfig field accepts either
		// kubeconfig contents or a path on disk; passing the path verbatim
		// is supported.
		providerArgs.Kubeconfig = pulumi.String(*args.KubeconfigPath)
	}
	if args.Context != nil {
		providerArgs.Context = pulumi.StringPtr(*args.Context)
	}

	k8sProvider, err = kubernetes.NewProvider(ctx, name+"-k8s-provider", providerArgs, pulumi.Parent(state))
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes provider: %w", err)
	}

	// Deploy each component as a Helm release
	skipAwait := derefBool(args.SkipAwait, false)
	deployedNames := make([]string, 0, len(sorted))
	releases := make(map[string]*helmv3.Release, len(sorted))

	for _, comp := range sorted {
		releaseOpts := []pulumi.ResourceOption{
			pulumi.Parent(state),
			pulumi.Provider(k8sProvider),
		}

		// Add dependency on prerequisite components
		var deps []pulumi.Resource
		for _, depName := range comp.DependsOn {
			if rel, ok := releases[depName]; ok {
				deps = append(deps, rel)
			}
		}
		if len(deps) > 0 {
			releaseOpts = append(releaseOpts, pulumi.DependsOn(deps))
		}

		// Create namespace if needed
		if comp.CreateNamespace {
			ns, nsErr := corev1.NewNamespace(ctx, name+"-ns-"+comp.Name, &corev1.NamespaceArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Name: pulumi.String(comp.Namespace),
				},
			}, pulumi.Parent(state), pulumi.Provider(k8sProvider))
			if nsErr != nil {
				return nil, fmt.Errorf("creating namespace for %s: %w", comp.Name, nsErr)
			}
			releaseOpts = append(releaseOpts, pulumi.DependsOn([]pulumi.Resource{ns}))
		}

		// Build Helm values
		values := toPulumiMap(comp.Values)

		// Resolve chart name + repo, handling OCI vs. HTTP Helm registries.
		// For OCI, the Pulumi Helm provider expects the full OCI URL as the
		// chart name with no separate repository option.
		chart := comp.Chart
		repo := comp.Repo
		if strings.HasPrefix(repo, "oci://") {
			if !strings.HasSuffix(repo, "/"+chart) {
				chart = repo + "/" + chart
			} else {
				chart = repo
			}
			repo = ""
		}

		// Create the Helm release
		releaseArgs := &helmv3.ReleaseArgs{
			Chart:           pulumi.String(chart),
			Version:         pulumi.StringPtr(comp.Version),
			Namespace:       pulumi.StringPtr(comp.Namespace),
			CreateNamespace: pulumi.Bool(true), // Fallback in case explicit ns creation fails
			Values:          values,
			SkipAwait:       pulumi.Bool(skipAwait),
		}
		if repo != "" {
			releaseArgs.RepositoryOpts = helmv3.RepositoryOptsArgs{
				Repo: pulumi.StringPtr(repo),
			}
		}

		release, relErr := helmv3.NewRelease(ctx, name+"-"+comp.Name, releaseArgs, releaseOpts...)
		if relErr != nil {
			return nil, fmt.Errorf("creating Helm release for %s: %w", comp.Name, relErr)
		}

		releases[comp.Name] = release
		deployedNames = append(deployedNames, comp.Name)
	}

	// Set outputs
	state.RecipeName = pulumi.String(resolved.Name).ToStringOutput()
	state.RecipeVersion = pulumi.String(resolved.Version).ToStringOutput()
	state.DeployedComponents = pulumi.ToStringArray(deployedNames).ToStringArrayOutput()
	state.ComponentCount = pulumi.Int(len(deployedNames)).ToIntOutput()

	if err := ctx.RegisterResourceOutputs(state, pulumi.Map{
		"recipeName":         pulumi.String(resolved.Name),
		"recipeVersion":      pulumi.String(resolved.Version),
		"deployedComponents": pulumi.ToStringArray(deployedNames),
		"componentCount":     pulumi.Int(len(deployedNames)),
	}); err != nil {
		return nil, err
	}

	return state, nil
}

// toPulumiMap converts a map[string]interface{} to pulumi.Map for Helm values.
func toPulumiMap(m map[string]interface{}) pulumi.Map {
	if m == nil {
		return nil
	}
	result := make(pulumi.Map, len(m))
	for k, v := range m {
		result[k] = toPulumiInput(v)
	}
	return result
}

// toPulumiInput converts an arbitrary value to a pulumi.Input.
func toPulumiInput(v interface{}) pulumi.Input {
	switch val := v.(type) {
	case map[string]interface{}:
		return toPulumiMap(val)
	case map[interface{}]interface{}:
		m := make(map[string]interface{}, len(val))
		for k, v := range val {
			m[fmt.Sprintf("%v", k)] = v
		}
		return toPulumiMap(m)
	case []interface{}:
		arr := make(pulumi.Array, len(val))
		for i, item := range val {
			arr[i] = toPulumiInput(item)
		}
		return arr
	case string:
		return pulumi.String(val)
	case int:
		return pulumi.Int(val)
	case int64:
		return pulumi.Int(int(val))
	case float64:
		return pulumi.Float64(val)
	case bool:
		return pulumi.Bool(val)
	default:
		return pulumi.String(fmt.Sprintf("%v", v))
	}
}

// validateArgs rejects invalid input combinations early with a clear error,
// rather than letting them surface as cryptic resolver or k8s-provider failures.
func validateArgs(args *ClusterStackArgs) error {
	if strings.TrimSpace(args.Accelerator) == "" {
		return fmt.Errorf("accelerator is required (one of: h100, gb200, b200)")
	}
	if strings.TrimSpace(args.Service) == "" {
		return fmt.Errorf("service is required (one of: aks, eks, gke, kind, oke)")
	}
	if strings.TrimSpace(args.Intent) == "" {
		return fmt.Errorf("intent is required (one of: training, inference)")
	}
	if args.Kubeconfig != nil && args.KubeconfigPath != nil {
		return fmt.Errorf("kubeconfig and kubeconfigPath are mutually exclusive; set only one")
	}
	return nil
}

func derefStr(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

func derefBool(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}
