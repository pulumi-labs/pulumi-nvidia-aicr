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

func TestNewClusterStackErrorsOnUnknownRecipe(t *testing.T) {
	mon := &recordingMonitor{}
	// Use criteria where every dimension is fictional so no recipe can score
	// a positive match (not even the generic service overlays).
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "fictional-gpu",
			Service:     "fictional-cloud",
			Intent:      "fictional-intent",
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no matching recipe found")
}
