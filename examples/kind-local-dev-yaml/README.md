# kind-local-dev-yaml

Exercise the NVIDIA AICR deployment pipeline against a local
[kind](https://kind.sigs.k8s.io/) cluster -- no real GPU hardware required.

The `kind` overlay disables driver installation and several other
GPU-Operator subcomponents that would otherwise hang in kind. Many GPU pods
will not actually become Ready, but the Helm releases install -- which is
enough for iterating on the AICR deployment graph.

This is **for AICR pipeline development**, not for running real GPU
workloads.

## Prerequisites

- [Pulumi CLI](https://www.pulumi.com/docs/install/).
- [kind](https://kind.sigs.k8s.io/) installed.
- A running kind cluster (kind sets the kubectl context automatically):

  ```bash
  kind create cluster --name aicr-dev
  ```

## Run

```bash
# Optional:
# pulumi config set intent training   # default: inference
pulumi up
```

## Clean up

```bash
pulumi destroy
kind delete cluster --name aicr-dev
```
