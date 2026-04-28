# existing-cluster-ts

Deploy NVIDIA AICR onto a Kubernetes cluster you already have, in TypeScript.

The simplest AICR example. Uses your ambient `KUBECONFIG`; the recipe
criteria come from `pulumi config`.

## Prerequisites

- A Kubernetes cluster with NVIDIA GPU nodes reachable via your kubeconfig.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Node.js 18+.

## Run

```bash
npm install
pulumi config set accelerator h100   # h100 | gb200 | b200
pulumi config set service eks        # aks | eks | gke | kind | oke
pulumi config set intent training    # training | inference
# Optional:
# pulumi config set platform kubeflow  # kubeflow | dynamo | nim
# pulumi config set os ubuntu          # ubuntu | cos
pulumi up
```

## Clean up

```bash
pulumi destroy
```

## Outputs

| Output | Description |
|---|---|
| `recipeName` | Resolved AICR recipe identifier (e.g. `h100-eks-ubuntu-training-kubeflow`). |
| `recipeVersion` | AICR recipe data version. |
| `deployedComponents` | Names of deployed components, in topological order. |
| `componentCount` | Number of components deployed. |
