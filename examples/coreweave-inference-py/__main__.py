"""CoreWeave + NVIDIA AICR H100 Inference Stack

This example deploys the AICR-validated inference stack on a CoreWeave
Kubernetes cluster. CoreWeave provides bare-metal GPU access at
significantly lower cost than hyperscaler equivalents.

Prerequisites:
  - A CoreWeave Kubernetes cluster with H100 GPU nodes
  - kubeconfig configured for the cluster

AICR doesn't ship a dedicated CoreWeave overlay yet, so this example uses
the EKS H100 inference recipe as the closest match (standard GPU operator
config that installs drivers, with cloud-specific add-ons skipped via
``skip_components``).

CoreWeave H100 pricing: ~$2.49/GPU/hr ($19.92/node with 8 GPUs).
"""

import pulumi
import pulumi_labs_nvidia_aicr as aicr

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
    service="eks",  # closest match; cloud-specific add-ons skipped below
    intent="inference",
    platform="dynamo",
    os="ubuntu",
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

# Validate the deployed stack empirically: a snapshot agent captures cluster
# state, then the recipe's deployment and conformance checks run as Jobs in
# the cluster (~10 minutes). With the default strict=False the update always
# succeeds and the verdict is data -- read the exported status and per-check
# results. Set strict=True to fail the update on failed checks instead.
#
# Validation pods tolerate all taints by default, so tainted GPU node pools
# need no configuration; the `tolerations` input exists to narrow that.
validation = aicr.ValidationRun("nvidia-inference-validation",
    criteria=inference_stack.criteria,     # same recipe as the stack -- no drift
    recipe_data_version=inference_stack.recipe_version,  # assert same recipe data as deployed
    kubeconfig_path=kubeconfig_path,       # same kubeconfig the stack uses
    triggers=[inference_stack.deployed_components],  # re-validate when the stack changes
)

# Exports
pulumi.export("recipe_name", inference_stack.recipe_name)
pulumi.export("deployed_components", inference_stack.deployed_components)
pulumi.export("component_count", inference_stack.component_count)
pulumi.export("validation_status", validation.status)
pulumi.export("validation_checks", validation.phase_results)
