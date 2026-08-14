# aws-eks-training-ts

Provision a fresh AWS EKS cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in TypeScript.

## What gets created

- VPC with two public subnets (us-east-1a, us-east-1b)
- EKS cluster with an OIDC provider
- A `p5.48xlarge` GPU node group (8× H100 80GB per node)
- The full AICR `h100-eks-ubuntu-training-kubeflow` stack
  (~11 Helm releases including GPU Operator, Kubeflow Trainer,
  KAI Scheduler, Kube Prometheus Stack, cert-manager, ...)

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
