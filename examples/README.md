# Examples

Full working examples for every supported cloud and scenario, in all Pulumi
languages.

## Training — PyTorch distributed training with Kubeflow Trainer

| Cloud | TypeScript | Python | Go | C# | Java |
|---|---|---|---|---|---|
| AWS EKS (H100) | [ts](./aws-eks-training-ts/) | [py](./aws-eks-training-py/) | [go](./aws-eks-training-go/) | [cs](./aws-eks-training-cs/) | [java](./aws-eks-training-java/) |
| Azure AKS (H100) | [ts](./azure-aks-training-ts/) | [py](./azure-aks-training-py/) | [go](./azure-aks-training-go/) | [cs](./azure-aks-training-cs/) | [java](./azure-aks-training-java/) |
| GCP GKE (H100) | [ts](./gcp-gke-training-ts/) | [py](./gcp-gke-training-py/) | [go](./gcp-gke-training-go/) | [cs](./gcp-gke-training-cs/) | [java](./gcp-gke-training-java/) |
| OCI OKE (GB200) | [ts](./oci-oke-training-ts/) | [py](./oci-oke-training-py/) | [go](./oci-oke-training-go/) | [cs](./oci-oke-training-cs/) | [java](./oci-oke-training-java/) |

## Inference — vLLM model serving with NIM / Dynamo

| Cloud | TypeScript | Python | Go | C# | Java |
|---|---|---|---|---|---|
| AWS EKS + NIM (H100) | [ts](./aws-eks-inference-ts/) | [py](./aws-eks-inference-py/) | [go](./aws-eks-inference-go/) | [cs](./aws-eks-inference-cs/) | [java](./aws-eks-inference-java/) |
| CoreWeave + Dynamo (H100) | [ts](./coreweave-inference-ts/) | [py](./coreweave-inference-py/) | [go](./coreweave-inference-go/) | [cs](./coreweave-inference-cs/) | [java](./coreweave-inference-java/) |

## Getting started

| Scenario | TypeScript | Python | Go | C# | Java | YAML |
|---|---|---|---|---|---|---|
| Existing cluster (quickstart) | [ts](./existing-cluster-ts/) | [py](./existing-cluster-py/) | [go](./existing-cluster-go/) | [cs](./existing-cluster-cs/) | [java](./existing-cluster-java/) | [yaml](./existing-cluster-yaml/) |
| Kind local dev (no GPUs) | [ts](./kind-local-dev-ts/) | [py](./kind-local-dev-py/) | [go](./kind-local-dev-go/) | [cs](./kind-local-dev-cs/) | [java](./kind-local-dev-java/) | [yaml](./kind-local-dev-yaml/) |

## Scenarios

### existing-cluster

The fastest way to try AICR. Deploys onto whatever cluster your ambient
`KUBECONFIG` points at. Set the criteria via `pulumi config`:

```bash
cd examples/existing-cluster-ts
npm install
pulumi config set accelerator h100
pulumi config set service eks
pulumi config set intent training
pulumi up
```

**Prerequisites:** a Kubernetes cluster with NVIDIA GPU nodes (H100, GB200,
or B200) reachable via your kubeconfig.

### aws-eks-training

Provisions a complete PyTorch training environment from scratch: VPC, EKS
cluster with H100 GPU nodes, then the full AICR Kubeflow training stack.

```bash
cd examples/aws-eks-training-ts
npm install
pulumi up
```

**Prerequisites:** AWS credentials with permissions to create EKS clusters,
VPCs, and EC2 instances.

**Cost:** `p5.48xlarge` instances are roughly **$98/hr each**. The default
node count is 2, so plan on **~$196/hr** while the cluster is up. Run
`pulumi destroy` when you are finished.

### azure-aks-training

Creates an AKS cluster with H100 GPU nodes on Azure, then deploys the AICR
Kubeflow training stack for distributed PyTorch training.

```bash
cd examples/azure-aks-training-ts
npm install
pulumi up
```

**Prerequisites:** Azure CLI configured (`az login`), subscription with GPU
VM quota for `Standard_ND96isr_H100_v5`.

**Cost:** `Standard_ND96isr_H100_v5` instances are roughly **$40/hr each**.

### gcp-gke-training

Creates a GKE cluster with H100 GPU nodes on Google Cloud, then deploys the
AICR Kubeflow training stack for distributed PyTorch training.

```bash
cd examples/gcp-gke-training-ts
npm install
pulumi up
```

**Prerequisites:** `gcloud` CLI configured, `gke-gcloud-auth-plugin`
installed, project with A3 GPU quota.

**Cost:** `a3-highgpu-8g` instances (8x H100) are roughly **$30/hr each**.

### oci-oke-training

Creates an OKE cluster with NVIDIA GB200 bare-metal GPU nodes on Oracle
Cloud, then deploys the AICR Kubeflow training stack. This is the only
example using GB200 (Grace Blackwell) accelerators.

```bash
cd examples/oci-oke-training-ts
npm install
pulumi config set oci:compartmentId <your-compartment-ocid>
pulumi config set availabilityDomain <your-ad>
pulumi up
```

**Prerequisites:** OCI CLI configured, compartment with GPU bare-metal shape
availability (`BM.GPU.GB200.4`).

**Cost:** GB200 bare-metal shapes are premium; contact Oracle for current
pricing in your region.

### aws-eks-inference

Provisions an EKS cluster with H100 GPU nodes, then deploys the AICR
inference stack with NIM (NVIDIA Inference Microservices) for vLLM-based
model serving.

```bash
cd examples/aws-eks-inference-ts
npm install
pulumi up
```

**Prerequisites:** same as aws-eks-training.

**Cost:** same as aws-eks-training (~$98/hr per `p5.48xlarge` node).

### coreweave-inference

Deploys the Dynamo inference stack onto a CoreWeave bare-metal H100 cluster
for vLLM model serving. AICR has no first-party CoreWeave overlay yet, so
the example uses the EKS H100 inference recipe with cloud-specific add-ons
skipped.

```bash
cd examples/coreweave-inference-ts
npm install
pulumi config set kubeconfigPath ~/.kube/coreweave
pulumi up
```

**Prerequisites:** a CoreWeave Kubernetes cluster with H100 nodes; kubeconfig
configured locally.

**Cost:** CoreWeave H100s are roughly **$2.49/GPU/hr** (~$19.92/node/hr for
8 GPUs).

### kind-local-dev

Exercise the AICR deployment pipeline against a local [kind](https://kind.sigs.k8s.io/)
cluster — no real GPU hardware required. The `kind` overlay disables driver
installation so most GPU workload pods will not actually run, but the Helm
releases install, which is useful for iterating on the deployment graph.

```bash
cd examples/kind-local-dev-ts
npm install
kind create cluster --name aicr-dev
pulumi up
```

## Conventions

Every example follows the same shape:

- **Pulumi.yaml** — project name, runtime, configurable inputs.
- **index.ts / \_\_main\_\_.py / main.go / Program.cs / App.java / Pulumi.yaml**
  — the program itself.
- **README.md** — what it does, prerequisites, cost, how to run, how to clean
  up.

After `pulumi up`, every example exports the resolved recipe name and the
list of deployed components so you can see what AICR installed:

```
Outputs:
    componentCount     : 11
    deployedComponents : ["cert-manager","gpu-operator","skyhook-operator", ...]
    recipeName         : "h100-eks-ubuntu-training-kubeflow"
    recipeVersion      : "0.1.0"
```
