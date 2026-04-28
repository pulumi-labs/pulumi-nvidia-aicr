import * as pulumi from "@pulumi/pulumi";
import * as gcp from "@pulumi/gcp";
import * as aicr from "@pulumi/nvidia-aicr";

// ============================================================================
// GCP GKE + NVIDIA AICR H100 Training Stack
//
// This example creates a complete GPU training environment:
//   1. A GKE cluster with a dedicated H100 GPU node pool
//   2. The full AICR-validated software stack for distributed training
//      including GPU Operator, Kubeflow Trainer, monitoring, and more.
//
// COST WARNING: a3-highgpu-8g instances cost approximately $30/hr each
// (8x NVIDIA H100 80GB per node). This example provisions 2 nodes (~$60/hr).
// Remember to run `pulumi destroy` when finished to avoid unexpected charges.
// ============================================================================

const config = new pulumi.Config();
const clusterName = config.get("clusterName") || "aicr-training";
const nodeCount = config.getNumber("nodeCount") || 2;

// Create the GKE cluster (we remove the default node pool and manage our own)
const cluster = new gcp.container.Cluster(clusterName, {
    initialNodeCount: 1,
    removeDefaultNodePool: true,
    deletionProtection: false,
    resourceLabels: {
        "nvidia-aicr": "true",
        "gpu-type": "h100",
    },
});

// Create a GPU node pool with A3 High-GPU machines (8x H100 80GB each)
const gpuNodePool = new gcp.container.NodePool("gpu-pool", {
    cluster: cluster.name,
    nodeCount: nodeCount,
    nodeConfig: {
        machineType: "a3-highgpu-8g",  // 8x NVIDIA H100 80GB per node
        guestAccelerators: [{
            type: "nvidia-h100-80gb",
            count: 8,
        }],
        oauthScopes: [
            "https://www.googleapis.com/auth/cloud-platform",
        ],
        labels: {
            "nvidia.com/gpu": "h100",
        },
    },
});

// Construct a kubeconfig from the GKE cluster endpoint and CA certificate.
// Uses gke-gcloud-auth-plugin for exec-based authentication.
const kubeconfig = pulumi.interpolate`apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: ${cluster.masterAuth.clusterCaCertificate}
    server: https://${cluster.endpoint}
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
`;

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
    service: "gke",
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
}, { dependsOn: [gpuNodePool] });

// Exports
export const kubeconfigOutput = pulumi.secret(kubeconfig);
export const recipeName = gpuStack.recipeName;
export const deployedComponents = gpuStack.deployedComponents;
export const componentCount = gpuStack.componentCount;
