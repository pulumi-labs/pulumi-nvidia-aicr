"""AICR on a local kind cluster -- for development of the deployment pipeline
without real GPU hardware.

Prerequisites:
  - kind installed and a cluster running:
      kind create cluster --name aicr-dev
  - kubectl context pointing at it (kind sets this automatically)

What it does:
  The ``kind`` overlay disables driver installation (assumes the host has
  pre-installed NVIDIA drivers via nvkind, if any) and several other
  GPU-Operator subcomponents that would otherwise hang in a kind cluster.
  Many GPU pods will not actually be Ready, but the Helm releases will
  install -- which is enough for iterating on the deployment graph.
"""

import pulumi
import pulumi_nvidia_aicr as aicr

config = pulumi.Config()
intent = config.get("intent") or "inference"

stack = aicr.ClusterStack("kind-aicr",
    accelerator="h100",
    service="kind",
    intent=intent,
    skip_await=True,  # kind clusters often can't satisfy GPU readiness; don't block
    skip_components=[
        "kube-prometheus-stack",
    ],
)

pulumi.export("recipeName", stack.recipe_name)
pulumi.export("recipeVersion", stack.recipe_version)
pulumi.export("deployedComponents", stack.deployed_components)
pulumi.export("componentCount", stack.component_count)
