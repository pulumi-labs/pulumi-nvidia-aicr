# aws-eks-inference-cs

Provision a fresh AWS EKS cluster with H100 GPU nodes and deploy the
AICR-validated vLLM inference stack with NIM on top, in C#.

See [aws-eks-inference-ts/README.md](../aws-eks-inference-ts/README.md) for the
full description, prerequisites, and cost breakdown.

## Run

```bash
pulumi up
```

## Clean up

```bash
pulumi destroy
```
