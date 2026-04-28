# gcp-gke-training-ts

Provision a fresh GCP GKE cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in TypeScript.

## What gets created

- A GKE cluster with the default node pool removed
- An `a3-highgpu-8g` GPU node pool (8x H100 80GB per node)
- The full AICR `h100-gke-ubuntu-training-kubeflow` stack
  (~10 Helm releases including GPU Operator, Kubeflow Trainer,
  KAI Scheduler, Kube Prometheus Stack, cert-manager, ...)

## Prerequisites

- GCP credentials configured (`gcloud auth application-default login`).
- The `gke-gcloud-auth-plugin` installed for kubeconfig exec-based auth:
  ```bash
  gcloud components install gke-gcloud-auth-plugin
  ```
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Node.js 18+.
- A GCP project with the GKE API enabled and sufficient GPU quota for
  `a3-highgpu-8g` instances in your target zone.

## Cost

`a3-highgpu-8g` instances are roughly **$30/hr each** (8x NVIDIA H100 80GB).
The default node count is 2, so plan on **~$60/hr** while the cluster is up.
Run `pulumi destroy` when finished to avoid surprise bills.

## Run

```bash
npm install
# Optional configuration:
# pulumi config set clusterName my-aicr-cluster
# pulumi config set --type int nodeCount 2
# pulumi config set gcp:zone us-central1-a
pulumi up
```

## Clean up

```bash
pulumi destroy
```
