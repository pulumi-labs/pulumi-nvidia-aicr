"""Oracle Cloud OKE + NVIDIA AICR GB200 Training Stack

This example creates a complete GPU training environment on OCI:
  1. A VCN (Virtual Cloud Network) with a subnet
  2. An OKE (Oracle Kubernetes Engine) cluster
  3. A GPU node pool with BM.GPU.GB200.4 bare-metal shapes
  4. The full AICR-validated software stack for distributed training
     including GPU Operator, Kubeflow Trainer, monitoring, and more.

This is the only example using NVIDIA GB200 (Grace Blackwell) GPUs --
the newest and most powerful NVIDIA data-center GPU architecture,
featuring the Blackwell GPU paired with the Grace CPU for maximum
training throughput.

COST WARNING: BM.GPU.GB200.4 bare-metal instances are premium-priced
GPU shapes. Contact OCI sales for current pricing as availability and
rates vary by region. Remember to run `pulumi destroy` when finished
to avoid unexpected charges.
"""

import pulumi
import pulumi_oci as oci
import pulumi_nvidia_aicr as aicr

config = pulumi.Config()
cluster_name = config.get("clusterName") or "aicr-training"
node_count = config.get_int("nodeCount") or 2
compartment_id = config.require("compartmentId")
availability_domain = config.require("availabilityDomain")

# Create a VCN for the OKE cluster
vcn = oci.core.Vcn("gpu-vcn",
    compartment_id=compartment_id,
    cidr_blocks=["10.0.0.0/16"],
    display_name=f"{cluster_name}-vcn",
    dns_label="gpuvcn",
)

# Create an internet gateway for public access
internet_gateway = oci.core.InternetGateway("igw",
    compartment_id=compartment_id,
    vcn_id=vcn.id,
    display_name=f"{cluster_name}-igw",
)

# Create a route table with internet access
route_table = oci.core.RouteTable("public-rt",
    compartment_id=compartment_id,
    vcn_id=vcn.id,
    display_name=f"{cluster_name}-public-rt",
    route_rules=[oci.core.RouteTableRouteRuleArgs(
        network_entity_id=internet_gateway.id,
        destination="0.0.0.0/0",
        destination_type="CIDR_BLOCK",
    )],
)

# Create a security list allowing necessary traffic
security_list = oci.core.SecurityList("oke-sl",
    compartment_id=compartment_id,
    vcn_id=vcn.id,
    display_name=f"{cluster_name}-oke-sl",
    egress_security_rules=[oci.core.SecurityListEgressSecurityRuleArgs(
        destination="0.0.0.0/0",
        protocol="all",
    )],
    ingress_security_rules=[
        oci.core.SecurityListIngressSecurityRuleArgs(
            source="10.0.0.0/16",
            protocol="all",
        ),
        oci.core.SecurityListIngressSecurityRuleArgs(
            source="0.0.0.0/0",
            protocol="6",  # TCP
            tcp_options=oci.core.SecurityListIngressSecurityRuleTcpOptionsArgs(
                min=6443,
                max=6443,
            ),
        ),
    ],
)

# Create a subnet for the OKE cluster and node pool
subnet = oci.core.Subnet("oke-subnet",
    compartment_id=compartment_id,
    vcn_id=vcn.id,
    cidr_block="10.0.1.0/24",
    display_name=f"{cluster_name}-subnet",
    route_table_id=route_table.id,
    security_list_ids=[security_list.id],
    dns_label="okesubnet",
)

# Create the OKE cluster
cluster = oci.containerengine.Cluster(cluster_name,
    compartment_id=compartment_id,
    vcn_id=vcn.id,
    kubernetes_version="v1.30.1",
    name=cluster_name,
    options=oci.containerengine.ClusterOptionsArgs(
        service_lb_subnet_ids=[subnet.id],
        kubernetes_network_config=oci.containerengine.ClusterOptionsKubernetesNetworkConfigArgs(
            pods_cidr="10.244.0.0/16",
            services_cidr="10.96.0.0/16",
        ),
    ),
)

# Create a GPU node pool with NVIDIA GB200 bare-metal shapes.
# BM.GPU.GB200.4 provides 4 NVIDIA GB200 GPUs per bare-metal node --
# the most powerful GPU shape available on OCI for AI/ML training.
node_pool = oci.containerengine.NodePool("gpu-nodes",
    compartment_id=compartment_id,
    cluster_id=cluster.id,
    kubernetes_version="v1.30.1",
    name=f"{cluster_name}-gb200-pool",
    node_shape="BM.GPU.GB200.4",
    node_config_details=oci.containerengine.NodePoolNodeConfigDetailsArgs(
        size=node_count,
        placement_configs=[oci.containerengine.NodePoolNodeConfigDetailsPlacementConfigArgs(
            availability_domain=availability_domain,
            subnet_id=subnet.id,
        )],
    ),
    initial_node_labels=[oci.containerengine.NodePoolInitialNodeLabelArgs(
        key="nvidia.com/gpu",
        value="gb200",
    )],
)

# Retrieve kubeconfig from the OKE cluster
kubeconfig = oci.containerengine.get_cluster_kube_config_output(
    cluster_id=cluster.id,
)

# Deploy the NVIDIA AICR-validated GPU training stack.
# This installs the full set of validated Helm charts including:
#   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
#   - Kubeflow Training Operator (distributed training with TrainJob)
#   - KAI Scheduler (GPU-aware scheduling)
#   - Kube Prometheus Stack (monitoring with GPU metrics)
#   - cert-manager, NVSentinel, Skyhook, and more
#
# The "gb200" accelerator selects the recipe family optimized for
# NVIDIA Grace Blackwell architecture.
gpu_stack = aicr.ClusterStack("nvidia-aicr",
    kubeconfig=kubeconfig.apply(lambda kc: kc.content),
    accelerator="gb200",
    service="oke",
    intent="training",
    platform="kubeflow",
)

# Exports
pulumi.export("recipe_name", gpu_stack.recipe_name)
pulumi.export("deployed_components", gpu_stack.deployed_components)
pulumi.export("component_count", gpu_stack.component_count)
