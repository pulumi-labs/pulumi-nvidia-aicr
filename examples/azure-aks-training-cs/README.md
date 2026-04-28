# azure-aks-training-cs

Provision a fresh Azure AKS cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in C#.

See [azure-aks-training-ts/README.md](../azure-aks-training-ts/README.md) for the
full description, prerequisites, and cost breakdown.

## Run

```bash
pulumi up
```

## Clean up

```bash
pulumi destroy
```
