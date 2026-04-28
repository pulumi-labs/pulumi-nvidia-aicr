# Pulumi NVIDIA AICR Provider

Deploy validated [NVIDIA AI Cluster Runtime (AICR)](https://github.com/nvidia/aicr) configurations
on Kubernetes clusters using Pulumi. Define your GPU infrastructure and software stack in a single
program with full lifecycle management.

## Overview

NVIDIA AICR captures validated combinations of GPU drivers, operators, and system configurations
as reproducible **recipes** for GPU-accelerated Kubernetes clusters. This Pulumi provider brings
AICR into the Infrastructure as Code ecosystem, enabling:

- **Single program** — Define both cloud infrastructure (EKS, GKE, AKS) and GPU software stack together
- **Validated recipes** — Deploy known-good component combinations, not ad-hoc configs
- **Full lifecycle** — Preview, deploy, update, and destroy with standard Pulumi workflows
- **Multi-language** — Use TypeScript, Python, Go, C#, Java, or YAML

## Quick Start

### Python

```python
import pulumi
import pulumi_nvidia_aicr as aicr

# Deploy the NVIDIA AICR-validated GPU software stack.
# Uses your ambient kubeconfig (~/.kube/config or KUBECONFIG).
gpu_stack = aicr.ClusterStack("nvidia-aicr",
    accelerator="h100",
    service="eks",
    intent="training",
    platform="kubeflow",
)

pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("components", gpu_stack.deployed_components)
```

### TypeScript

```typescript
import * as eks from "@pulumi/eks";
import * as aicr from "@pulumi/nvidia-aicr";

// Create an EKS cluster with H100 GPU nodes
const cluster = new eks.Cluster("gpu-cluster", {
    instanceType: "p5.48xlarge",
    desiredCapacity: 2,
});

// Deploy ~11 validated Helm charts: GPU Operator, Kubeflow Trainer,
// KAI Scheduler, Prometheus, cert-manager, and more.
const gpuStack = new aicr.ClusterStack("nvidia-aicr", {
    kubeconfig: cluster.kubeconfigJson,
    accelerator: "h100",
    service: "eks",
    intent: "training",
    platform: "kubeflow",
});

export const recipeName = gpuStack.recipeName;
export const components = gpuStack.deployedComponents;
```

## Resource: ClusterStack

The `ClusterStack` component resource deploys a complete AICR-validated GPU software stack
on a Kubernetes cluster.

### Inputs

| Property | Type | Required | Description |
|---|---|---|---|
| `accelerator` | `string` | Yes | GPU type: `"h100"`, `"gb200"`, `"b200"` |
| `service` | `string` | Yes | Kubernetes service: `"aks"`, `"eks"`, `"gke"`, `"kind"`, `"oke"` |
| `intent` | `string` | Yes | Workload type: `"training"`, `"inference"` |
| `os` | `string` | No | OS: `"ubuntu"` (default), `"cos"` (gke only) |
| `platform` | `string` | No | ML platform: `"kubeflow"` (training), `"dynamo"` (inference), `"nim"` (inference, EKS+H100 only). Leave unset for the base recipe without a platform-specific runtime. `intent: "inference"` always includes the kgateway inference gateway as part of the base inference stack; choosing a platform layers a runtime on top. |
| `kubeconfig` | `Input<string>` | No | Kubeconfig contents (accepts outputs from cluster resources) |
| `kubeconfigPath` | `string` | No | Path to kubeconfig file |
| `context` | `string` | No | Kubeconfig context |
| `componentOverrides` | `map` | No | Per-component Helm value overrides |
| `skipComponents` | `string[]` | No | Components to exclude from deployment |
| `skipAwait` | `bool` | No | Skip waiting for Helm releases (default: false) |

If neither `kubeconfig` nor `kubeconfigPath` is set, the ambient kubeconfig (`~/.kube/config`
or `KUBECONFIG` env var) is used.

### Outputs

| Property | Type | Description |
|---|---|---|
| `recipeName` | `string` | Resolved recipe identifier |
| `recipeVersion` | `string` | AICR recipe version |
| `deployedComponents` | `string[]` | Names of deployed components |
| `componentCount` | `int` | Number of deployed components |

### What Gets Deployed

A typical training recipe (H100 + EKS + Kubeflow) deploys these validated components:

| Component | Purpose |
|---|---|
| **cert-manager** | TLS certificate management |
| **gpu-operator** | NVIDIA GPU drivers, device plugin, DCGM |
| **nvsentinel** | GPU security policies |
| **skyhook-operator** | GPU virtualization |
| **kube-prometheus-stack** | Monitoring with GPU metrics (Prometheus + Grafana) |
| **k8s-ephemeral-storage-metrics** | Storage monitoring |
| **nvidia-dra-driver-gpu** | Dynamic Resource Allocation for GPUs |
| **kai-scheduler** | GPU-aware workload scheduling |
| **aws-ebs-csi-driver** | EKS: Persistent volume provisioning |
| **aws-efa** | EKS: Elastic Fabric Adapter for RDMA networking |
| **kubeflow-trainer** | Distributed training with TrainJob |

## Customization

### Component Overrides

Customize specific components while keeping the validated recipe baseline:

<details>
<summary>TypeScript</summary>

```typescript
const gpuStack = new aicr.ClusterStack("aicr", {
    kubeconfig: cluster.kubeconfigJson,
    accelerator: "h100",
    service: "eks",
    intent: "training",
    componentOverrides: {
        "gpu-operator": {
            version: "v25.11.0",
            values: {
                driver: { version: "535.129.03" },
            },
        },
    },
});
```

</details>

<details>
<summary>Python</summary>

```python
gpu_stack = aicr.ClusterStack("aicr",
    kubeconfig=cluster.kubeconfig_json,
    accelerator="h100",
    service="eks",
    intent="training",
    component_overrides={
        "gpu-operator": aicr.ComponentOverrideArgs(
            version="v25.11.0",
            values={
                "driver": {"version": "535.129.03"},
            },
        ),
    },
)
```

</details>

<details>
<summary>Go</summary>

```go
gpuStack, err := aicr.NewClusterStack(ctx, "aicr", &aicr.ClusterStackArgs{
    Kubeconfig:  cluster.KubeconfigJson,
    Accelerator: "h100",
    Service:     "eks",
    Intent:      "training",
    ComponentOverrides: aicr.ComponentOverrideMap{
        "gpu-operator": aicr.ComponentOverrideArgs{
            Version: pulumi.StringPtr("v25.11.0"),
            Values: pulumi.Map{
                "driver": pulumi.Map{"version": pulumi.String("535.129.03")},
            },
        },
    },
})
```

</details>

<details>
<summary>C#</summary>

```csharp
var gpuStack = new ClusterStack("aicr", new ClusterStackArgs
{
    Kubeconfig = cluster.KubeconfigJson,
    Accelerator = "h100",
    Service = "eks",
    Intent = "training",
    ComponentOverrides =
    {
        ["gpu-operator"] = new ComponentOverrideArgs
        {
            Version = "v25.11.0",
            Values = { ["driver"] = new InputMap<object> { ["version"] = "535.129.03" } },
        },
    },
});
```

</details>

### Skipping Components

Exclude components that are already installed or not needed:

<details>
<summary>TypeScript</summary>

```typescript
const stack = new aicr.ClusterStack("aicr", {
    accelerator: "h100",
    service: "eks",
    intent: "inference",
    platform: "dynamo",
    skipComponents: ["cert-manager", "kube-prometheus-stack"],
});
```

</details>

<details>
<summary>Python</summary>

```python
stack = aicr.ClusterStack("aicr",
    accelerator="h100",
    service="eks",
    intent="inference",
    platform="dynamo",
    skip_components=["cert-manager", "kube-prometheus-stack"],
)
```

</details>

<details>
<summary>Go</summary>

```go
stack, err := aicr.NewClusterStack(ctx, "aicr", &aicr.ClusterStackArgs{
    Accelerator: "h100",
    Service:     "eks",
    Intent:      "inference",
    Platform:    pulumi.StringPtr("dynamo"),
    SkipComponents: pulumi.StringArray{
        pulumi.String("cert-manager"),
        pulumi.String("kube-prometheus-stack"),
    },
})
```

</details>

<details>
<summary>C#</summary>

```csharp
var stack = new ClusterStack("aicr", new ClusterStackArgs
{
    Accelerator = "h100",
    Service = "eks",
    Intent = "inference",
    Platform = "dynamo",
    SkipComponents = { "cert-manager", "kube-prometheus-stack" },
});
```

</details>

## Examples

Full working examples for every supported cloud and scenario. See
[examples/](./examples/) for prerequisites, cost estimates, and detailed
instructions.

**Training — PyTorch distributed training with Kubeflow Trainer:**

| Cloud | TypeScript | Python | Go | C# | Java |
|---|---|---|---|---|---|
| AWS EKS (H100) | [ts](./examples/aws-eks-training-ts/) | [py](./examples/aws-eks-training-py/) | [go](./examples/aws-eks-training-go/) | [cs](./examples/aws-eks-training-cs/) | [java](./examples/aws-eks-training-java/) |
| Azure AKS (H100) | [ts](./examples/azure-aks-training-ts/) | [py](./examples/azure-aks-training-py/) | [go](./examples/azure-aks-training-go/) | [cs](./examples/azure-aks-training-cs/) | [java](./examples/azure-aks-training-java/) |
| GCP GKE (H100) | [ts](./examples/gcp-gke-training-ts/) | [py](./examples/gcp-gke-training-py/) | [go](./examples/gcp-gke-training-go/) | [cs](./examples/gcp-gke-training-cs/) | [java](./examples/gcp-gke-training-java/) |
| OCI OKE (GB200) | [ts](./examples/oci-oke-training-ts/) | [py](./examples/oci-oke-training-py/) | [go](./examples/oci-oke-training-go/) | [cs](./examples/oci-oke-training-cs/) | [java](./examples/oci-oke-training-java/) |

**Inference — vLLM model serving with NIM / Dynamo:**

| Cloud | TypeScript | Python | Go | C# | Java |
|---|---|---|---|---|---|
| AWS EKS + NIM (H100) | [ts](./examples/aws-eks-inference-ts/) | [py](./examples/aws-eks-inference-py/) | [go](./examples/aws-eks-inference-go/) | [cs](./examples/aws-eks-inference-cs/) | [java](./examples/aws-eks-inference-java/) |
| CoreWeave + Dynamo (H100) | [ts](./examples/coreweave-inference-ts/) | [py](./examples/coreweave-inference-py/) | [go](./examples/coreweave-inference-go/) | [cs](./examples/coreweave-inference-cs/) | [java](./examples/coreweave-inference-java/) |

**Getting started:**

| Scenario | TypeScript | Python | Go | C# | Java | YAML |
|---|---|---|---|---|---|---|
| Existing cluster (quickstart) | [ts](./examples/existing-cluster-ts/) | [py](./examples/existing-cluster-py/) | [go](./examples/existing-cluster-go/) | [cs](./examples/existing-cluster-cs/) | [java](./examples/existing-cluster-java/) | [yaml](./examples/existing-cluster-yaml/) |
| Kind local dev (no GPUs) | [ts](./examples/kind-local-dev-ts/) | [py](./examples/kind-local-dev-py/) | [go](./examples/kind-local-dev-go/) | [cs](./examples/kind-local-dev-cs/) | [java](./examples/kind-local-dev-java/) | [yaml](./examples/kind-local-dev-yaml/) |

## Supported Configurations

Validated recipe overlays shipped by upstream AICR:

| Accelerator | Services | Intents | Platforms |
|---|---|---|---|
| H100 | EKS, GKE, AKS, Kind | Training, Inference | Kubeflow, Dynamo, NIM (EKS only) |
| GB200 | EKS, OKE | Training, Inference | Kubeflow, Dynamo |
| B200 | Any | Training | -- |

The `kind` service overlay targets local development with [kind](https://kind.sigs.k8s.io/)
clusters -- useful for exercising the deployment pipeline without provisioning
real GPU hardware.

## Development

```bash
# Build provider
make provider

# Run tests
make test

# Generate schema
make schema

# Generate SDKs
make nodejs_sdk python_sdk go_sdk
```

## AICR Version Compatibility

This provider embeds AICR recipe data. The provider version tracks the AICR version:

| Provider Version | AICR Version |
|---|---|
| 0.1.x | main (development) |

## License

Apache 2.0 -- see [LICENSE](./LICENSE) for details.
