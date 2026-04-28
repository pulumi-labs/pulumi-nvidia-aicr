# aws-eks-training-go

Provision a fresh AWS EKS cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in Go.

See [aws-eks-training-ts/README.md](../aws-eks-training-ts/README.md) for the
full description, prerequisites, and cost breakdown.

## Run

```bash
pulumi up
```

## Clean up

```bash
pulumi destroy
```
