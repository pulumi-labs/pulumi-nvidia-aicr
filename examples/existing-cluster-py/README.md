# existing-cluster-py

Deploy NVIDIA AICR onto a Kubernetes cluster you already have, in Python.

The simplest AICR example. Uses your ambient `KUBECONFIG`; the recipe
criteria come from `pulumi config`.

## Prerequisites

- A Kubernetes cluster with NVIDIA GPU nodes reachable via your kubeconfig.
- [Pulumi CLI](https://www.pulumi.com/docs/install/) and Python 3.9+.

## Run

```bash
python3 -m venv venv && source venv/bin/activate
pip install -r requirements.txt
pulumi config set accelerator h100   # h100 | gb200 | b200
pulumi config set service eks        # aks | eks | gke | kind | oke
pulumi config set intent training    # training | inference
# Optional:
# pulumi config set platform kubeflow  # kubeflow | dynamo | nim
# pulumi config set os ubuntu          # ubuntu | cos
pulumi up
```

## Validation

The example ends with a `ValidationRun` that snapshots the cluster and runs
the recipe's deployment and conformance checks in-cluster (~10 minutes,
non-strict: the update succeeds and the verdict lands in the
`validation_status` / `validation_checks` outputs). It validates the same
criteria the stack resolved from `pulumi config` and uses the same ambient
kubeconfig, and re-runs whenever the deployed component set changes.
Requires provider/SDK >= 0.3.0.

## Clean up

```bash
pulumi destroy
```

> **Note:** `pulumi destroy` uninstalls the Helm releases but leaves their
> CRDs on the cluster (Helm never removes CRDs on uninstall). A later
> `pulumi up` onto the same cluster fails with `invalid ownership metadata`;
> delete the leftover CRDs first. See "Known Limitations" in the main README.

## Outputs

| Output | Description |
|---|---|
| `recipe_name` | Resolved AICR recipe identifier. |
| `recipe_version` | AICR recipe data version. |
| `deployed_components` | Names of deployed components, in topological order. |
| `component_count` | Number of components deployed. |
| `validation_status` | Rollup validation verdict: `passed`, `failed`, or `readiness-failed`. |
| `validation_checks` | Per-check validation results across all phases run. |
