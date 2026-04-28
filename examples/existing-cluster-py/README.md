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

## Clean up

```bash
pulumi destroy
```

## Outputs

| Output | Description |
|---|---|
| `recipe_name` | Resolved AICR recipe identifier. |
| `recipe_version` | AICR recipe data version. |
| `deployed_components` | Names of deployed components, in topological order. |
| `component_count` | Number of components deployed. |
