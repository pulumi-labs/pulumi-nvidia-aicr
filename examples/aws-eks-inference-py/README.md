# aws-eks-inference-py

Provision a fresh AWS EKS cluster with H100 GPU nodes and deploy the
AICR-validated vLLM inference stack with NIM on top, in Python.

See [aws-eks-inference-ts/README.md](../aws-eks-inference-ts/README.md) for the
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
