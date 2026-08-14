package provider

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv3 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	yamlv2 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/yaml/v2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/aicr"
)

// builtinNamespaces are Kubernetes built-in namespaces that always exist and
// must not be created (or duplicated) by this provider.
var builtinNamespaces = map[string]bool{
	"default":         true,
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
}

// supported{Accelerators,Services,Intents,OSes,Platforms} encode the public
// contract for ClusterStack inputs. Mismatches are rejected up-front in
// validateArgs so users get a clear error rather than relying on the
// resolver's wildcard-match semantics to surface the problem.
var (
	supportedAccelerators = []string{"h100", "gb200", "b200"}
	supportedServices     = []string{"aks", "eks", "gke", "kind", "oke"}
	supportedIntents      = []string{"training", "inference"}
	// supportedOSes lists only OS values with backing recipes in the pinned
	// SDK's data (the SDK's request schema names more — rhel, amazonlinux,
	// talos — but v0.18.0 ships no leaves for them, and admitting them here
	// would trade this allowlist's friendly errors for SDK resolution
	// errors). Extend alongside SDK bumps; deriving this from the SDK's
	// CriteriaRegistry is tracked as a follow-up.
	supportedOSes      = []string{"ubuntu", "cos", "ol"}
	supportedPlatforms = []string{"kubeflow", "dynamo", "nim"}
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

	// The operating system. Optional; leave unset for OS-agnostic resolution.
	// Supported values: "ubuntu", "cos", "ol".
	OS *string `pulumi:"os,optional"`

	// The ML platform/framework. Optional.
	// Supported values: "kubeflow" (training), "dynamo" (inference), "nim" (inference).
	Platform *string `pulumi:"platform,optional"`

	// The worker-node count hint used to size the recipe. Optional.
	Nodes *int `pulumi:"nodes,optional"`

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
	// The canonicalized recipe criteria used for resolution. Wire this into
	// a ValidationRun's `criteria` input to validate exactly this stack.
	Criteria RecipeCriteria `pulumi:"criteria"`
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
	an.Describe(&a.OS, `Operating system flavor of the worker nodes.

Supported values: "ubuntu", "cos" (Container-Optimized OS, GKE only), "ol"
(Oracle Linux, OKE) — the values with backing recipes in this provider's
pinned AICR data. Additional OS values (rhel, amazonlinux, talos) arrive
through AICR SDK upgrades.

Leave unset for OS-agnostic resolution: OS-pinned recipe overlays (kernel
tuning, driver constraints) are skipped and the OS-agnostic recipe is used.
Set it when the cluster's OS is known. Some combinations require an OS
(e.g. gke requires "cos"; eks platform recipes require "ubuntu") and fail
with a message listing the valid values; kind recipes require it unset.`)
	an.Describe(&a.Platform, `ML platform/framework to layer on top of the base recipe.

Supported values: "kubeflow" (training), "dynamo" (inference), "nim" (inference, EKS+H100 only).

Leave unset for the base recipe without a platform-specific runtime. Note
that intent="inference" always includes an inference gateway (part of the
base inference stack); choosing a platform layers a runtime ("dynamo",
"nim") on top. intent="training" leaves training-runtime components out
entirely when platform is unset.`)
	an.Describe(&a.Nodes, `Worker-node count hint used to size the recipe (number of nodes, not GPUs).
Leave unset to let AICR pick the default-sized recipe.`)
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
	an.Describe(&c.Values, `Additional or override Helm values, deep-merged on top of the
recipe-resolved values.

Merge semantics: nested maps merge recursively; scalars and arrays replace
the recipe's value; setting a key to null removes it from the
recipe-resolved values, restoring the chart's own default for that key.
Note the null asymmetry: a null *in the recipe data* is passed through to
Helm (explicitly clearing the chart default), while a null *here* removes
the recipe's setting. There is currently no way to pass a literal null
through to Helm from this input — and some language SDKs drop null map
entries during serialization before they reach the provider at all.`)
}

// Annotate populates schema metadata for the ClusterStack output state.
func (s *ClusterStack) Annotate(an infer.Annotator) {
	an.Describe(&s.RecipeName, `The resolved AICR recipe name (e.g., "h100-eks-ubuntu-training-kubeflow").`)
	an.Describe(&s.RecipeVersion, `The AICR recipe data version embedded in this provider build.`)
	an.Describe(&s.DeployedComponents, `Names of all components deployed as part of this stack, in topological order.`)
	an.Describe(&s.ComponentCount, `Number of components deployed.`)
	an.Describe(&s.Criteria, `The canonicalized recipe criteria this stack resolved with. Wire it into a
ValidationRun's `+"`criteria`"+` input so deployment and validation share a single
source of truth.`)
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

	// Build recipe criteria from inputs. Canonicalize (trim + lower) to
	// match the case-insensitive validation we just performed; otherwise
	// inputs like " EKS " would pass validateArgs but fail resolution, and
	// uppercase values would leak into the resolved recipe name.
	criteria := aicr.Criteria{
		Service:     canonical(args.Service),
		Accelerator: canonical(args.Accelerator),
		Intent:      canonical(args.Intent),
		OS:          canonicalOr(args.OS, ""),
		Platform:    canonicalOr(args.Platform, ""),
	}
	if args.Nodes != nil {
		criteria.Nodes = int32(*args.Nodes)
	}

	// Resolve and bundle the AICR recipe via the SDK. Resolution is
	// synchronous, offline (embedded recipe data), and happens at plan time.
	// The pulumi.Context is not a context.Context, so use Background.
	resolved, err := aicr.Resolve(context.Background(), criteria)
	if err != nil {
		return nil, err
	}

	// Apply user overrides
	if args.ComponentOverrides != nil || len(args.SkipComponents) > 0 {
		overrides := make(map[string]aicr.ComponentOverride)
		for k, v := range args.ComponentOverrides {
			overrides[k] = aicr.ComponentOverride{
				Version:   v.Version,
				Namespace: v.Namespace,
				Values:    v.Values,
			}
		}
		resolved = aicr.ApplyOverrides(resolved, overrides, args.SkipComponents)
	}

	// Components are already in the SDK's topologically sorted deployment
	// order (RecipeResult.DeploymentOrder).
	sorted := resolved.Components

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

	// Deploy each component. Most are Helm releases; some are raw manifest
	// bundles (skyhook-customizations, gke-nccl-tcpxo, gpu-operator's
	// dcgm-exporter sidecar); a few are both. We track every Pulumi resource
	// produced for a component so that downstream components depending on it
	// wait for the full set, not just one piece.
	skipAwait := derefBool(args.SkipAwait, false)
	deployedNames := make([]string, 0, len(sorted))
	deployedResources := make(map[string][]pulumi.Resource, len(sorted))

	// Pre-create a single Namespace per unique non-built-in target namespace.
	// AICR recipes routinely point multiple components at the same namespace
	// (e.g. monitoring is shared by kube-prometheus-stack and prometheus-
	// adapter); creating a Namespace per component would either duplicate
	// the resource or fail at apply time.
	namespaceResources := make(map[string]*corev1.Namespace)
	for _, comp := range sorted {
		ns := comp.Namespace
		if ns == "" || builtinNamespaces[ns] {
			continue
		}
		if _, ok := namespaceResources[ns]; ok {
			continue
		}
		nsRes, nsErr := corev1.NewNamespace(ctx, name+"-ns-"+ns, &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(ns),
			},
		}, pulumi.Parent(state), pulumi.Provider(k8sProvider))
		if nsErr != nil {
			return nil, fmt.Errorf("creating namespace %q: %w", ns, nsErr)
		}
		namespaceResources[ns] = nsRes
	}

	for _, comp := range sorted {
		baseOpts := []pulumi.ResourceOption{
			pulumi.Parent(state),
			pulumi.Provider(k8sProvider),
		}

		var deps []pulumi.Resource
		for _, depName := range comp.DependsOn {
			deps = append(deps, deployedResources[depName]...)
		}
		if nsRes, ok := namespaceResources[comp.Namespace]; ok {
			deps = append(deps, nsRes)
		}
		if len(deps) > 0 {
			baseOpts = append(baseOpts, pulumi.DependsOn(deps))
		}

		hasChart := comp.Chart != "" && comp.Repo != ""

		// Pre-manifests must be applied BEFORE the component's chart (e.g. a
		// privileged Namespace with PSS labels the chart's pods need to land
		// in, or a kernel-module ConfigMap the driver DaemonSet mounts).
		if strings.TrimSpace(comp.PreManifests) != "" {
			yamlDoc, preErr := renderManifestBundle(comp, comp.PreManifests)
			if preErr != nil {
				return nil, fmt.Errorf("rendering pre-manifests for %s: %w", comp.Name, preErr)
			}
			if strings.TrimSpace(yamlDoc) != "" {
				cg, cgErr := yamlv2.NewConfigGroup(ctx, name+"-"+comp.Name+"-pre-manifests",
					&yamlv2.ConfigGroupArgs{
						Yaml:      pulumi.StringPtr(yamlDoc),
						SkipAwait: pulumi.BoolPtr(skipAwait),
					}, baseOpts...)
				if cgErr != nil {
					return nil, fmt.Errorf("applying pre-manifests for %s: %w", comp.Name, cgErr)
				}
				deployedResources[comp.Name] = append(deployedResources[comp.Name], cg)
			}
		}

		if hasChart {
			// Deliver values as a YAML asset rather than a typed map: the
			// Pulumi Go SDK strips null map entries during input marshaling,
			// but explicit nulls are semantic in Helm (setting a key to null
			// deletes the chart's default — e.g. the AICR eks overlay clears
			// nvidia-dra-driver-gpu's controller.affinity.nodeAffinity that
			// way). YAML text preserves them, and AllowNullValues makes the
			// Helm provider honor them. sigs.k8s.io/yaml marshals map keys
			// in sorted order, keeping the asset text (and thus diffs)
			// deterministic across previews.
			var valueFiles pulumi.AssetOrArchiveArray
			if len(comp.Values) > 0 {
				valuesYAML, yErr := sigsyaml.Marshal(comp.Values)
				if yErr != nil {
					return nil, fmt.Errorf("encoding Helm values for %s: %w", comp.Name, yErr)
				}
				valueFiles = pulumi.AssetOrArchiveArray{
					pulumi.NewStringAsset(string(valuesYAML)),
				}
			}

			// Resolve chart name + repo, handling OCI vs. HTTP Helm registries.
			// For OCI, the Pulumi Helm provider expects the full OCI URL as the
			// chart name with no separate repository option. The AICR contract
			// is that Source is the OCI namespace and Chart is the chart within
			// it — the full reference is always Source + "/" + Chart, even when
			// the namespace ends with the chart name (kai-scheduler lives at
			// oci://ghcr.io/kai-scheduler/kai-scheduler/kai-scheduler); see
			// NVIDIA/aicr#1954.
			chart := comp.Chart
			repo := comp.Repo
			if strings.HasPrefix(repo, "oci://") {
				chart = repo + "/" + chart
				repo = ""
			}

			// CreateNamespace=true is left as a safety net for the rare case
			// where the user-provided kubeconfig can install Helm releases
			// but lacks RBAC to create Namespaces directly via the K8s
			// provider. Helm's create-namespace is idempotent.
			releaseArgs := &helmv3.ReleaseArgs{
				// Deterministic physical release name (#18): Pulumi
				// auto-naming appends a per-deployment random suffix, so a
				// destroy -> up could never adopt resources Helm keeps on
				// uninstall (cert-manager CRDs, kai-scheduler Queues, ...)
				// — their meta.helm.sh/release-name annotation named a
				// release that no longer existed. The component name is
				// stable across deployments (and matches the aicr CLI's own
				// installs). Coexistence of two ClusterStacks on one
				// cluster was never possible anyway: components install
				// into fixed namespaces.
				Name:            pulumi.StringPtr(comp.Name),
				Chart:           pulumi.String(chart),
				Version:         pulumi.StringPtr(comp.Version),
				Namespace:       pulumi.StringPtr(comp.Namespace),
				CreateNamespace: pulumi.Bool(true),
				ValueYamlFiles:  valueFiles,
				AllowNullValues: pulumi.BoolPtr(true),
				SkipAwait:       pulumi.Bool(skipAwait),
			}
			if repo != "" {
				releaseArgs.RepositoryOpts = helmv3.RepositoryOptsArgs{
					Repo: pulumi.StringPtr(repo),
				}
			}

			releaseOpts := append([]pulumi.ResourceOption(nil), baseOpts...)
			// With a fixed physical name, a replacement must uninstall the
			// old release before installing the new one — create-before-
			// delete would collide on the Helm release name.
			releaseOpts = append(releaseOpts, pulumi.DeleteBeforeReplace(true))
			// Sequence the release after this component's pre-manifests.
			if existing := deployedResources[comp.Name]; len(existing) > 0 {
				releaseOpts = append(releaseOpts, pulumi.DependsOn(existing))
			}

			release, relErr := helmv3.NewRelease(ctx, name+"-"+comp.Name, releaseArgs, releaseOpts...)
			if relErr != nil {
				return nil, fmt.Errorf("creating Helm release for %s: %w", comp.Name, relErr)
			}
			deployedResources[comp.Name] = append(deployedResources[comp.Name], release)
		}

		manifestRendered := false
		if strings.TrimSpace(comp.Manifests) != "" {
			yamlDoc, mfErr := renderManifestBundle(comp, comp.Manifests)
			if mfErr != nil {
				return nil, fmt.Errorf("rendering manifests for %s: %w", comp.Name, mfErr)
			}

			// The bundle may render to nothing if every template is
			// guarded off by its `enabled` flag (e.g. skyhook-customizations
			// with enabled: false). Treat that as a deliberate no-op —
			// the user disabled the bundle's contents through values.
			if strings.TrimSpace(yamlDoc) != "" {
				manifestOpts := append([]pulumi.ResourceOption(nil), baseOpts...)
				// If this component has a Helm release, sequence the manifests
				// after it so any CRDs/operators it ships are ready first.
				if existing := deployedResources[comp.Name]; len(existing) > 0 {
					manifestOpts = append(manifestOpts, pulumi.DependsOn(existing))
				}

				cg, cgErr := yamlv2.NewConfigGroup(ctx, name+"-"+comp.Name+"-manifests",
					&yamlv2.ConfigGroupArgs{
						Yaml:      pulumi.StringPtr(yamlDoc),
						SkipAwait: pulumi.BoolPtr(skipAwait),
					}, manifestOpts...)
				if cgErr != nil {
					return nil, fmt.Errorf("applying manifests for %s: %w", comp.Name, cgErr)
				}
				deployedResources[comp.Name] = append(deployedResources[comp.Name], cg)
			}
			manifestRendered = true
		}

		if len(deployedResources[comp.Name]) == 0 {
			// Components whose manifests rendered to nothing are a
			// deliberate no-op (e.g. a bundle disabled through values);
			// skip silently. A truly empty component (no chart and no
			// manifest content at all) indicates a recipe/adapter bug —
			// surface it rather than dropping the component quietly.
			if !manifestRendered && strings.TrimSpace(comp.PreManifests) == "" {
				return nil, fmt.Errorf("component %q has no chart and no manifests", comp.Name)
			}
			continue
		}
		deployedNames = append(deployedNames, comp.Name)
	}

	// Set outputs
	state.RecipeName = pulumi.String(resolved.Name).ToStringOutput()
	state.RecipeVersion = pulumi.String(resolved.Version).ToStringOutput()
	state.DeployedComponents = pulumi.ToStringArray(deployedNames).ToStringArrayOutput()
	state.ComponentCount = pulumi.Int(len(deployedNames)).ToIntOutput()
	state.Criteria = criteriaOutput(criteria, args.Nodes)

	// Mirror the criteria into the registered outputs map, omitting unset
	// optionals (matching the RecipeCriteria pointer fields).
	criteriaMap := pulumi.Map{
		"accelerator": pulumi.String(criteria.Accelerator),
		"service":     pulumi.String(criteria.Service),
		"intent":      pulumi.String(criteria.Intent),
	}
	if criteria.OS != "" {
		criteriaMap["os"] = pulumi.String(criteria.OS)
	}
	if criteria.Platform != "" {
		criteriaMap["platform"] = pulumi.String(criteria.Platform)
	}
	if args.Nodes != nil {
		criteriaMap["nodes"] = pulumi.Int(*args.Nodes)
	}

	if err := ctx.RegisterResourceOutputs(state, pulumi.Map{
		"recipeName":         pulumi.String(resolved.Name),
		"recipeVersion":      pulumi.String(resolved.Version),
		"deployedComponents": pulumi.ToStringArray(deployedNames),
		"componentCount":     pulumi.Int(len(deployedNames)),
		"criteria":           criteriaMap,
	}); err != nil {
		return nil, err
	}

	return state, nil
}

// criteriaOutput builds the RecipeCriteria output from the canonicalized
// resolution criteria. Optionals stay nil when unset so wiring the output
// into a ValidationRun re-resolves identical criteria.
func criteriaOutput(criteria aicr.Criteria, nodes *int) RecipeCriteria {
	out := RecipeCriteria{
		Accelerator: criteria.Accelerator,
		Service:     criteria.Service,
		Intent:      criteria.Intent,
	}
	if criteria.OS != "" {
		osVal := criteria.OS
		out.OS = &osVal
	}
	if criteria.Platform != "" {
		platform := criteria.Platform
		out.Platform = &platform
	}
	if nodes != nil {
		n := *nodes
		out.Nodes = &n
	}
	return out
}

// validateArgs rejects invalid input combinations early with a clear error,
// rather than letting them surface as cryptic resolver or k8s-provider
// failures. Required fields must be set, and every dimension is checked
// against the supported allowlist — the resolver treats empty fields as
// wildcards, so an unrecognized accelerator like "fictional-gpu" can match
// generic service overlays without this check.
func validateArgs(args *ClusterStackArgs) error {
	if err := validateCriteria(args.Accelerator, args.Service, args.Intent,
		args.OS, args.Platform, args.Nodes); err != nil {
		return err
	}
	if args.Kubeconfig != nil && args.KubeconfigPath != nil {
		return fmt.Errorf("kubeconfig and kubeconfigPath are mutually exclusive; set only one")
	}
	return nil
}

// validateCriteria checks the recipe-selection dimensions shared by
// ClusterStack and ValidationRun (allowlists, nodes bounds, and the
// published combination matrix), so both resources produce byte-identical
// friendly errors.
func validateCriteria(accelerator, service, intent string, osName, platform *string, nodes *int) error {
	accel := strings.ToLower(strings.TrimSpace(accelerator))
	svc := strings.ToLower(strings.TrimSpace(service))
	intentVal := strings.ToLower(strings.TrimSpace(intent))

	if accel == "" {
		return fmt.Errorf("accelerator is required (one of: %s)", strings.Join(supportedAccelerators, ", "))
	}
	if !contains(supportedAccelerators, accel) {
		return fmt.Errorf("accelerator %q is not supported (must be one of: %s)",
			accelerator, strings.Join(supportedAccelerators, ", "))
	}
	if svc == "" {
		return fmt.Errorf("service is required (one of: %s)", strings.Join(supportedServices, ", "))
	}
	if !contains(supportedServices, svc) {
		return fmt.Errorf("service %q is not supported (must be one of: %s)",
			service, strings.Join(supportedServices, ", "))
	}
	if intentVal == "" {
		return fmt.Errorf("intent is required (one of: %s)", strings.Join(supportedIntents, ", "))
	}
	if !contains(supportedIntents, intentVal) {
		return fmt.Errorf("intent %q is not supported (must be one of: %s)",
			intent, strings.Join(supportedIntents, ", "))
	}
	if osName != nil && *osName != "" {
		osVal := strings.ToLower(strings.TrimSpace(*osName))
		if !contains(supportedOSes, osVal) {
			return fmt.Errorf("os %q is not supported (must be one of: %s)",
				*osName, strings.Join(supportedOSes, ", "))
		}
	}
	if platform != nil && *platform != "" {
		platformVal := strings.ToLower(strings.TrimSpace(*platform))
		if !contains(supportedPlatforms, platformVal) {
			return fmt.Errorf("platform %q is not supported (must be one of: %s)",
				*platform, strings.Join(supportedPlatforms, ", "))
		}
	}
	if nodes != nil && *nodes < 0 {
		return fmt.Errorf("nodes must be non-negative; got %d", *nodes)
	}
	// The SDK's RecipeRequest.Nodes is an int32; bound it here so an
	// oversized value is a friendly error, not a silent integer wrap.
	if nodes != nil && *nodes > math.MaxInt32 {
		return fmt.Errorf("nodes must be at most %d; got %d", math.MaxInt32, *nodes)
	}
	return validateCompatibility(accel, svc, intentVal,
		canonicalOr(osName, ""),
		canonicalOr(platform, ""),
	)
}

// validateCompatibility enforces the README's published combination matrix.
// The resolver also rejects unsupported combinations (no leaf will match)
// but we surface a precise message here so users see "kubeflow is training-
// only" instead of "no matching recipe found".
func validateCompatibility(accelerator, service, intent, osName, platform string) error {
	switch platform {
	case "kubeflow":
		if intent != "training" {
			return fmt.Errorf("platform %q is training-only; got intent %q", platform, intent)
		}
	case "dynamo":
		if intent != "inference" {
			return fmt.Errorf("platform %q is inference-only; got intent %q", platform, intent)
		}
	case "nim":
		if intent != "inference" || service != "eks" || accelerator != "h100" {
			return fmt.Errorf(
				"platform %q is supported only on eks+h100+inference; got service=%q accelerator=%q intent=%q",
				platform, service, accelerator, intent)
		}
	}
	if accelerator == "b200" && intent != "training" {
		return fmt.Errorf("accelerator %q is training-only; got intent %q", accelerator, intent)
	}
	if osName == "cos" && service != "gke" {
		return fmt.Errorf("os %q is only supported on gke; got service %q", osName, service)
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// canonical lower-cases and trims an input criterion so that " EKS " and
// "eks" produce the same recipe.Criteria value.
func canonical(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// canonicalOr is canonical for an optional string, falling back to def
// when the pointer is nil or canonicalizes to the empty string.
func canonicalOr(s *string, def string) string {
	if s == nil {
		return def
	}
	c := canonical(*s)
	if c == "" {
		return def
	}
	return c
}

func derefBool(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}
