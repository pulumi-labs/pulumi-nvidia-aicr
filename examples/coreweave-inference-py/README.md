# coreweave-inference-py

Deploy the NVIDIA AICR Dynamo inference stack onto a CoreWeave bare-metal
H100 cluster, in Python.

See [coreweave-inference-ts/README.md](../coreweave-inference-ts/README.md)
for the full description, recipe-choice notes, prerequisites, and cost.

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
