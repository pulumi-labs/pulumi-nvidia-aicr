# gcp-gke-training-py

Provision a fresh GCP GKE cluster with H100 GPU nodes and deploy the
AICR-validated Kubeflow training stack on top, in Python.

See [gcp-gke-training-ts/README.md](../gcp-gke-training-ts/README.md) for the
full description, prerequisites, and cost breakdown -- the program is the
same, only the language differs.

## Run

```bash
python3 -m venv venv && source venv/bin/activate
pip install -r requirements.txt
pulumi up
```

## Clean up

```bash
pulumi destroy
```
