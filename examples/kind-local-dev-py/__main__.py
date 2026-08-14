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
import pulumi_labs_nvidia_aicr as aicr

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

# Optional: run the recipe's empirical validation against the cluster
# (config: `pulumi config set validate true`). Adds ~10 minutes to the update.
#
# Honest expectations on a GPU-less kind cluster: the readiness pre-flight
# passes (kind recipes bind no OS or GPU constraints), but deployment health
# checks for GPU components fail or skip, and GPU conformance checks skip --
# there is no GPU to validate. Useful for exercising the validation pipeline
# itself; a real verdict needs real hardware (see the EKS/GKE examples).
if config.get_bool("validate"):
    validation = aicr.ValidationRun("kind-validation",
        criteria=stack.criteria,            # same recipe as the stack
        recipe_data_version=stack.recipe_version,       # assert same recipe data as deployed
        require_gpu=False,                  # kind has no GPU nodes
        triggers=[stack.deployed_components],  # re-run when the stack changes
    )
    pulumi.export("validationStatus", validation.status)
    pulumi.export("validationChecks", validation.phase_results)
