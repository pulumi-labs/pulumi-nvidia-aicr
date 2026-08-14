# azure-aks-training-cs

Provision a fresh Azure AKS cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in C#.

See [azure-aks-training-ts/README.md](../azure-aks-training-ts/README.md) for the
full description, prerequisites, and cost breakdown.

## Run

```bash
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
