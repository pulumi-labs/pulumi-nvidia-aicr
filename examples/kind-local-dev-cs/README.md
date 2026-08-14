# kind-local-dev-cs

Exercise the NVIDIA AICR deployment pipeline against a local
[kind](https://kind.sigs.k8s.io/) cluster -- no real GPU hardware required.

The `kind` overlay disables driver installation and several other
GPU-Operator subcomponents that would otherwise hang in kind. Many GPU pods
will not actually become Ready, but the Helm releases install -- which is
enough for iterating on the AICR deployment graph.

This is **for AICR pipeline development**, not for running real GPU
workloads.

## Prerequisites

- [Pulumi CLI](https://www.pulumi.com/docs/install/) and .NET 8.0+.
- [kind](https://kind.sigs.k8s.io/) installed.
- A running kind cluster (kind sets the kubectl context automatically):

  ```bash
  kind create cluster --name aicr-dev
  ```

## Run

```bash
# Optional:
# pulumi config set intent training   # default: inference
# pulumi config set validate true     # run empirical validation (see below)
pulumi up
```

## Validation

With `validate: true` a `ValidationRun` snapshots the cluster and runs the
recipe's deployment and conformance checks in-cluster (~10 minutes, needs
provider/SDK >= 0.3.0). On a GPU-less kind cluster expect an honest result:
readiness passes, GPU deployment checks fail or skip, GPU conformance checks
skip. Useful for exercising the validation pipeline itself — a real verdict
needs real hardware (see the EKS/GKE examples).

## Clean up

```bash
pulumi destroy
kind delete cluster --name aicr-dev
```

> **Note:** if you keep the cluster (skipping `kind delete cluster`) after a
> `pulumi destroy`, a later `pulumi up` fails on leftover CRDs
> (`invalid ownership metadata`) -- Helm never removes CRDs on uninstall.
> Recreate the kind cluster instead; see "Known Limitations" in the main README.
