# coreweave-inference-java

Deploy the NVIDIA AICR Dynamo inference stack onto a CoreWeave bare-metal
H100 cluster, in Java.

See [coreweave-inference-ts/README.md](../coreweave-inference-ts/README.md)
for the full description, recipe-choice notes, prerequisites, and cost.

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
