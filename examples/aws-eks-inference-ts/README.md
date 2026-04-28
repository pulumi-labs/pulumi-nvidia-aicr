# aws-eks-inference-ts

Provision a fresh AWS EKS cluster with H100 GPU nodes and deploy the
AICR-validated vLLM inference stack with NIM on top, in TypeScript.

## What gets created

- VPC with two public subnets (us-east-1a, us-east-1b)
- EKS cluster with an OIDC provider
- A `p5.48xlarge` GPU node group (8x H100 80GB per node)
- The full AICR `h100-eks-ubuntu-inference-nim` stack
  (Helm releases including GPU Operator, NIM Operator, KGateway,
  KAI Scheduler, Kube Prometheus Stack, cert-manager, ...)

The NIM (NVIDIA Inference Microservices) platform provides optimized
vLLM-based model serving. The stack includes KGateway for GPU-aware
ingress routing and the NIM Operator for managing model deployment
custom resources.

## Prerequisites

- AWS credentials with permission to create EKS clusters, VPCs, EC2 instances,
  IAM roles, and OIDC providers.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Node.js 18+.

## Cost

`p5.48xlarge` instances are roughly **$98.32/hr each**. The default node
count is 2, so plan on **~$196/hr** while the cluster is up. Run
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
