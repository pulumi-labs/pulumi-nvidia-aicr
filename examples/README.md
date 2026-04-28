# Examples

Three end-to-end scenarios, each available in five languages (TypeScript,
Python, Go, C#, Java) and YAML where applicable.

## Pick a scenario

| Scenario | What it does | Best for |
|---|---|---|
| **[existing-cluster](#existing-cluster)** | Deploy AICR onto a Kubernetes cluster you already have. Uses ambient `kubeconfig` and `pulumi config` for criteria. | Trying AICR for the first time; existing GPU clusters. |
| **[aws-eks-training](#aws-eks-training)** | Provision a fresh EKS cluster with H100 nodes, then deploy the AICR training stack. | New AWS GPU clusters; Kubeflow training workloads. |
| **[coreweave-inference](#coreweave-inference)** | Deploy the AICR Dynamo inference stack onto a CoreWeave bare-metal H100 cluster. | Cost-optimized inference on bare metal. |

## Pick a language

| Scenario | TypeScript | Python | Go | C# | Java | YAML |
|---|---|---|---|---|---|---|
| existing-cluster | [-ts](./existing-cluster-ts/) | [-py](./existing-cluster-py/) | [-go](./existing-cluster-go/) | [-cs](./existing-cluster-cs/) | [-java](./existing-cluster-java/) | [-yaml](./existing-cluster-yaml/) |
| aws-eks-training | [-ts](./aws-eks-training-ts/) | [-py](./aws-eks-training-py/) | [-go](./aws-eks-training-go/) | [-cs](./aws-eks-training-cs/) | [-java](./aws-eks-training-java/) | -- |
| coreweave-inference | [-ts](./coreweave-inference-ts/) | [-py](./coreweave-inference-py/) | [-go](./coreweave-inference-go/) | [-cs](./coreweave-inference-cs/) | [-java](./coreweave-inference-java/) | -- |
| kind-local-dev | [-ts](./kind-local-dev-ts/) | -- | -- | -- | -- | -- |

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

Provisions a complete training environment from scratch: VPC, EKS cluster,
H100 GPU node group, then the full AICR Kubeflow training stack on top.

```bash
cd examples/aws-eks-training-ts
npm install
pulumi up
```

**Prerequisites:** AWS credentials with permissions to create EKS clusters,
VPCs, and EC2 instances.

**Cost:** `p5.48xlarge` instances are roughly **$98.32/hr each**. The default
node count is 2, so plan on **~$196/hr** while the cluster is up. Run
`pulumi destroy` when you are finished.

### coreweave-inference

Deploys the Dynamo inference stack onto a CoreWeave bare-metal H100 cluster.
AICR has no first-party CoreWeave overlay yet, so the example uses the EKS
H100 inference recipe with cloud-specific add-ons skipped.

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
installation (relying on the host's pre-installed drivers, if any) so most
GPU workload pods will not actually run, but the Helm releases install,
which is useful for iterating on the deployment graph.

```bash
cd examples/kind-local-dev-ts
npm install
kind create cluster --name aicr-dev
pulumi up
```

## Conventions

Every example follows the same shape:

- **Pulumi.yaml** — project name, runtime, configurable inputs.
- **index.ts / __main__.py / main.go / Program.cs / src/main/java/.../App.java / Pulumi.yaml**
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
