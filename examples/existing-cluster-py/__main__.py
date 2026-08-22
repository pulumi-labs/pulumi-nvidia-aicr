"""AICR Quickstart -- Deploy on an Existing Kubernetes Cluster

The simplest way to deploy NVIDIA AICR. Uses your ambient kubeconfig
(~/.kube/config) and configurable criteria via `pulumi config`.

Usage:
  pulumi config set accelerator h100
  pulumi config set service eks
  pulumi config set intent training
  pulumi up
"""

import pulumi
import pulumi_labs_nvidia_aicr as aicr

config = pulumi.Config()

gpu_stack = aicr.ClusterStack("aicr",
    # Uses ambient kubeconfig when no kubeconfig/kubeconfig_path is set
    accelerator=config.require("accelerator"),
    service=config.require("service"),
    intent=config.require("intent"),
    platform=config.get("platform"),          # optional
    os=config.get("os"),                      # optional; unset = OS-agnostic recipe
    skip_await=config.get_bool("skipAwait") or False,
)

# Validate the deployed stack empirically: a snapshot agent captures cluster
# state, then the recipe's deployment and conformance checks run as Jobs in
# the cluster (~10 minutes). With the default strict=False the update always
# succeeds and the verdict is data -- read the exported status and per-check
# results. Set strict=True to fail the update on failed checks instead.
#
# Validation pods tolerate all taints by default, so tainted GPU node groups
# need no configuration; the `tolerations` input exists to narrow that.
validation = aicr.ValidationRun("aicr-validation",
    # Wiring the stack's criteria output keeps a single source of truth --
    # whatever `pulumi config` resolved to is exactly what gets validated.
    criteria=gpu_stack.criteria,
    recipe_data_version=gpu_stack.recipe_version,      # assert same recipe data as deployed
    # No kubeconfig set: uses the same ambient kubeconfig as the stack.
    triggers=[gpu_stack.deployed_components],  # re-validate when the stack changes
)

pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("recipe_version", gpu_stack.recipe_version)
pulumi.export("deployed_components", gpu_stack.deployed_components)
pulumi.export("component_count", gpu_stack.component_count)
pulumi.export("validation_status", validation.status)
pulumi.export("validation_checks", validation.phase_results)
