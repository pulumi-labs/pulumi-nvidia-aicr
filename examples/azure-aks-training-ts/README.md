# azure-aks-training-ts

Provision a fresh Azure AKS cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in TypeScript.

## What gets created

- Azure Resource Group
- AKS cluster with a system node pool (`Standard_D4s_v3`)
  and a GPU node pool (`Standard_ND96isr_H100_v5`, 8x H100 80GB per node)
- The full AICR `h100-aks-ubuntu-training-kubeflow` stack
  (~11 Helm releases including GPU Operator, Kubeflow Trainer,
  KAI Scheduler, Kube Prometheus Stack, cert-manager, ...)

## Prerequisites

- Azure CLI configured with credentials that have permission to create
  AKS clusters, resource groups, and VM scale sets.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Node.js 18+.

## Cost

`Standard_ND96isr_H100_v5` VMs are roughly **~$40/hr each**. The default
GPU node count is 2, so plan on **~$80/hr** while the cluster is up. Run
`pulumi destroy` when finished to avoid surprise bills.

## Run

```bash
npm install
# Optional configuration:
# pulumi config set clusterName my-aicr-cluster
# pulumi config set --type int nodeCount 2
pulumi up
```

## Clean up

```bash
pulumi destroy
```
