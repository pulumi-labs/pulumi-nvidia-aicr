# oci-oke-training-py

Provision a fresh Oracle Cloud OKE cluster with **NVIDIA GB200 (Grace Blackwell)**
bare-metal GPU nodes and deploy the AICR-validated Kubeflow training stack on top,
in Python.

See [oci-oke-training-ts/README.md](../oci-oke-training-ts/README.md) for the
full description, prerequisites, and cost breakdown.

## Run

```bash
pip install -r requirements.txt

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
