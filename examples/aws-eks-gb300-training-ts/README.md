# aws-eks-gb300-training-ts

Provision a fresh AWS EKS cluster with a GB300 NVL72 node group and deploy the
AICR-validated Kubeflow training stack on top, in TypeScript.

## What gets created

- VPC with two public subnets (the GPU zone plus a second zone for the
  control plane)
- EKS cluster on Kubernetes 1.34 with an OIDC provider; the default node
  group is skipped
- A managed node group of `p6e-gb300r.36xlarge` instances (4× GB300 per node,
  Grace ARM64 host, one EFA interface) pinned to an EC2 Capacity Block via a
  launch template
- The full AICR `gb300-eks-ubuntu-training-kubeflow` stack (~15 Helm
  releases including GPU Operator with GB300 pre-manifests, the NVIDIA DRA
  driver, EFA device plugin, Kubeflow Trainer, KAI Scheduler, NVSentinel,
  Kube Prometheus Stack, cert-manager, ...)

## Prerequisites

- AWS credentials with permission to create EKS clusters, VPCs, EC2
  instances, launch templates, IAM roles, and OIDC providers.
- An active [EC2 Capacity Block for ML](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-capacity-blocks.html)
  reservation for `p6e-gb300r.36xlarge`. Note its ID and availability zone.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Node.js 18+.

## Cost

Capacity Blocks are billed for the whole reservation window, not per running
instance-hour, so the reservation itself is the dominant cost. Consult the
EC2 Capacity Blocks pricing page for the current GB300 rate. The EKS control
plane and networking add a small hourly charge; run `pulumi destroy` when
finished.

## Run

```bash
npm install
pulumi config set capacityReservationId cr-0123456789abcdef0
# Optional configuration:
# pulumi config set gpuAvailabilityZone us-east-1a     # must match the reservation
# pulumi config set secondAvailabilityZone us-east-1b
# pulumi config set clusterName my-gb300-cluster
# pulumi config set --type int nodeCount 2             # must fit the reservation
pulumi up
```

## Notes on the GB300 shape

- The recipe floors Kubernetes at 1.34 because GB300 NVLS validation uses
  the GA `resource.k8s.io/v1` DRA API for IMEX ComputeDomains.
- `p6e-gb300r.36xlarge` exposes a single usable EFA interface; the launch
  template enables it on the primary network interface, which is the layout
  the recipe's EFA component expects.
- The node group uses the `AL2023_ARM_64_NVIDIA` AMI type because the Grace
  host is ARM64.

## Validation

The example ends with a `ValidationRun` that snapshots the cluster and runs
the recipe's deployment and conformance checks in-cluster (non-strict: the
update succeeds and the verdict lands in the `validationStatus` /
`validationChecks` outputs). It re-runs whenever the deployed component set
changes. Requires provider/SDK >= 0.4.0 (the first release with GB300
recipes).

## Clean up

```bash
pulumi destroy
```

Destroying the stack releases the instances but not the Capacity Block
reservation itself.
