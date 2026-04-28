# existing-cluster-cs

Deploy NVIDIA AICR onto a Kubernetes cluster you already have, in C#.

The simplest AICR example. Uses your ambient `KUBECONFIG`; the recipe
criteria come from `pulumi config`.

## Prerequisites

- A Kubernetes cluster with NVIDIA GPU nodes reachable via your kubeconfig.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and .NET 8.0+.

## Run

```bash
pulumi config set accelerator h100   # h100 | gb200 | b200
pulumi config set service eks        # aks | eks | gke | kind | oke
pulumi config set intent training    # training | inference
pulumi up
```

## Clean up

```bash
pulumi destroy
```
