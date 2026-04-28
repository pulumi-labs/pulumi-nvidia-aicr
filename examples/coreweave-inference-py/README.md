# coreweave-inference-py

Deploy the NVIDIA AICR Dynamo inference stack onto a CoreWeave bare-metal
H100 cluster, in Python.

See [coreweave-inference-ts/README.md](../coreweave-inference-ts/README.md)
for the full description, recipe-choice notes, prerequisites, and cost.

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
