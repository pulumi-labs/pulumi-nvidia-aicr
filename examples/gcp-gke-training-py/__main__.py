"""GCP GKE + NVIDIA AICR H100 Training Stack

This example creates a complete GPU training environment:
  1. A GKE cluster with a dedicated H100 GPU node pool
  2. The full AICR-validated software stack for distributed training
     including GPU Operator, Kubeflow Trainer, monitoring, and more.

COST WARNING: a3-highgpu-8g instances cost approximately $30/hr each
(8x NVIDIA H100 80GB per node). This example provisions 2 nodes (~$60/hr).
Remember to run `pulumi destroy` when finished to avoid unexpected charges.
"""

import pulumi
import pulumi_gcp as gcp
import pulumi_nvidia_aicr as aicr

config = pulumi.Config()
cluster_name = config.get("clusterName") or "aicr-training"
node_count = config.get_int("nodeCount") or 2

# Create the GKE cluster (we remove the default node pool and manage our own)
cluster = gcp.container.Cluster(cluster_name,
    initial_node_count=1,
    remove_default_node_pool=True,
    deletion_protection=False,
    resource_labels={
        "nvidia-aicr": "true",
        "gpu-type": "h100",
    },
)

# Create a GPU node pool with A3 High-GPU machines (8x H100 80GB each)
gpu_node_pool = gcp.container.NodePool("gpu-pool",
    cluster=cluster.name,
    node_count=node_count,
    node_config=gcp.container.NodePoolNodeConfigArgs(
        machine_type="a3-highgpu-8g",  # 8x NVIDIA H100 80GB per node
        guest_accelerators=[gcp.container.NodePoolNodeConfigGuestAcceleratorArgs(
            type="nvidia-h100-80gb",
            count=8,
        )],
        oauth_scopes=[
            "https://www.googleapis.com/auth/cloud-platform",
        ],
        labels={
            "nvidia.com/gpu": "h100",
        },
    ),
)

# Construct a kubeconfig from the GKE cluster endpoint and CA certificate.
# Uses gke-gcloud-auth-plugin for exec-based authentication.
kubeconfig = pulumi.Output.all(
    cluster.endpoint,
    cluster.master_auth.cluster_ca_certificate,
).apply(lambda args: f"""apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: {args[1]}
    server: https://{args[0]}
  name: gke-cluster
contexts:
- context:
    cluster: gke-cluster
    user: gke-user
  name: gke-context
current-context: gke-context
kind: Config
users:
- name: gke-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gke-gcloud-auth-plugin
      installHint: Install gke-gcloud-auth-plugin for kubeconfig exec auth
      provideClusterInfo: true
""")

# Deploy the NVIDIA AICR-validated GPU training stack
# This installs ~10 validated Helm charts including:
#   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
#   - Kubeflow Training Operator (distributed training with TrainJob)
#   - KAI Scheduler (GPU-aware scheduling)
#   - Kube Prometheus Stack (monitoring with GPU metrics)
#   - cert-manager, NVSentinel, Skyhook, and more
gpu_stack = aicr.ClusterStack("nvidia-aicr",
    kubeconfig=kubeconfig,
    accelerator="h100",
    service="gke",
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
    opts=pulumi.ResourceOptions(depends_on=[gpu_node_pool]),
)

# Exports
pulumi.export("kubeconfig", pulumi.Output.secret(kubeconfig))
pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("deployed_components", gpu_stack.deployed_components)
pulumi.export("component_count", gpu_stack.component_count)
