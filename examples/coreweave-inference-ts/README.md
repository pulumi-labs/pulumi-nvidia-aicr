# coreweave-inference-ts

Deploy the NVIDIA AICR Dynamo inference stack onto a CoreWeave bare-metal
H100 cluster, in TypeScript.

## Notes on the recipe choice

AICR doesn't ship a dedicated CoreWeave overlay yet, so this example uses
the `h100-eks-ubuntu-inference-dynamo` recipe (set via `service: "eks"`)
because it has the standard GPU operator config that installs drivers from
scratch. The cloud-specific add-ons (`aws-efa`, `aws-ebs-csi-driver`) are
filtered out via `skipComponents`, and the Dynamo platform's storage class
is overridden to `coreweave-ssd`.

## Components deployed

GPU Operator, Dynamo Platform, KGateway, KAI Scheduler, the Kube Prometheus
monitoring stack, cert-manager, NVSentinel, and a few more — see the
`deployedComponents` output for the exact list resolved at runtime.

## Prerequisites

- A CoreWeave Kubernetes cluster with H100 GPU nodes.
- A kubeconfig file pointing at that cluster.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Node.js 18+.

## Cost

CoreWeave H100s are roughly **$2.49/GPU/hr** (~$19.92/node/hr for 8 GPUs).
Run `pulumi destroy` when finished.

## Run

```bash
npm install
# Optional: point at a specific kubeconfig
# pulumi config set kubeconfigPath ~/.kube/coreweave
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
