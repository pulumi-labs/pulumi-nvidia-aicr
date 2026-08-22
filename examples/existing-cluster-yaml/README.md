# existing-cluster-yaml

Deploy NVIDIA AICR onto a Kubernetes cluster you already have, in pure YAML.

The minimal AICR example. Uses your ambient `KUBECONFIG`; the recipe
criteria come from `pulumi config`.

## Prerequisites

- A Kubernetes cluster with NVIDIA GPU nodes reachable via your kubeconfig.
- [Pulumi CLI](https://www.pulumi.com/docs/install/).

## Run

```bash
pulumi config set accelerator h100   # h100 | gb200 | b200
pulumi config set service eks        # aks | eks | gke | kind | oke
pulumi config set intent training    # training | inference
# Optional:
# pulumi config set platform kubeflow  # kubeflow | dynamo | nim
pulumi up
```

## Validation

Uncomment the `aicr-validation` block (and its outputs) in `Pulumi.yaml` to
run a `ValidationRun` that snapshots the cluster and runs the recipe's
deployment and conformance checks in-cluster (~10 minutes, non-strict: the
update succeeds and the verdict lands in the `validationStatus` /
`validationChecks` outputs, needs provider >= 0.3.0). It validates the same
criteria the stack resolved from `pulumi config`, uses the same ambient
kubeconfig, and re-runs whenever the deployed component set changes.

## Clean up

```bash
pulumi destroy
```

> **Note:** `pulumi destroy` uninstalls the Helm releases but leaves their
> CRDs on the cluster (Helm never removes CRDs on uninstall). A later
> `pulumi up` onto the same cluster fails with `invalid ownership metadata`;
> delete the leftover CRDs first. See "Known Limitations" in the main README.
