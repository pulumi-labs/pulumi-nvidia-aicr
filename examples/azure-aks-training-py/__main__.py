"""Azure AKS + NVIDIA AICR H100 Training Stack

This example creates a complete GPU training environment:
  1. An AKS cluster with H100 GPU worker nodes
  2. The full AICR-validated software stack for distributed training
     including GPU Operator, Kubeflow Trainer, monitoring, and more.

COST WARNING: Standard_ND96isr_H100_v5 VMs cost approximately $40/hr each.
This example provisions 2 GPU nodes (~$80/hr). Remember to run
`pulumi destroy` when finished to avoid unexpected charges.
"""

import base64

import pulumi
import pulumi_azure_native as azure
import pulumi_nvidia_aicr as aicr

config = pulumi.Config()
cluster_name = config.get("clusterName") or "aicr-training"
node_count = config.get_int("nodeCount") or 2

# Create a resource group for all resources
resource_group = azure.resources.ResourceGroup("gpu-rg",
    resource_group_name=f"{cluster_name}-rg",
    tags={
        "nvidia.com/aicr": "true",
        "nvidia.com/gpu": "h100",
    },
)

# Create the AKS cluster with a system node pool and a GPU node pool
cluster = azure.containerservice.ManagedCluster(cluster_name,
    resource_group_name=resource_group.name,
    resource_name_=cluster_name,
    dns_prefix=cluster_name,
    kubernetes_version="1.30",
    identity=azure.containerservice.ManagedClusterIdentityArgs(
        type=azure.containerservice.ResourceIdentityType.SYSTEM_ASSIGNED,
    ),
    agent_pool_profiles=[
        azure.containerservice.ManagedClusterAgentPoolProfileArgs(
            name="system",
            mode=azure.containerservice.AgentPoolMode.SYSTEM,
            vm_size="Standard_D4s_v3",
            count=1,
            os_type=azure.containerservice.OSType.LINUX,
        ),
        azure.containerservice.ManagedClusterAgentPoolProfileArgs(
            name="gpunodes",
            mode=azure.containerservice.AgentPoolMode.USER,
            vm_size="Standard_ND96isr_H100_v5",  # 8x NVIDIA H100 80GB per node
            count=node_count,
            os_type=azure.containerservice.OSType.LINUX,
            node_labels={
                "nvidia.com/gpu.present": "true",
            },
            node_taints=["nvidia.com/gpu=present:NoSchedule"],
        ),
    ],
    tags={
        "nvidia.com/aicr": "true",
        "nvidia.com/gpu": "h100",
    },
)

# Retrieve the kubeconfig from the AKS cluster
kubeconfig = pulumi.Output.all(resource_group.name, cluster.name).apply(
    lambda args: azure.containerservice.list_managed_cluster_user_credentials_output(
        resource_group_name=args[0],
        resource_name=args[1],
    ),
).apply(
    lambda creds: base64.b64decode(creds.kubeconfigs[0].value).decode("utf-8"),
)

# Deploy the NVIDIA AICR-validated GPU training stack
# This installs ~11 validated Helm charts including:
#   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
#   - Kubeflow Training Operator (distributed training with TrainJob)
#   - KAI Scheduler (GPU-aware scheduling)
#   - Kube Prometheus Stack (monitoring with GPU metrics)
#   - cert-manager, NVSentinel, Skyhook, and more
gpu_stack = aicr.ClusterStack("nvidia-aicr",
    kubeconfig=kubeconfig,
    accelerator="h100",
    service="aks",
    intent="training",
    platform="kubeflow",
    # Optional: customize specific components
    component_overrides={
        "gpu-operator": aicr.ComponentOverrideArgs(
            values={
                "driver": {
                    # Use a specific driver version if needed
                    "version": "580.105.08",
                },
            },
        ),
    },
)

# Exports
pulumi.export("kubeconfig", pulumi.Output.secret(kubeconfig))
pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("deployed_components", gpu_stack.deployed_components)
pulumi.export("component_count", gpu_stack.component_count)
