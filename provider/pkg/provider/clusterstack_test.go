package provider

import (
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateArgsRejectsEmptyRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		args ClusterStackArgs
		want string
	}{
		{
			name: "empty accelerator",
			args: ClusterStackArgs{Accelerator: "", Service: "eks", Intent: "training"},
			want: "accelerator is required",
		},
		{
			name: "whitespace accelerator",
			args: ClusterStackArgs{Accelerator: "   ", Service: "eks", Intent: "training"},
			want: "accelerator is required",
		},
		{
			name: "empty service",
			args: ClusterStackArgs{Accelerator: "h100", Service: "", Intent: "training"},
			want: "service is required",
		},
		{
			name: "empty intent",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: ""},
			want: "intent is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(&tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateArgsRejectsBothKubeconfigForms(t *testing.T) {
	path := "/tmp/kubeconfig"
	args := &ClusterStackArgs{
		Accelerator:    "h100",
		Service:        "eks",
		Intent:         "training",
		Kubeconfig:     pulumi.String("contents").ToStringPtrOutput(),
		KubeconfigPath: &path,
	}
	err := validateArgs(args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestValidateArgsAcceptsValidInput(t *testing.T) {
	args := &ClusterStackArgs{
		Accelerator: "h100",
		Service:     "eks",
		Intent:      "training",
	}
	assert.NoError(t, validateArgs(args))
}

func TestValidateArgsRejectsUnsupportedValues(t *testing.T) {
	// validateArgs is the choke point that prevents the resolver's
	// "empty matches anything" wildcard semantics from quietly accepting
	// a typo'd accelerator or an unknown cloud service.
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		args ClusterStackArgs
		want string
	}{
		{
			name: "unsupported accelerator",
			args: ClusterStackArgs{Accelerator: "a100", Service: "eks", Intent: "training"},
			want: `accelerator "a100" is not supported`,
		},
		{
			name: "unsupported service",
			args: ClusterStackArgs{Accelerator: "h100", Service: "rke", Intent: "training"},
			want: `service "rke" is not supported`,
		},
		{
			name: "unsupported intent",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: "serving"},
			want: `intent "serving" is not supported`,
		},
		{
			name: "unsupported os",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: "training", OS: str("flatcar")},
			want: `os "flatcar" is not supported`,
		},
		{
			name: "unsupported platform",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: "training", Platform: str("ray")},
			want: `platform "ray" is not supported`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(&tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateArgsRejectsIncompatibleCombinations(t *testing.T) {
	// Dimensions can each be individually-valid yet form a combination
	// that the AICR recipe matrix does not cover. Catch those up-front.
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		args ClusterStackArgs
		want string
	}{
		{
			name: "kubeflow + inference",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: "inference", Platform: str("kubeflow")},
			want: `platform "kubeflow" is training-only`,
		},
		{
			name: "dynamo + training",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: "training", Platform: str("dynamo")},
			want: `platform "dynamo" is inference-only`,
		},
		{
			name: "nim outside eks+h100+inference",
			args: ClusterStackArgs{Accelerator: "h100", Service: "gke", Intent: "inference", Platform: str("nim")},
			want: `platform "nim" is supported only on eks+h100+inference`,
		},
		{
			name: "b200 + inference",
			args: ClusterStackArgs{Accelerator: "b200", Service: "eks", Intent: "inference"},
			want: `accelerator "b200" is training-only`,
		},
		{
			name: "cos outside gke",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: "training", OS: str("cos")},
			want: `os "cos" is only supported on gke`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArgs(&tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateArgsCanonicalizesWhitespaceAndCase(t *testing.T) {
	args := &ClusterStackArgs{
		Accelerator: "  H100 ",
		Service:     "EKS",
		Intent:      "Training",
	}
	assert.NoError(t, validateArgs(args))
}

func TestValidateArgsAcceptsUnsetOptionalFields(t *testing.T) {
	// Optional fields (OS, Platform) left unset must pass validation —
	// the resolver fills in defaults (OS=ubuntu) or treats them as
	// "no platform" base recipes.
	args := &ClusterStackArgs{
		Accelerator: "h100",
		Service:     "eks",
		Intent:      "training",
	}
	assert.NoError(t, validateArgs(args))
}

// recordingMonitor captures every NewResource call so tests can assert on the
// shape of the registered resource graph.
type recordingMonitor struct {
	mu        sync.Mutex
	resources []resourceRecord
}

type resourceRecord struct {
	typeToken string
	name      string
	inputs    resource.PropertyMap
}

func (m *recordingMonitor) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *recordingMonitor) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	m.resources = append(m.resources, resourceRecord{
		typeToken: args.TypeToken,
		name:      args.Name,
		inputs:    args.Inputs,
	})
	m.mu.Unlock()
	// Echo inputs back as the new state so outputs are populated for downstream resources.
	return args.Name + "-id", args.Inputs, nil
}

func TestNewClusterStackBuildsResourceGraph(t *testing.T) {
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "eks",
			Intent:      "training",
			Platform:    pulumi.StringRef("kubeflow"),
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	var components, providers, namespaces, releases int
	releaseNames := map[string]bool{}
	for _, r := range mon.resources {
		switch {
		case r.typeToken == "nvidia-aicr:index:ClusterStack":
			components++
		case r.typeToken == "pulumi:providers:kubernetes":
			providers++
		case r.typeToken == "kubernetes:core/v1:Namespace":
			namespaces++
		case strings.HasPrefix(r.typeToken, "kubernetes:helm.sh/v3:Release"):
			releases++
			releaseNames[r.name] = true
		}
	}

	assert.Equal(t, 1, components, "expected exactly one ClusterStack component")
	assert.Equal(t, 1, providers, "expected exactly one Kubernetes provider")
	assert.Greater(t, releases, 5, "expected several Helm releases for h100/eks/training/kubeflow")
	assert.Greater(t, namespaces, 0, "expected at least one Namespace resource")

	// Spot-check a few well-known components are present.
	assert.True(t, releaseNames["stack-cert-manager"], "cert-manager release missing; got: %v", releaseNames)
	assert.True(t, releaseNames["stack-gpu-operator"], "gpu-operator release missing")
	assert.True(t, releaseNames["stack-kubeflow-trainer"], "kubeflow-trainer release missing")
}

func TestNewClusterStackDedupesSharedNamespaces(t *testing.T) {
	// kube-prometheus-stack and prometheus-adapter both target the
	// "monitoring" namespace; aws-efa and aws-ebs-csi-driver both target
	// the built-in "kube-system". The provider must create exactly one
	// Namespace resource per unique non-built-in namespace, and zero for
	// Kubernetes built-ins.
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "eks",
			Intent:      "training",
			Platform:    pulumi.StringRef("kubeflow"),
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	nsCounts := map[string]int{}
	for _, r := range mon.resources {
		if r.typeToken != "kubernetes:core/v1:Namespace" {
			continue
		}
		nsName, _ := r.inputs["metadata"].ObjectValue()["name"].V.(string)
		nsCounts[nsName]++
	}

	for nsName, count := range nsCounts {
		assert.Equalf(t, 1, count,
			"namespace %q should be created exactly once, got %d", nsName, count)
		assert.NotContainsf(t, []string{"kube-system", "kube-public", "kube-node-lease", "default"}, nsName,
			"built-in namespace %q must not be created", nsName)
	}
	// Sanity check: at least one application namespace was created.
	assert.NotEmpty(t, nsCounts, "expected at least one application namespace")
}

func TestNewClusterStackDeploysManifestComponents(t *testing.T) {
	// On gke-cos/h100/training the recipe pulls in two manifest-only
	// components (skyhook-customizations, gke-nccl-tcpxo) plus side-car
	// manifests for gpu-operator. Each must surface as a yaml/v2 ConfigGroup
	// resource — the previous "skip if no chart" behavior would drop them.
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		os := "cos"
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "gke",
			Intent:      "training",
			OS:          &os,
			Platform:    pulumi.StringRef("kubeflow"),
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	manifestNames := map[string]bool{}
	for _, r := range mon.resources {
		if r.typeToken == "kubernetes:yaml/v2:ConfigGroup" {
			manifestNames[r.name] = true
		}
	}
	assert.True(t, manifestNames["stack-skyhook-customizations-manifests"],
		"expected skyhook-customizations manifest bundle; got: %v", manifestNames)
	assert.True(t, manifestNames["stack-gke-nccl-tcpxo-manifests"],
		"expected gke-nccl-tcpxo manifest bundle; got: %v", manifestNames)
	assert.True(t, manifestNames["stack-gpu-operator-manifests"],
		"expected gpu-operator side-car manifest bundle; got: %v", manifestNames)
}

func TestNewClusterStackTreatsEmptyManifestRenderAsNoOp(t *testing.T) {
	// A manifest-only component whose templates all render to nothing
	// (e.g. skyhook-customizations with an `enabled: false` override on
	// the gke-cos training recipe) is a deliberate user-driven no-op,
	// not a configuration error. The previous "no chart and no manifests"
	// guard would have failed the entire deploy in that case.
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		os := "cos"
		falseStr := "false"
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "gke",
			Intent:      "training",
			OS:          &os,
			Platform:    pulumi.StringRef("kubeflow"),
			ComponentOverrides: map[string]ComponentOverride{
				"skyhook-customizations": {
					Values: map[string]interface{}{"enabled": falseStr},
				},
			},
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	for _, r := range mon.resources {
		if r.typeToken == "kubernetes:yaml/v2:ConfigGroup" {
			assert.NotEqualf(t, "stack-skyhook-customizations-manifests", r.name,
				"disabled skyhook-customizations bundle must not register a ConfigGroup")
		}
	}
}

func TestNewClusterStackHonorsSkipComponents(t *testing.T) {
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator:    "h100",
			Service:        "eks",
			Intent:         "training",
			Platform:       pulumi.StringRef("kubeflow"),
			SkipComponents: []string{"cert-manager", "kube-prometheus-stack"},
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	for _, r := range mon.resources {
		if strings.HasPrefix(r.typeToken, "kubernetes:helm.sh/v3:Release") {
			assert.NotEqual(t, "stack-cert-manager", r.name, "skipped cert-manager should not be deployed")
			assert.NotEqual(t, "stack-kube-prometheus-stack", r.name, "skipped kube-prometheus-stack should not be deployed")
		}
	}
}

func TestNewClusterStackRejectsUnsupportedCriteria(t *testing.T) {
	mon := &recordingMonitor{}
	// validateArgs rejects out-of-allowlist accelerators before the resolver
	// has a chance to wildcard-match them against generic service overlays.
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "fictional-gpu",
			Service:     "eks",
			Intent:      "training",
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accelerator")
	assert.Contains(t, err.Error(), "not supported")
}
