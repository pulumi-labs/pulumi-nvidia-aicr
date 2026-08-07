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
			// In the SDK's request schema but with no backing recipes in the
			// pinned data — must fail here with the friendly allowlist error,
			// not inside SDK resolution.
			name: "os without backing recipes",
			args: ClusterStackArgs{Accelerator: "h100", Service: "eks", Intent: "training", OS: str("rhel")},
			want: `os "rhel" is not supported`,
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
	// Optional fields (OS, Platform, Nodes) left unset must pass validation —
	// the SDK resolves the OS-agnostic recipe / "no platform" base recipe.
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
			OS:          pulumi.StringRef("ubuntu"),
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
			OS:          pulumi.StringRef("ubuntu"),
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
	// On gke-cos/h100/training the recipe pulls in manifest-only components
	// (nodewright-customizations, gke-nccl-tcpxo) plus side-car manifests
	// for kubeflow-trainer. Each must surface as a yaml/v2 ConfigGroup
	// resource — a "skip if no chart" behavior would drop them.
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
	assert.True(t, manifestNames["stack-nodewright-customizations-manifests"],
		"expected nodewright-customizations manifest bundle; got: %v", manifestNames)
	assert.True(t, manifestNames["stack-gke-nccl-tcpxo-manifests"],
		"expected gke-nccl-tcpxo manifest bundle; got: %v", manifestNames)
	assert.True(t, manifestNames["stack-kubeflow-trainer-manifests"],
		"expected kubeflow-trainer side-car manifest bundle; got: %v", manifestNames)
}

func TestNewClusterStackDeploysPreManifestsBeforeChart(t *testing.T) {
	// gb200-eks recipes attach a kernel-module pre-manifest to gpu-operator;
	// it must surface as its own ConfigGroup alongside the Helm release.
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "gb200",
			Service:     "eks",
			Intent:      "training",
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	var preManifest, release bool
	for _, r := range mon.resources {
		switch {
		case r.typeToken == "kubernetes:yaml/v2:ConfigGroup" && r.name == "stack-gpu-operator-pre-manifests":
			preManifest = true
		case strings.HasPrefix(r.typeToken, "kubernetes:helm.sh/v3:Release") && r.name == "stack-gpu-operator":
			release = true
		}
	}
	assert.True(t, preManifest, "expected gpu-operator pre-manifest ConfigGroup")
	assert.True(t, release, "expected gpu-operator Helm release")
}

func TestNewClusterStackTreatsEmptyManifestRenderAsNoOp(t *testing.T) {
	// A manifest-only component whose templates all render to nothing
	// (e.g. nodewright-customizations with an `enabled: false` override)
	// is a deliberate user-driven no-op, not a configuration error. A
	// "no chart and no manifests" guard would have failed the entire
	// deploy in that case.
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
				"nodewright-customizations": {
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
			assert.NotEqualf(t, "stack-nodewright-customizations-manifests", r.name,
				"disabled nodewright-customizations bundle must not register a ConfigGroup")
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
			OS:             pulumi.StringRef("ubuntu"),
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

func TestNewClusterStackKindLocalDev(t *testing.T) {
	// service=kind is the hardware-free local development path. The old
	// vendored resolver could never resolve it (it forced os=ubuntu while
	// kind recipes bind no OS); the SDK resolves it with OS unset, so a
	// plain kind ClusterStack must produce a resource graph.
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "kind",
			Intent:      "inference",
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	releaseNames := map[string]bool{}
	for _, r := range mon.resources {
		if strings.HasPrefix(r.typeToken, "kubernetes:helm.sh/v3:Release") {
			releaseNames[r.name] = true
		}
	}
	assert.True(t, releaseNames["stack-gpu-operator"], "gpu-operator release missing; got: %v", releaseNames)
	assert.True(t, releaseNames["stack-agentgateway"], "agentgateway release missing; got: %v", releaseNames)
}

func TestNewClusterStackComposesChartCoordinates(t *testing.T) {
	// OCI sources: ComponentRef.Source is the OCI namespace and Chart the
	// chart within it; the full reference is always Source + "/" + Chart,
	// even when the namespace ends with the chart name (see NVIDIA/aicr#1954
	// — kai-scheduler's truncated reference addressed the parent repository
	// and failed with a 403 that masqueraded as a permissions error).
	// HTTP sources: chart name and repository stay separate.
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "kind",
			Intent:      "inference",
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	charts := map[string]struct{ chart, repo string }{}
	for _, r := range mon.resources {
		if !strings.HasPrefix(r.typeToken, "kubernetes:helm.sh/v3:Release") {
			continue
		}
		chart := r.inputs["chart"].StringValue()
		repo := ""
		if ro, ok := r.inputs["repositoryOpts"]; ok && ro.IsObject() {
			if rv, ok := ro.ObjectValue()["repo"]; ok && rv.IsString() {
				repo = rv.StringValue()
			}
		}
		charts[r.name] = struct{ chart, repo string }{chart, repo}
	}

	kai, ok := charts["stack-kai-scheduler"]
	require.True(t, ok, "kai-scheduler release missing; got %v", charts)
	assert.Equal(t, "oci://ghcr.io/kai-scheduler/kai-scheduler/kai-scheduler", kai.chart,
		"OCI reference must be Source + \"/\" + Chart even when Source ends with the chart name")
	assert.Empty(t, kai.repo, "OCI releases must not set a separate repository")

	cm, ok := charts["stack-cert-manager"]
	require.True(t, ok, "cert-manager release missing")
	assert.Equal(t, "cert-manager", cm.chart)
	assert.Equal(t, "https://charts.jetstack.io", cm.repo,
		"HTTP releases keep chart name and repository separate")
}

func TestNewClusterStackPropagatesSkipAwait(t *testing.T) {
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		skip := true
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "kind",
			Intent:      "inference",
			SkipAwait:   &skip,
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	checked := 0
	for _, r := range mon.resources {
		switch {
		case strings.HasPrefix(r.typeToken, "kubernetes:helm.sh/v3:Release"),
			r.typeToken == "kubernetes:yaml/v2:ConfigGroup":
			assert.Truef(t, r.inputs["skipAwait"].BoolValue(),
				"%s must carry skipAwait=true", r.name)
			checked++
		}
	}
	assert.Greater(t, checked, 5, "expected skipAwait asserted on several resources")
}

func TestValidateArgsRejectsNegativeNodes(t *testing.T) {
	nodes := -1
	err := validateArgs(&ClusterStackArgs{
		Accelerator: "h100", Service: "eks", Intent: "training", Nodes: &nodes,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-negative")

	valid := 4
	assert.NoError(t, validateArgs(&ClusterStackArgs{
		Accelerator: "h100", Service: "eks", Intent: "training", Nodes: &valid,
	}))
}

func TestNewClusterStackPreservesNullHelmValues(t *testing.T) {
	// Explicit nulls in recipe values are semantic: Helm deletes the chart
	// default for a key set to null. The AICR eks overlay sets
	// controller.affinity.nodeAffinity: null on nvidia-dra-driver-gpu to
	// clear the chart's default GPU node affinity; stringifying it to
	// "<nil>" (the old toPulumiInput behavior) rendered a Deployment the
	// API server rejects (nodeAffinity must be an object, not a string).
	mon := &recordingMonitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := NewClusterStack(ctx, "stack", &ClusterStackArgs{
			Accelerator: "h100",
			Service:     "eks",
			Intent:      "training",
			OS:          pulumi.StringRef("ubuntu"),
		})
		return err
	}, pulumi.WithMocks("project", "stack", mon))
	require.NoError(t, err)

	mon.mu.Lock()
	defer mon.mu.Unlock()

	for _, r := range mon.resources {
		if !strings.HasPrefix(r.typeToken, "kubernetes:helm.sh/v3:Release") || r.name != "stack-nvidia-dra-driver-gpu" {
			continue
		}
		require.True(t, r.inputs["allowNullValues"].BoolValue(),
			"allowNullValues must be set so the Helm provider honors null-deletion")
		files := r.inputs["valueYamlFiles"].ArrayValue()
		require.Len(t, files, 1, "expected exactly one values YAML asset")
		yamlText := files[0].AssetValue().Text
		assert.Contains(t, yamlText, "nodeAffinity: null",
			"the recipe's explicit null must survive into the values YAML")
		assert.NotContains(t, yamlText, "<nil>",
			"nulls must not be stringified")
		return
	}
	t.Fatal("nvidia-dra-driver-gpu release not found")
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
