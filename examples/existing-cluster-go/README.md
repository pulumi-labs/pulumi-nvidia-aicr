# existing-cluster-go

Deploy NVIDIA AICR onto a Kubernetes cluster you already have, in Go.

The simplest AICR example. Uses your ambient `KUBECONFIG`; the recipe
criteria come from `pulumi config`.

## Prerequisites

- A Kubernetes cluster with NVIDIA GPU nodes reachable via your kubeconfig.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Go 1.24+.

## Run

```bash
pulumi config set accelerator h100   # h100 | gb200 | b200
pulumi config set service eks        # aks | eks | gke | kind | oke
pulumi config set intent training    # training | inference
# Optional:
# pulumi config set platform kubeflow  # kubeflow | dynamo | nim
# pulumi config set os ubuntu          # ubuntu | cos
pulumi up
```

## Validation

The example ends with a `ValidationRun` that snapshots the cluster and runs
the recipe's deployment and conformance checks in-cluster (~10 minutes,
non-strict: the update succeeds and the verdict lands in the
`validationStatus` / `validationChecks` outputs). It re-runs whenever the
deployed component set changes. Requires provider/SDK >= 0.3.0.

## Clean up

```bash
pulumi destroy
```

> **Note:** `pulumi destroy` uninstalls the Helm releases but leaves their
> CRDs on the cluster (Helm never removes CRDs on uninstall). A later
> `pulumi up` onto the same cluster fails with `invalid ownership metadata`;
> delete the leftover CRDs first. See "Known Limitations" in the main README.

## Outputs

| Output | Description |
|---|---|
| `recipeName` | Resolved AICR recipe identifier. |
| `recipeVersion` | AICR recipe data version. |
| `deployedComponents` | Names of deployed components, in topological order. |
| `componentCount` | Number of components deployed. |
| `validationStatus` | Validation verdict: `passed`, `failed`, or `readiness-failed`. |
| `validationChecks` | Per-check validation results across all phases run. |
