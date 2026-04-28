# oci-oke-training-ts

Provision a fresh Oracle Cloud OKE cluster with **NVIDIA GB200 (Grace Blackwell)**
bare-metal GPU nodes and deploy the AICR-validated Kubeflow training stack on top,
in TypeScript.

This is the only example using GB200 accelerators -- the newest and most powerful
NVIDIA data-center GPU architecture.

## What gets created

- VCN (Virtual Cloud Network) with a subnet, internet gateway, and route table
- OKE (Oracle Kubernetes Engine) cluster
- A `BM.GPU.GB200.4` bare-metal GPU node pool (4 GB200 GPUs per node)
- The full AICR `gb200-oke-ubuntu-training-kubeflow` stack
  (Helm releases including GPU Operator, Kubeflow Trainer,
  KAI Scheduler, Kube Prometheus Stack, cert-manager, ...)

## Prerequisites

- **OCI CLI** configured with valid credentials (`~/.oci/config`).
- **Compartment OCID** -- the OCI compartment where resources will be created.
  Find it in the OCI Console under Identity > Compartments.
- **Availability domain** -- the AD where GB200 bare-metal shapes are available
  (e.g., `Uocm:PHX-AD-1`). Check shape availability in the OCI Console under
  Compute > Bare Metal shapes.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Node.js 18+.
- Sufficient OCI service limits for `BM.GPU.GB200.4` shapes in your tenancy.
  You may need to request a limit increase.

## Cost

`BM.GPU.GB200.4` bare-metal instances are **premium-priced** GPU shapes.
Contact OCI sales for current pricing as availability and rates vary by region.
The default node count is 2. Run `pulumi destroy` when finished to avoid
surprise bills.

## Run

```bash
npm install

# Required configuration:
pulumi config set compartmentId ocid1.compartment.oc1..aaaa...
pulumi config set availabilityDomain "Uocm:PHX-AD-1"

# Optional configuration:
# pulumi config set clusterName my-aicr-cluster
# pulumi config set --type int nodeCount 2

pulumi up
```

## Clean up

```bash
pulumi destroy
```
