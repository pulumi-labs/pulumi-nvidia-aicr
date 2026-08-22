# azure-aks-training-py

Provision a fresh Azure AKS cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in Python.

See [azure-aks-training-ts/README.md](../azure-aks-training-ts/README.md) for the
full description, prerequisites, and cost breakdown -- the program is the
same, only the language differs.

## Run

```bash
python3 -m venv venv && source venv/bin/activate
pip install -r requirements.txt
pulumi up
```

## Validation

The example ends with a `ValidationRun` that snapshots the cluster and runs
the recipe's deployment and conformance checks in-cluster (~10 minutes,
non-strict: the update succeeds and the verdict lands in the
`validation_status` / `validation_checks` outputs). It re-runs whenever the
deployed component set changes. Requires provider/SDK >= 0.3.0.

## Clean up

```bash
pulumi destroy
```
