"""CoreWeave + NVIDIA AICR H100 Inference Stack

This example deploys the AICR-validated inference stack on a CoreWeave
Kubernetes cluster. CoreWeave provides bare-metal GPU access at
significantly lower cost than hyperscaler equivalents.

Prerequisites:
  - A CoreWeave Kubernetes cluster with H100 GPU nodes
  - kubeconfig configured for the cluster

The "self-managed" service type uses the base AICR recipe without
cloud-specific operators (no aws-efa, aws-ebs-csi-driver, etc.),
which is appropriate for CoreWeave's bare-metal environment.

CoreWeave H100 pricing: ~$2.49/GPU/hr ($19.92/node with 8 GPUs)
"""

import pulumi
import pulumi_nvidia_aicr as aicr

config = pulumi.Config()
kubeconfig_path = config.get("kubeconfigPath") or "~/.kube/config"

# Deploy NVIDIA AICR inference stack
# Components include:
#   - NVIDIA GPU Operator (driver management, device plugin)
#   - Dynamo Platform (NVIDIA's inference serving framework)
#   - KGateway (API gateway for inference endpoints)
#   - KAI Scheduler (GPU-aware scheduling)
#   - Monitoring stack (Prometheus, Grafana, DCGM metrics)
#   - cert-manager, NVSentinel, and more
inference_stack = aicr.ClusterStack("nvidia-inference",
    kubeconfig_path=kubeconfig_path,
    accelerator="h100",
    service="self-managed",   # CoreWeave = self-managed K8s
    intent="inference",
    platform="dynamo",
    # Skip cloud-specific components not needed on CoreWeave
    skip_components=[
        "aws-efa",
        "aws-ebs-csi-driver",
    ],
    # Customize Dynamo platform for CoreWeave's storage
    component_overrides={
        "dynamo-platform": aicr.ComponentOverrideArgs(
            values={
                "etcd": {
                    "persistence": {
                        "storageClass": "coreweave-ssd",
                    },
                },
                "nats": {
                    "config": {
                        "jetstream": {
                            "fileStore": {
                                "pvc": {
                                    "storageClassName": "coreweave-ssd",
                                },
                            },
                        },
                    },
                },
            },
        ),
    },
)

# Exports
pulumi.export("recipe_name", inference_stack.recipe_name)
pulumi.export("deployed_components", inference_stack.deployed_components)
pulumi.export("component_count", inference_stack.component_count)
