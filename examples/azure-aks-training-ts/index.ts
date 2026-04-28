import * as pulumi from "@pulumi/pulumi";
import * as azure from "@pulumi/azure-native";
import * as aicr from "@pulumi/nvidia-aicr";

// ============================================================================
// Azure AKS + NVIDIA AICR H100 Training Stack
//
// This example creates a complete GPU training environment:
//   1. An AKS cluster with H100 GPU worker nodes
//   2. The full AICR-validated software stack for distributed training
//      including GPU Operator, Kubeflow Trainer, monitoring, and more.
//
// COST WARNING: Standard_ND96isr_H100_v5 VMs cost approximately $40/hr each.
// This example provisions 2 GPU nodes (~$80/hr). Remember to run
// `pulumi destroy` when finished to avoid unexpected charges.
// ============================================================================

const config = new pulumi.Config();
const clusterName = config.get("clusterName") || "aicr-training";
const nodeCount = config.getNumber("nodeCount") || 2;

// Create a resource group for all resources
const resourceGroup = new azure.resources.ResourceGroup("gpu-rg", {
    resourceGroupName: `${clusterName}-rg`,
    tags: {
        "nvidia.com/aicr": "true",
        "nvidia.com/gpu": "h100",
    },
});

// Create the AKS cluster with a system node pool and a GPU node pool
const cluster = new azure.containerservice.ManagedCluster(clusterName, {
    resourceGroupName: resourceGroup.name,
    resourceName: clusterName,
    dnsPrefix: clusterName,
    kubernetesVersion: "1.30",
    identity: {
        type: azure.containerservice.ResourceIdentityType.SystemAssigned,
    },
    agentPoolProfiles: [
        {
            name: "system",
            mode: azure.containerservice.AgentPoolMode.System,
            vmSize: "Standard_D4s_v3",
            count: 1,
            osType: azure.containerservice.OSType.Linux,
        },
        {
            name: "gpunodes",
            mode: azure.containerservice.AgentPoolMode.User,
            vmSize: "Standard_ND96isr_H100_v5", // 8x NVIDIA H100 80GB per node
            count: nodeCount,
            osType: azure.containerservice.OSType.Linux,
            nodeLabels: {
                "nvidia.com/gpu.present": "true",
            },
            nodeTaints: ["nvidia.com/gpu=present:NoSchedule"],
        },
    ],
    tags: {
        "nvidia.com/aicr": "true",
        "nvidia.com/gpu": "h100",
    },
});

// Retrieve the kubeconfig from the AKS cluster
const kubeconfig = pulumi.all([resourceGroup.name, cluster.name]).apply(
    ([rgName, clusterName]) =>
        azure.containerservice.listManagedClusterUserCredentialsOutput({
            resourceGroupName: rgName,
            resourceName: clusterName,
        }),
).apply(creds => {
    const encoded = creds.kubeconfigs[0].value;
    return Buffer.from(encoded, "base64").toString("utf-8");
});

// Deploy the NVIDIA AICR-validated GPU training stack
// This installs ~10 validated Helm charts including:
//   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
//   - Kubeflow Training Operator (distributed training with TrainJob)
//   - KAI Scheduler (GPU-aware scheduling)
//   - Kube Prometheus Stack (monitoring with GPU metrics)
//   - cert-manager, NVSentinel, Skyhook, and more
const gpuStack = new aicr.ClusterStack("nvidia-aicr", {
    kubeconfig: kubeconfig,
    accelerator: "h100",
    service: "aks",
    intent: "training",
    platform: "kubeflow",
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
export const kubeconfigOut = pulumi.secret(kubeconfig);
export const recipeName = gpuStack.recipeName;
export const deployedComponents = gpuStack.deployedComponents;
export const componentCount = gpuStack.componentCount;
