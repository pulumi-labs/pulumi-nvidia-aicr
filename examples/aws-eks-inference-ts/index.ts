import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";
import * as eks from "@pulumi/eks";
import * as aicr from "@pulumi/nvidia-aicr";

// ============================================================================
// AWS EKS + NVIDIA AICR H100 vLLM Inference Stack
//
// This example creates a complete GPU inference environment:
//   1. An EKS cluster with H100 GPU worker nodes
//   2. The full AICR-validated software stack for vLLM model serving
//      including GPU Operator, NIM Operator, KGateway, monitoring, and more.
//
// The NIM (NVIDIA Inference Microservices) platform provides optimized
// vLLM-based model serving with automatic scaling, health checks, and
// GPU-aware routing via KGateway.
//
// COST WARNING: p5.48xlarge instances cost approximately $98.32/hr each.
// This example provisions 2 nodes (~$196/hr). Remember to run
// `pulumi destroy` when finished to avoid unexpected charges.
// ============================================================================

const config = new pulumi.Config();
const clusterName = config.get("clusterName") || "aicr-inference";
const nodeCount = config.getNumber("nodeCount") || 2;

// Create a VPC for the EKS cluster
const vpc = new aws.ec2.Vpc("gpu-vpc", {
    cidrBlock: "10.0.0.0/16",
    enableDnsHostnames: true,
    enableDnsSupport: true,
    tags: { Name: `${clusterName}-vpc` },
});

const publicSubnet1 = new aws.ec2.Subnet("public-1", {
    vpcId: vpc.id,
    cidrBlock: "10.0.1.0/24",
    availabilityZone: "us-east-1a",
    mapPublicIpOnLaunch: true,
    tags: { Name: `${clusterName}-public-1` },
});

const publicSubnet2 = new aws.ec2.Subnet("public-2", {
    vpcId: vpc.id,
    cidrBlock: "10.0.2.0/24",
    availabilityZone: "us-east-1b",
    mapPublicIpOnLaunch: true,
    tags: { Name: `${clusterName}-public-2` },
});

const igw = new aws.ec2.InternetGateway("igw", {
    vpcId: vpc.id,
});

const routeTable = new aws.ec2.RouteTable("public-rt", {
    vpcId: vpc.id,
    routes: [{
        cidrBlock: "0.0.0.0/0",
        gatewayId: igw.id,
    }],
});

new aws.ec2.RouteTableAssociation("public-1-rta", {
    subnetId: publicSubnet1.id,
    routeTableId: routeTable.id,
});

new aws.ec2.RouteTableAssociation("public-2-rta", {
    subnetId: publicSubnet2.id,
    routeTableId: routeTable.id,
});

// Create the EKS cluster with GPU node group
const cluster = new eks.Cluster(clusterName, {
    vpcId: vpc.id,
    subnetIds: [publicSubnet1.id, publicSubnet2.id],
    instanceType: "p5.48xlarge",    // 8x NVIDIA H100 80GB per node
    desiredCapacity: nodeCount,
    minSize: 1,
    maxSize: nodeCount * 2,
    nodeAssociatePublicIpAddress: false,
    createOidcProvider: true,
    tags: {
        "nvidia.com/aicr": "true",
        "nvidia.com/gpu": "h100",
    },
});

// Deploy the NVIDIA AICR-validated vLLM inference stack
// This installs the validated Helm charts for NIM-based inference including:
//   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
//   - NIM Operator (manages NIM custom resources for model serving)
//   - KGateway (GPU-aware ingress routing for inference endpoints)
//   - KAI Scheduler (GPU-aware scheduling)
//   - Kube Prometheus Stack (monitoring with GPU metrics)
//   - cert-manager, NVSentinel, Skyhook, and more
const gpuStack = new aicr.ClusterStack("nvidia-aicr", {
    kubeconfig: cluster.kubeconfigJson,
    accelerator: "h100",
    service: "eks",
    intent: "inference",
    platform: "nim",
    // Optional: customize specific components
    componentOverrides: {
        "gpu-operator": {
            values: {
                driver: {
                    // Use a specific driver version if needed
                    version: "580.105.08",
                },
            },
        },
    },
});

// Exports
export const kubeconfig = pulumi.secret(cluster.kubeconfigJson);
export const recipeName = gpuStack.recipeName;
export const deployedComponents = gpuStack.deployedComponents;
export const componentCount = gpuStack.componentCount;
