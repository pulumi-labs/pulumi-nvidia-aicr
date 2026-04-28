import * as pulumi from "@pulumi/pulumi";
import * as oci from "@pulumi/oci";
import * as aicr from "@pulumi/nvidia-aicr";

// ============================================================================
// Oracle Cloud OKE + NVIDIA AICR GB200 Training Stack
//
// This example creates a complete GPU training environment on OCI:
//   1. A VCN (Virtual Cloud Network) with a subnet
//   2. An OKE (Oracle Kubernetes Engine) cluster
//   3. A GPU node pool with BM.GPU.GB200.4 bare-metal shapes
//   4. The full AICR-validated software stack for distributed training
//      including GPU Operator, Kubeflow Trainer, monitoring, and more.
//
// This is the only example using NVIDIA GB200 (Grace Blackwell) GPUs --
// the newest and most powerful NVIDIA data-center GPU architecture,
// featuring the Blackwell GPU paired with the Grace CPU for maximum
// training throughput.
//
// COST WARNING: BM.GPU.GB200.4 bare-metal instances are premium-priced
// GPU shapes. Contact OCI sales for current pricing as availability and
// rates vary by region. Remember to run `pulumi destroy` when finished
// to avoid unexpected charges.
// ============================================================================

const config = new pulumi.Config();
const clusterName = config.get("clusterName") || "aicr-training";
const nodeCount = config.getNumber("nodeCount") || 2;
const compartmentId = config.require("compartmentId");
const availabilityDomain = config.require("availabilityDomain");

// Create a VCN for the OKE cluster
const vcn = new oci.core.Vcn("gpu-vcn", {
    compartmentId: compartmentId,
    cidrBlocks: ["10.0.0.0/16"],
    displayName: `${clusterName}-vcn`,
    dnsLabel: "gpuvcn",
});

// Create an internet gateway for public access
const internetGateway = new oci.core.InternetGateway("igw", {
    compartmentId: compartmentId,
    vcnId: vcn.id,
    displayName: `${clusterName}-igw`,
});

// Create a route table with internet access
const routeTable = new oci.core.RouteTable("public-rt", {
    compartmentId: compartmentId,
    vcnId: vcn.id,
    displayName: `${clusterName}-public-rt`,
    routeRules: [{
        networkEntityId: internetGateway.id,
        destination: "0.0.0.0/0",
        destinationType: "CIDR_BLOCK",
    }],
});

// Create a security list allowing necessary traffic
const securityList = new oci.core.SecurityList("oke-sl", {
    compartmentId: compartmentId,
    vcnId: vcn.id,
    displayName: `${clusterName}-oke-sl`,
    egressSecurityRules: [{
        destination: "0.0.0.0/0",
        protocol: "all",
    }],
    ingressSecurityRules: [
        {
            source: "10.0.0.0/16",
            protocol: "all",
        },
        {
            source: "0.0.0.0/0",
            protocol: "6", // TCP
            tcpOptions: {
                min: 6443,
                max: 6443,
            },
        },
    ],
});

// Create a subnet for the OKE cluster and node pool
const subnet = new oci.core.Subnet("oke-subnet", {
    compartmentId: compartmentId,
    vcnId: vcn.id,
    cidrBlock: "10.0.1.0/24",
    displayName: `${clusterName}-subnet`,
    routeTableId: routeTable.id,
    securityListIds: [securityList.id],
    dnsLabel: "okesubnet",
});

// Create the OKE cluster
const cluster = new oci.containerengine.Cluster(clusterName, {
    compartmentId: compartmentId,
    vcnId: vcn.id,
    kubernetesVersion: "v1.30.1",
    name: clusterName,
    options: {
        serviceLbSubnetIds: [subnet.id],
        kubernetesNetworkConfig: {
            podsCidr: "10.244.0.0/16",
            servicesCidr: "10.96.0.0/16",
        },
    },
});

// Create a GPU node pool with NVIDIA GB200 bare-metal shapes
// BM.GPU.GB200.4 provides 4 NVIDIA GB200 GPUs per bare-metal node --
// the most powerful GPU shape available on OCI for AI/ML training.
const nodePool = new oci.containerengine.NodePool("gpu-nodes", {
    compartmentId: compartmentId,
    clusterId: cluster.id,
    kubernetesVersion: "v1.30.1",
    name: `${clusterName}-gb200-pool`,
    nodeShape: "BM.GPU.GB200.4",
    nodeConfigDetails: {
        size: nodeCount,
        placementConfigs: [{
            availabilityDomain: availabilityDomain,
            subnetId: subnet.id,
        }],
    },
    initialNodeLabels: [{
        key: "nvidia.com/gpu",
        value: "gb200",
    }],
});

// Retrieve kubeconfig from the OKE cluster
const kubeconfig = oci.containerengine.getClusterKubeConfigOutput({
    clusterId: cluster.id,
});

// Deploy the NVIDIA AICR-validated GPU training stack
// This installs the full set of validated Helm charts including:
//   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
//   - Kubeflow Training Operator (distributed training with TrainJob)
//   - KAI Scheduler (GPU-aware scheduling)
//   - Kube Prometheus Stack (monitoring with GPU metrics)
//   - cert-manager, NVSentinel, Skyhook, and more
//
// The "gb200" accelerator selects the recipe family optimized for
// NVIDIA Grace Blackwell architecture.
const gpuStack = new aicr.ClusterStack("nvidia-aicr", {
    kubeconfig: kubeconfig.apply(kc => kc.content),
    accelerator: "gb200",
    service: "oke",
    intent: "training",
    platform: "kubeflow",
});

// Exports
export const recipeName = gpuStack.recipeName;
export const deployedComponents = gpuStack.deployedComponents;
export const componentCount = gpuStack.componentCount;
