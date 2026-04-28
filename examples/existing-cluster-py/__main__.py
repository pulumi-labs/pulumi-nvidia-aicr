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
import pulumi_nvidia_aicr as aicr

config = pulumi.Config()

gpu_stack = aicr.ClusterStack("aicr",
    # Uses ambient kubeconfig when no kubeconfig/kubeconfig_path is set
    accelerator=config.require("accelerator"),
    service=config.require("service"),
    intent=config.require("intent"),
    platform=config.get("platform"),          # optional
    os=config.get("os"),                      # optional, defaults to ubuntu
    skip_await=config.get_bool("skipAwait") or False,
)

pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("recipe_version", gpu_stack.recipe_version)
pulumi.export("deployed_components", gpu_stack.deployed_components)
pulumi.export("component_count", gpu_stack.component_count)
