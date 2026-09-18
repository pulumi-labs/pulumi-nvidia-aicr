import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";
import * as eks from "@pulumi/eks";
import * as aicr from "@pulumi-labs/nvidia-aicr";

// ============================================================================
// AWS EKS + NVIDIA AICR GB300 Training Stack
//
// This example creates a complete GPU training environment:
//   1. An EKS cluster (Kubernetes 1.34+, required by the GB300 recipe for
//      the GA DRA API) with a GB300 NVL72 managed node group:
//      p6e-gb300r.36xlarge, 4 GPUs per node on Grace ARM64 hosts, EFA
//      scale-out fabric.
//   2. The full AICR-validated software stack for distributed training
//      including GPU Operator, DRA driver, EFA device plugin, Kubeflow
//      Trainer, monitoring, and more.
//
// CAPACITY: GB300 instances on AWS are sold through EC2 Capacity Blocks for
// ML. Reserve a block for p6e-gb300r.36xlarge in one availability zone,
// then pass its ID via `pulumi config set capacityReservationId cr-...`.
// The node group is pinned to that reservation and placed in the matching
// zone. Capacity Blocks are billed per reservation, not per instance-hour.
// Remember to run `pulumi destroy` when finished.
// ============================================================================

const config = new pulumi.Config();
const clusterName = config.get("clusterName") || "aicr-gb300-training";
const nodeCount = config.getNumber("nodeCount") || 2;
const capacityReservationId = config.require("capacityReservationId");
// Capacity Blocks are zonal: the GPU node group lives in this zone.
const gpuAvailabilityZone = config.get("gpuAvailabilityZone") || "us-east-1a";
// EKS needs subnets in at least two zones for the control plane.
const secondAvailabilityZone = config.get("secondAvailabilityZone") || "us-east-1b";

// Create a VPC for the EKS cluster
const vpc = new aws.ec2.Vpc("gpu-vpc", {
    cidrBlock: "10.0.0.0/16",
    enableDnsHostnames: true,
    enableDnsSupport: true,
    tags: { Name: `${clusterName}-vpc` },
});

const gpuSubnet = new aws.ec2.Subnet("public-gpu", {
    vpcId: vpc.id,
    cidrBlock: "10.0.1.0/24",
    availabilityZone: gpuAvailabilityZone,
    mapPublicIpOnLaunch: true,
    tags: { Name: `${clusterName}-public-gpu` },
});

const secondSubnet = new aws.ec2.Subnet("public-2", {
    vpcId: vpc.id,
    cidrBlock: "10.0.2.0/24",
    availabilityZone: secondAvailabilityZone,
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

new aws.ec2.RouteTableAssociation("public-gpu-rta", {
    subnetId: gpuSubnet.id,
    routeTableId: routeTable.id,
});

new aws.ec2.RouteTableAssociation("public-2-rta", {
    subnetId: secondSubnet.id,
    routeTableId: routeTable.id,
});

// IAM role for the managed node group
const nodeRole = new aws.iam.Role("node-role", {
    assumeRolePolicy: JSON.stringify({
        Version: "2012-10-17",
        Statement: [{
            Action: "sts:AssumeRole",
            Effect: "Allow",
            Principal: { Service: "ec2.amazonaws.com" },
        }],
    }),
});

const nodePolicies = [
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
];
nodePolicies.forEach((policyArn, i) => {
    new aws.iam.RolePolicyAttachment(`node-policy-${i}`, {
        role: nodeRole.name,
        policyArn,
    });
});

// Create the EKS cluster. The default node group is skipped: GB300 nodes
// come from the Capacity-Block-backed managed node group below.
const cluster = new eks.Cluster(clusterName, {
    version: "1.34",
    vpcId: vpc.id,
    subnetIds: [gpuSubnet.id, secondSubnet.id],
    skipDefaultNodeGroup: true,
    instanceRoles: [nodeRole],
    createOidcProvider: true,
    tags: {
        "nvidia.com/aicr": "true",
        "nvidia.com/gpu": "gb300",
    },
});

// Launch template pinning the node group to the Capacity Block. EFA is
// enabled on the primary interface; p6e-gb300r.36xlarge presents exactly
// one usable EFA interface, which is the shape the AICR recipe targets.
const launchTemplate = new aws.ec2.LaunchTemplate("gpu-lt", {
    capacityReservationSpecification: {
        capacityReservationTarget: { capacityReservationId },
    },
    networkInterfaces: [{
        deviceIndex: 0,
        interfaceType: "efa",
        associatePublicIpAddress: "true",
        deleteOnTermination: "true",
    }],
    tagSpecifications: [{
        resourceType: "instance",
        tags: { Name: `${clusterName}-gb300`, "nvidia.com/gpu": "gb300" },
    }],
});

// GB300 NVL72 managed node group: Grace ARM64 hosts need the ARM64 NVIDIA
// AMI, and Capacity Blocks require the CAPACITY_BLOCK capacity type.
const gpuNodes = new eks.ManagedNodeGroup("gb300-nodes", {
    cluster,
    nodeRole,
    subnetIds: [gpuSubnet.id],
    instanceTypes: ["p6e-gb300r.36xlarge"],   // 4x NVIDIA GB300 per node
    amiType: "AL2023_ARM_64_NVIDIA",
    capacityType: "CAPACITY_BLOCK",
    launchTemplate: {
        id: launchTemplate.id,
        version: launchTemplate.latestVersion.apply(v => `${v}`),
    },
    scalingConfig: {
        desiredSize: nodeCount,
        minSize: nodeCount,
        maxSize: nodeCount,
    },
    labels: {
        "nvidia.com/gpu": "gb300",
    },
});

// Deploy the NVIDIA AICR-validated GB300 training stack
// This installs ~15 validated Helm charts including:
//   - NVIDIA GPU Operator with GB300 kernel-module pre-manifests
//   - NVIDIA DRA driver (ComputeDomain / IMEX for NVL72)
//   - AWS EFA device plugin and EBS CSI driver
//   - Kubeflow Trainer, KAI Scheduler, NVSentinel, monitoring, and more
const gpuStack = new aicr.ClusterStack("nvidia-aicr", {
    kubeconfig: cluster.kubeconfigJson,
    accelerator: "gb300",
    service: "eks",
    intent: "training",
    platform: "kubeflow",
    os: "ubuntu",
}, { dependsOn: [gpuNodes] });

// Validate the deployed stack empirically: a snapshot agent captures cluster
// state, then the recipe's deployment and conformance checks run as Jobs in
// the cluster. With the default strict: false the update always succeeds
// and the verdict is data — read the exported status and per-check results.
const validation = new aicr.ValidationRun("nvidia-aicr-validation", {
    criteria: gpuStack.criteria,              // same recipe as the stack — no drift
    recipeDataVersion: gpuStack.recipeVersion, // assert same recipe data as deployed
    kubeconfig: cluster.kubeconfigJson,
    triggers: [gpuStack.deployedComponents],  // re-validate when the stack changes
});

// Exports
export const kubeconfig = pulumi.secret(cluster.kubeconfigJson);
export const recipeName = gpuStack.recipeName;
export const deployedComponents = gpuStack.deployedComponents;
export const componentCount = gpuStack.componentCount;
export const validationStatus = validation.status;
export const validationChecks = validation.phaseResults;
