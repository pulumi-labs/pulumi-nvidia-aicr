# Pulumi NVIDIA AICR Provider

Deploy validated [NVIDIA AI Cluster Runtime (AICR)](https://github.com/nvidia/aicr) configurations
on Kubernetes clusters using Pulumi — and validate the result empirically. Define your GPU
infrastructure, its software stack, and its acceptance checks in a single program with full
lifecycle management.

## Overview

NVIDIA AICR captures validated combinations of GPU drivers, operators, and system configurations
as reproducible **recipes** for GPU-accelerated Kubernetes clusters. This Pulumi provider brings
AICR into the Infrastructure as Code ecosystem, enabling:

- **Single program** — Define cloud infrastructure (EKS, GKE, AKS, OKE, or an existing cluster such as CoreWeave CKS) and the GPU software stack together
- **Validated recipes** — Deploy known-good component combinations, not ad-hoc configs (`ClusterStack`)
- **Empirical validation** — Run AICR's deployment/conformance/performance checks against the live cluster and record the verdict in state (`ValidationRun`)
- **Full lifecycle** — Preview, deploy, update, and destroy with standard Pulumi workflows
- **Multi-language** — Use TypeScript, Python, Go, C#, Java, or YAML

## Installation

Add the SDK for your language; the provider plugin is downloaded automatically from this repo's
GitHub releases on first use.

| Language | Package |
|---|---|
| TypeScript / JavaScript | `npm install @pulumi-labs/nvidia-aicr` |
| Python | `pip install pulumi-labs-nvidia-aicr` (module `pulumi_labs_nvidia_aicr`) |
| Go | `go get github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr` |
| C# | `dotnet add package Pulumi.Labs.NvidiaAicr` |
| Java | `com.pulumi.labs:nvidia-aicr` — build from [`sdk/java`](./sdk/java) (not yet on Maven Central) |
| YAML | no SDK; reference resources by type token (`nvidia-aicr:index:ClusterStack`, `nvidia-aicr:index:ValidationRun`) |

To install the plugin manually (air-gapped CI, pinning):

```bash
pulumi plugin install resource nvidia-aicr <version> --server github://api.github.com/pulumi-labs/pulumi-nvidia-aicr
```

## Quick Start

Deploy the validated stack onto an existing cluster (ambient kubeconfig: `~/.kube/config` or
`KUBECONFIG`), then validate it.

### Python

```python
import pulumi
import pulumi_labs_nvidia_aicr as aicr

# Deploy the NVIDIA AICR-validated GPU software stack for H100 + EKS + Kubeflow training.
gpu_stack = aicr.ClusterStack("nvidia-aicr",
    accelerator="h100",
    service="eks",
    intent="training",
    platform="kubeflow",
)

# Validate the deployed stack: cluster snapshot + deployment and conformance checks.
# Wiring `criteria` from the stack guarantees validation targets the exact recipe
# (and component subset) that was deployed.
validation = aicr.ValidationRun("nvidia-aicr-validation",
    criteria=gpu_stack.criteria,
    recipe_data_version=gpu_stack.recipe_version,   # assert the same recipe data as deployed
    triggers=[gpu_stack.deployed_components],        # re-validate when the stack changes
    opts=pulumi.ResourceOptions(depends_on=[gpu_stack]),
)

pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("components", gpu_stack.deployed_components)
pulumi.export("validation_status", validation.status)      # "passed" | "failed" | "readiness-failed"
pulumi.export("validation_checks", validation.phase_results)
```

### TypeScript

```typescript
import * as eks from "@pulumi/eks";
import * as aicr from "@pulumi-labs/nvidia-aicr";

// Create an EKS cluster with H100 GPU nodes
const cluster = new eks.Cluster("gpu-cluster", {
    instanceType: "p5.48xlarge",
    desiredCapacity: 2,
});

// Deploy ~14 validated Helm charts: GPU Operator, Kubeflow Trainer,
// KAI Scheduler, Prometheus, cert-manager, and more.
const gpuStack = new aicr.ClusterStack("nvidia-aicr", {
    kubeconfig: cluster.kubeconfigJson,
    accelerator: "h100",
    service: "eks",
    intent: "training",
    platform: "kubeflow",
});

// Validate it against the live cluster.
const validation = new aicr.ValidationRun("nvidia-aicr-validation", {
    kubeconfig: cluster.kubeconfigJson,
    criteria: gpuStack.criteria,
    recipeDataVersion: gpuStack.recipeVersion,
    triggers: [gpuStack.deployedComponents],
}, { dependsOn: [gpuStack] });

export const recipeName = gpuStack.recipeName;
export const components = gpuStack.deployedComponents;
export const validationStatus = validation.status;
export const validationChecks = validation.phaseResults;
```

## Resource: ClusterStack

The `ClusterStack` component resource deploys a complete AICR-validated GPU software stack
on a Kubernetes cluster as a set of Helm releases (plus raw manifests where the recipe ships them),
in the recipe's dependency order.

### Inputs

| Property | Type | Required | Description |
|---|---|---|---|
| `accelerator` | `string` | Yes | GPU type: `"h100"`, `"gb200"`, `"b200"`, `"rtx-pro-6000"` |
| `service` | `string` | Yes | Kubernetes service: `"aks"`, `"eks"`, `"gke"`, `"oke"`, `"kind"`, plus the cloud-neutral leaves `"bcm"` and `"lke"` (no hyperscaler CSI/EFA components; they double as stand-ins for providers without an AICR criteria value yet — e.g. CoreWeave CKS deploys the `lke` leaf) |
| `intent` | `string` | Yes | Workload type: `"training"`, `"inference"` |
| `os` | `string` | No | OS: `"ubuntu"`, `"cos"` (gke only), `"ol"` (oke) — the values backed by recipes in the pinned AICR data; more arrive via SDK upgrades. Leave unset for OS-agnostic resolution; set it when the cluster's OS is known. Some combinations require it (gke needs `"cos"`, platform recipes need `"ubuntu"`); `kind` requires it unset. |
| `platform` | `string` | No | ML platform: `"kubeflow"` (training), `"dynamo"` (inference), `"nim"` (inference, EKS+H100 only). Leave unset for the base recipe without a platform-specific runtime. `intent: "inference"` always includes an inference gateway as part of the base inference stack; choosing a platform layers a runtime on top. |
| `nodes` | `int` | No | Worker-node count hint used to size the recipe (nodes, not GPUs) |
| `kubeconfig` | `Input<string>` | No | Kubeconfig contents (accepts outputs from cluster resources) |
| `kubeconfigPath` | `string` | No | Path to kubeconfig file |
| `context` | `string` | No | Kubeconfig context |
| `componentOverrides` | `map` | No | Per-component overrides: Helm `values` (deep-merged), chart `version`, `namespace` |
| `skipComponents` | `string[]` | No | Components to exclude from deployment (bring-your-own cert-manager, platform-managed GPU operator, …). Echoed in the `criteria` output so a wired `ValidationRun` treats them as out of scope. |
| `skipAwait` | `bool` | No | Skip waiting for Helm releases to become ready (default: false) |

`accelerator`, `service`, `intent`, `os`, `platform`, and `nodes` are plain strings/ints (not
outputs) because the recipe is resolved at plan time — `pulumi preview` shows exactly which
components will be deployed. If neither `kubeconfig` nor `kubeconfigPath` is set, the ambient
kubeconfig (`~/.kube/config` or `KUBECONFIG` env var) is used.

### Outputs

| Property | Type | Description |
|---|---|---|
| `recipeName` | `string` | Resolved recipe identifier (e.g. `h100-eks-ubuntu-training-kubeflow`) |
| `recipeVersion` | `string` | AICR SDK module version providing the recipe data (e.g. `v0.18.0`) |
| `deployedComponents` | `string[]` | Names of deployed components, in deployment order |
| `componentCount` | `int` | Number of deployed components |
| `criteria` | `RecipeCriteria` | The canonicalized criteria the stack resolved with (`accelerator`, `service`, `intent`, `os`, `platform`, `nodes`, `skipComponents`). Wire it into `ValidationRun.criteria`. |

### What Gets Deployed

A typical training recipe (H100 + EKS + Ubuntu + Kubeflow) deploys these validated components:

| Component | Purpose |
|---|---|
| **cert-manager** | TLS certificate management |
| **nfd** | Node Feature Discovery for hardware labeling |
| **gpu-operator** | NVIDIA GPU drivers, device plugin, DCGM |
| **nvsentinel** | GPU health monitoring and remediation |
| **nodewright-operator** | Node OS/kernel customization operator |
| **nodewright-customizations** | GPU node tuning (GRUB, sysctl, containerd limits) |
| **prometheus-operator-crds** | Prometheus operator CRDs |
| **kube-prometheus-stack** | Monitoring with GPU metrics (Prometheus + Grafana) |
| **prometheus-adapter** | Custom-metrics API for autoscaling |
| **k8s-ephemeral-storage-metrics** | Storage monitoring |
| **nvidia-dra-driver-gpu** | Dynamic Resource Allocation for GPUs |
| **kai-scheduler** | GPU-aware workload scheduling |
| **aws-ebs-csi-driver** | EKS: Persistent volume provisioning |
| **aws-efa** | EKS: Elastic Fabric Adapter for RDMA networking |
| **kubeflow-trainer** | Distributed training with TrainJob |

## Resource: ValidationRun

`ValidationRun` runs an AICR recipe's empirical validation against a live cluster at deploy time
and records structured per-check results in Pulumi state. It uses the SDK's validation harness:
a short-lived snapshot-agent Job captures the cluster's configuration, then one validator Job per
check runs the recipe's declared **deployment**, **conformance**, and (opt-in) **performance**
checks — GPU operator health, `nvidia-smi` on every GPU node, DCGM metrics flowing into
Prometheus, gang scheduling through KAI, DRA support, NCCL bandwidth, and so on.

Wire it to a `ClusterStack` so deployment and validation share one source of truth:

- `criteria` — the stack's `criteria` output: same recipe, same component subset. A stack deployed
  with `skipComponents` is a different stack; the criteria carry the skipped names, so checks that
  presuppose a skipped component (for example the GPU-operator health and DCGM metrics checks when
  `gpu-operator` is platform-managed) report `skipped` with a reason instead of failing, and the
  SDK's component-aware checks scope themselves to what was deployed. Skipping does not verify a
  replacement you run yourself — those checks are simply not made.
- `recipeDataVersion` — the stack's `recipeVersion`: asserts the validator uses the same recipe
  data the stack deployed (fails fast otherwise).
- `triggers` — e.g. the stack's `deployedComponents`: any change re-runs validation.
- `dependsOn` the stack so validation starts after the stack is up.

### Inputs

| Property | Type | Required | Description |
|---|---|---|---|
| `criteria` | `RecipeCriteria` | Yes | Recipe criteria (`accelerator`, `service`, `intent`, optional `os`, `platform`, `nodes`, `skipComponents`). Wire a `ClusterStack`'s `criteria` output or build inline. |
| `recipeDataVersion` | `string` | No | Recipe-data version assertion (not a pin); wire `stack.recipeVersion` |
| `kubeconfig` / `kubeconfigPath` / `context` | `string` | No | Cluster access, same conventions as `ClusterStack`; ambient kubeconfig when unset. `kubeconfig` is written to a mode-0600 temp file for the run. |
| `phases` | `string[]` | No | Any of `"deployment"`, `"conformance"`, `"performance"`. Default `["deployment", "conformance"]` — performance (NCCL, inference benchmarks) is an explicit opt-in |
| `strict` | `bool` | No | Default `false`: a failed validation is recorded (`status: "failed"`, plus a warning diagnostic listing the failed checks) and the update succeeds. `true`: a failed or readiness-failed run fails the update while persisting full results, and re-runs on every subsequent `pulumi up` until it passes — use `dependsOn` to gate downstream resources |
| `requireGpu` | `bool` | No | Whether the snapshot agent requires GPU nodes (default `true`; set `false` for hardware-free clusters such as kind) |
| `tolerations` | `Toleration[]` | No | Applied to validation pods; unset keeps the validator's default tolerate-all, so tainted GPU node groups need no configuration |
| `nodeSelector` | `map<string>` | No | Node selector for validation pods |
| `namespace` | `string` | No | Validation namespace (default `aicr-validation`); created on first run and deliberately never deleted |
| `timeoutMinutes` | `int` | No | Overall run timeout (default 30, max 1440) |
| `imageRegistry` / `imagePullSecrets` | `string` / `string[]` | No | Air-gapped mirrors for the agent and validator images |
| `includeCtrfReport` | `bool` | No | Store the merged CTRF JSON report in `ctrfReport` (default `false`; per-check results are always in `phaseResults`) |
| `triggers` | `any[]` | No | Arbitrary values; a change replaces the resource and re-runs validation (every input change does) |

### Outputs

| Property | Type | Description |
|---|---|---|
| `status` | `string` | `"passed"`, `"failed"`, or `"readiness-failed"` (the cluster does not meet the recipe's readiness constraints, e.g. OS or Kubernetes version) |
| `phaseResults` | `CheckResult[]` | Per-check `name`, `phase`, `status` (`passed` / `failed` / `skipped` / `other`), `message` |
| `passed` / `failed` / `skipped` / `other` | `int` | Counts; `other` is inconclusive (crash, OOM, timeout) and never flips the verdict |
| `readinessMessage` | `string` | The readiness failure, when `status` is `"readiness-failed"` |
| `recipeName` / `recipeVersion` | `string` | What was validated |
| `runId` / `completedAt` | `string` | Run identifier (labels the run's cluster-side artifacts) and RFC 3339 completion time |
| `ctrfReport` | `string` | Merged CTRF JSON, when `includeCtrfReport` is set |

### Semantics

- **Preview never runs anything.** Validation happens at apply time.
- **Any input change replaces the resource** (a fresh run); delete is a no-op — the harness cleans
  its own Jobs, RBAC and ConfigMaps per run.
- **Privilege:** the snapshot agent runs privileged (read-only node inspection ClusterRole) and
  validator Jobs run under a per-run cluster-admin ClusterRoleBinding, both removed afterwards. The
  kubeconfig identity needs RBAC to create/patch Namespaces, ClusterRoles and ClusterRoleBindings.
- **Duration:** a deployment + conformance run typically takes 2–10 minutes depending on the recipe
  (each check is a Job); performance checks can take much longer.

### Examples

<details>
<summary>TypeScript</summary>

```typescript
import * as aicr from "@pulumi-labs/nvidia-aicr";

const gpuStack = new aicr.ClusterStack("nvidia-aicr", {
    accelerator: "h100",
    service: "eks",
    intent: "training",
    platform: "kubeflow",
});

const validation = new aicr.ValidationRun("nvidia-aicr-validation", {
    criteria: gpuStack.criteria,
    recipeDataVersion: gpuStack.recipeVersion,
    triggers: [gpuStack.deployedComponents],
    strict: true,                       // fail the update until the cluster validates
}, { dependsOn: [gpuStack] });

export const validationStatus = validation.status;
export const validationChecks = validation.phaseResults;
```

</details>

<details>
<summary>Python</summary>

```python
import pulumi
import pulumi_labs_nvidia_aicr as aicr

gpu_stack = aicr.ClusterStack("nvidia-aicr",
    accelerator="h100",
    service="eks",
    intent="training",
    platform="kubeflow",
)

validation = aicr.ValidationRun("nvidia-aicr-validation",
    criteria=gpu_stack.criteria,
    recipe_data_version=gpu_stack.recipe_version,
    triggers=[gpu_stack.deployed_components],
    strict=True,
    opts=pulumi.ResourceOptions(depends_on=[gpu_stack]),
)

pulumi.export("validation_status", validation.status)
pulumi.export("validation_checks", validation.phase_results)
```

</details>

<details>
<summary>Go</summary>

```go
gpuStack, err := aicr.NewClusterStack(ctx, "nvidia-aicr", &aicr.ClusterStackArgs{
    Accelerator: "h100",
    Service:     "eks",
    Intent:      "training",
    Platform:    pulumi.StringRef("kubeflow"),
})
if err != nil {
    return err
}

validation, err := aicr.NewValidationRun(ctx, "nvidia-aicr-validation", &aicr.ValidationRunArgs{
    Criteria:          gpuStack.Criteria, // the stack's own criteria output
    RecipeDataVersion: gpuStack.RecipeVersion,
    Triggers:          pulumi.Array{gpuStack.DeployedComponents},
    Strict:            pulumi.Bool(true),
}, pulumi.DependsOn([]pulumi.Resource{gpuStack}))
if err != nil {
    return err
}

ctx.Export("validationStatus", validation.Status)
ctx.Export("validationChecks", validation.PhaseResults)
```

</details>

<details>
<summary>C#</summary>

```csharp
using Pulumi;
using Pulumi.Labs.NvidiaAicr;
using Pulumi.Labs.NvidiaAicr.Inputs;

var gpuStack = new ClusterStack("nvidia-aicr", new ClusterStackArgs
{
    Accelerator = "h100",
    Service = "eks",
    Intent = "training",
    Platform = "kubeflow",
});

var validation = new ValidationRun("nvidia-aicr-validation", new ValidationRunArgs
{
    // C# (and Java) cannot pass the stack's Criteria output as an input type:
    // re-state the same criteria — including any SkipComponents — to stay in sync.
    Criteria = new RecipeCriteriaArgs
    {
        Accelerator = "h100",
        Service = "eks",
        Intent = "training",
        Platform = "kubeflow",
    },
    RecipeDataVersion = gpuStack.RecipeVersion,
    Triggers = gpuStack.DeployedComponents.Apply(c => new object[] { c }),
    Strict = true,
}, new CustomResourceOptions { DependsOn = { gpuStack } });

return new Dictionary<string, object?>
{
    ["validationStatus"] = validation.Status,
    ["validationChecks"] = validation.PhaseResults,
};
```

</details>

<details>
<summary>Java</summary>

```java
import com.pulumi.labs.nvidiaaicr.ClusterStack;
import com.pulumi.labs.nvidiaaicr.ClusterStackArgs;
import com.pulumi.labs.nvidiaaicr.ValidationRun;
import com.pulumi.labs.nvidiaaicr.ValidationRunArgs;
import com.pulumi.labs.nvidiaaicr.inputs.RecipeCriteriaArgs;
import com.pulumi.resources.CustomResourceOptions;
import java.util.List;

var gpuStack = new ClusterStack("nvidia-aicr", ClusterStackArgs.builder()
    .accelerator("h100")
    .service("eks")
    .intent("training")
    .platform("kubeflow")
    .build());

var validation = new ValidationRun("nvidia-aicr-validation", ValidationRunArgs.builder()
    // Re-state the stack's criteria (Java cannot pass the output type as an input).
    .criteria(RecipeCriteriaArgs.builder()
        .accelerator("h100")
        .service("eks")
        .intent("training")
        .platform("kubeflow")
        .build())
    .recipeDataVersion(gpuStack.recipeVersion())
    .triggers(gpuStack.deployedComponents().applyValue(c -> List.<Object>of(c)))
    .strict(true)
    .build(),
    CustomResourceOptions.builder().dependsOn(gpuStack).build());

ctx.export("validationStatus", validation.status());
ctx.export("validationChecks", validation.phaseResults());
```

</details>

<details>
<summary>YAML</summary>

```yaml
resources:
  gpu-stack:
    type: nvidia-aicr:index:ClusterStack
    properties:
      accelerator: h100
      service: eks
      intent: training
      platform: kubeflow
  validation:
    type: nvidia-aicr:index:ValidationRun
    properties:
      criteria: ${gpu-stack.criteria}
      recipeDataVersion: ${gpu-stack.recipeVersion}
      triggers:
        - ${gpu-stack.deployedComponents}
      strict: true
    options:
      dependsOn:
        - ${gpu-stack}
outputs:
  validationStatus: ${validation.status}
  validationChecks: ${validation.phaseResults}
```

</details>

## Customization

### Component Overrides

Customize specific components while keeping the validated recipe baseline. Helm `values` are
deep-merged on top of the recipe's values (nested maps merge; scalars and arrays replace; a `null`
removes the recipe's setting).

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

Exclude components that are already installed or not needed. The skipped names are echoed in the
stack's `criteria` output, so a `ValidationRun` wired to it treats them as out of scope (their
checks report `skipped`, not `failed`). Example: on CoreWeave CKS the platform already runs the GPU
operator operands, NFD and the DRA driver in its own namespaces.

<details>
<summary>TypeScript</summary>

```typescript
const stack = new aicr.ClusterStack("aicr", {
    accelerator: "rtx-pro-6000",
    service: "lke",            // cloud-neutral leaf used for CoreWeave CKS
    intent: "training",
    skipComponents: ["gpu-operator", "nfd", "nvidia-dra-driver-gpu"],
});

const validation = new aicr.ValidationRun("aicr-validation", {
    criteria: stack.criteria,  // carries skipComponents
}, { dependsOn: [stack] });
```

</details>

<details>
<summary>Python</summary>

```python
stack = aicr.ClusterStack("aicr",
    accelerator="rtx-pro-6000",
    service="lke",             # cloud-neutral leaf used for CoreWeave CKS
    intent="training",
    skip_components=["gpu-operator", "nfd", "nvidia-dra-driver-gpu"],
)

validation = aicr.ValidationRun("aicr-validation",
    criteria=stack.criteria,   # carries skip_components
    opts=pulumi.ResourceOptions(depends_on=[stack]),
)
```

</details>

<details>
<summary>Go</summary>

```go
stack, err := aicr.NewClusterStack(ctx, "aicr", &aicr.ClusterStackArgs{
    Accelerator: "rtx-pro-6000",
    Service:     "lke", // cloud-neutral leaf used for CoreWeave CKS
    Intent:      "training",
    SkipComponents: pulumi.StringArray{
        pulumi.String("gpu-operator"),
        pulumi.String("nfd"),
        pulumi.String("nvidia-dra-driver-gpu"),
    },
})
if err != nil {
    return err
}

validation, err := aicr.NewValidationRun(ctx, "aicr-validation", &aicr.ValidationRunArgs{
    Criteria: stack.Criteria, // carries SkipComponents
}, pulumi.DependsOn([]pulumi.Resource{stack}))
```

</details>

<details>
<summary>C#</summary>

```csharp
var stack = new ClusterStack("aicr", new ClusterStackArgs
{
    Accelerator = "rtx-pro-6000",
    Service = "lke", // cloud-neutral leaf used for CoreWeave CKS
    Intent = "training",
    SkipComponents = { "gpu-operator", "nfd", "nvidia-dra-driver-gpu" },
});

var validation = new ValidationRun("aicr-validation", new ValidationRunArgs
{
    Criteria = new RecipeCriteriaArgs
    {
        Accelerator = "rtx-pro-6000",
        Service = "lke",
        Intent = "training",
        SkipComponents = { "gpu-operator", "nfd", "nvidia-dra-driver-gpu" }, // keep in sync
    },
}, new CustomResourceOptions { DependsOn = { stack } });
```

</details>

## Examples

Full working examples for every supported cloud and scenario, each ending with a `ValidationRun`.
See [examples/](./examples/) for prerequisites, cost estimates, and detailed instructions.

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

Criteria values the provider accepts, and the recipe leaves upstream AICR ships for them in the
pinned SDK data (any accepted combination resolves; a combination without a dedicated leaf gets the
closest base recipe):

| Accelerator | Services | Intents | Platforms |
|---|---|---|---|
| H100 | EKS, GKE, AKS, Kind, BCM | Training, Inference | Kubeflow, Dynamo, NIM (EKS only) |
| GB200 | EKS, OKE | Training, Inference | Kubeflow, Dynamo |
| B200 | GKE | Training | Kubeflow |
| RTX PRO 6000 | EKS, LKE | Training, Inference | Kubeflow, Dynamo, NIM (EKS only); LKE ships base training/inference leaves |

`bcm` (NVIDIA Base Command Manager) and `lke` (Linode Kubernetes Engine) are the cloud-neutral
leaves — no hyperscaler CSI/EFA components — and are the stand-ins for providers AICR has no
criteria value for yet (CoreWeave CKS uses `lke`). The `kind` service overlay targets local
development with [kind](https://kind.sigs.k8s.io/) clusters -- useful for exercising the
deployment pipeline without provisioning real GPU hardware.

## Known Limitations

**Redeploying over resources left by a pre-v0.3.0 deployment.** Helm never
removes CRDs (and some charts keep other resources, e.g. kai-scheduler's
Queues) on uninstall. Releases are now installed under deterministic names
(the component name), so a `pulumi destroy` followed by `pulumi up` adopts
those leftovers cleanly. Deployments made with provider versions before
v0.3.0 used randomly-suffixed release names, however — redeploying over
*their* leftovers still fails with `invalid ownership metadata`; delete the
orphaned resources first (`kubectl get crds | grep -e cert-manager -e nvidia`
and `kubectl delete crd ...`), or recreate the cluster (kind). One-time
migration note: the first `pulumi up` after upgrading to v0.3.0 replaces
every Helm release (physical names change) — use a maintenance window on
live clusters. Background in
[#18](https://github.com/pulumi-labs/pulumi-nvidia-aicr/issues/18).

**ValidationRun validates recipe-deployed components only.** Components listed in
`skipComponents` are out of scope for validation, not verified: a platform-managed GPU stack
(e.g. CoreWeave's `cw-*` namespaces) is skipped, not checked. First-class support needs
recipe-level ownership data upstream
([NVIDIA/aicr#2342](https://github.com/NVIDIA/aicr/issues/2342)).

## Development

```bash
# Build provider
make provider

# Run tests
make test

# Generate schema (CI verifies the committed schema matches)
make schema

# Generate all SDKs (nodejs, python, go, dotnet, java + fixups)
make sdks
```

## AICR Version Compatibility

This provider delegates recipe resolution to the official
[NVIDIA AICR Go SDK](https://github.com/NVIDIA/aicr) (`pkg/client/v1`),
whose embedded recipe data is pinned by the SDK module version. The
`recipeVersion` stack output reports the SDK version in use.

| Provider Version | AICR SDK Module Version |
|---|---|
| 0.1.x | v0.18.0 |
| next release (`main`) | v0.19.0 |

## License

Apache 2.0 -- see [LICENSE](./LICENSE) for details.
