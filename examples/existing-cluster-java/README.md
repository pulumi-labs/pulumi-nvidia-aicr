# existing-cluster-java

Deploy NVIDIA AICR onto a Kubernetes cluster you already have, in Java.

The simplest AICR example. Uses your ambient `KUBECONFIG`; the recipe
criteria come from `pulumi config`.

## Prerequisites

- A Kubernetes cluster with NVIDIA GPU nodes reachable via your kubeconfig.
- [Pulumi CLI](https://www.pulumi.com/docs/install/), JDK 11+, Maven 3.6+.

## Run

```bash
pulumi config set accelerator h100   # h100 | gb200 | b200
pulumi config set service eks        # aks | eks | gke | kind | oke
pulumi config set intent training    # training | inference
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

> **Note:** `pulumi destroy` uninstalls the Helm releases but leaves their
> CRDs on the cluster (Helm never removes CRDs on uninstall). A later
> `pulumi up` onto the same cluster fails with `invalid ownership metadata`;
> delete the leftover CRDs first. See "Known Limitations" in the main README.
