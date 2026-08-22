# gcp-gke-training-py

Provision a fresh GCP GKE cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in Python.

See [gcp-gke-training-ts/README.md](../gcp-gke-training-ts/README.md) for the
full description, prerequisites, and cost breakdown -- the program is the
same, only the language differs.

## Run

```bash
python3 -m venv venv && source venv/bin/activate
pip install -r requirements.txt
pulumi up
```

### Single Spot H100 (budget variant)

The default 2x a3-highgpu-8g layout costs ~$60/hr. With only 1 GPU of
preemptible quota (`PREEMPTIBLE_NVIDIA_H100_GPUS` plus the global
`GPUS_ALL_REGIONS` cap) you can run the whole stack — and a real H100
validation — on one Spot node:

```bash
pulumi config set machineType a3-highgpu-1g
pulumi config set gpuCount 1
pulumi config set nodeCount 1
pulumi config set spot true
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
