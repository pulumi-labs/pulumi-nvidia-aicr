"""AWS EKS + NVIDIA AICR H100 Training Stack

This example creates a complete GPU training environment:
  1. An EKS cluster with H100 GPU worker nodes
  2. The full AICR-validated software stack for distributed training
     including GPU Operator, Kubeflow Trainer, monitoring, and more.

COST WARNING: p5.48xlarge instances cost approximately $98.32/hr each.
This example provisions 2 nodes (~$196/hr). Remember to run
`pulumi destroy` when finished to avoid unexpected charges.
"""

import pulumi
import pulumi_aws as aws
import pulumi_eks as eks
import pulumi_nvidia_aicr as aicr

config = pulumi.Config()
cluster_name = config.get("clusterName") or "aicr-training"
node_count = config.get_int("nodeCount") or 2

# Create a VPC for the EKS cluster
vpc = aws.ec2.Vpc("gpu-vpc",
    cidr_block="10.0.0.0/16",
    enable_dns_hostnames=True,
    enable_dns_support=True,
    tags={"Name": f"{cluster_name}-vpc"},
)

public_subnet_1 = aws.ec2.Subnet("public-1",
    vpc_id=vpc.id,
    cidr_block="10.0.1.0/24",
    availability_zone="us-east-1a",
    map_public_ip_on_launch=True,
    tags={"Name": f"{cluster_name}-public-1"},
)

public_subnet_2 = aws.ec2.Subnet("public-2",
    vpc_id=vpc.id,
    cidr_block="10.0.2.0/24",
    availability_zone="us-east-1b",
    map_public_ip_on_launch=True,
    tags={"Name": f"{cluster_name}-public-2"},
)

igw = aws.ec2.InternetGateway("igw", vpc_id=vpc.id)

route_table = aws.ec2.RouteTable("public-rt",
    vpc_id=vpc.id,
    routes=[aws.ec2.RouteTableRouteArgs(
        cidr_block="0.0.0.0/0",
        gateway_id=igw.id,
    )],
)

aws.ec2.RouteTableAssociation("public-1-rta",
    subnet_id=public_subnet_1.id,
    route_table_id=route_table.id,
)

aws.ec2.RouteTableAssociation("public-2-rta",
    subnet_id=public_subnet_2.id,
    route_table_id=route_table.id,
)

# Create the EKS cluster with GPU node group
cluster = eks.Cluster(cluster_name,
    vpc_id=vpc.id,
    subnet_ids=[public_subnet_1.id, public_subnet_2.id],
    instance_type="p5.48xlarge",    # 8x NVIDIA H100 80GB per node
    desired_capacity=node_count,
    min_size=1,
    max_size=node_count * 2,
    node_associate_public_ip_address=False,
    create_oidc_provider=True,
    tags={
        "nvidia.com/aicr": "true",
        "nvidia.com/gpu": "h100",
    },
)

# Deploy the NVIDIA AICR-validated GPU training stack
# This installs ~11 validated Helm charts including:
#   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
#   - Kubeflow Training Operator (distributed training with TrainJob)
#   - KAI Scheduler (GPU-aware scheduling)
#   - Kube Prometheus Stack (monitoring with GPU metrics)
#   - cert-manager, NVSentinel, Skyhook, and more
gpu_stack = aicr.ClusterStack("nvidia-aicr",
    kubeconfig=cluster.kubeconfig_json,
    accelerator="h100",
    service="eks",
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
pulumi.export("kubeconfig", pulumi.Output.secret(cluster.kubeconfig_json))
pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("deployed_components", gpu_stack.deployed_components)
pulumi.export("component_count", gpu_stack.component_count)
