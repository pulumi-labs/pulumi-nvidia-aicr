# gcp-gke-training-go

Provision a fresh GCP GKE cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in Go.

See [gcp-gke-training-ts/README.md](../gcp-gke-training-ts/README.md) for the
full description, prerequisites, and cost breakdown.

## Run

```bash
pulumi up
```

## Clean up

```bash
pulumi destroy
```
