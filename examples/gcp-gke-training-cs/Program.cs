// GCP GKE + NVIDIA AICR H100 Training Stack
//
// Creates a GKE cluster with H100 GPU worker nodes, then deploys the full
// AICR-validated Kubeflow training stack.
//
// COST WARNING: a3-highgpu-8g instances cost ~$30/hr each (8x H100 80GB).
// Default nodeCount is 2, so plan on ~$60/hr while the cluster is up.
using Pulumi;
using Pulumi.Gcp.Container;
using Pulumi.Gcp.Container.Inputs;
using Pulumi.NvidiaAicr;

return await Deployment.RunAsync(() =>
{
    var config = new Config();
    var clusterName = config.Get("clusterName") ?? "aicr-training";
    var nodeCount = config.GetInt32("nodeCount") ?? 2;

    // Create the GKE cluster (we remove the default node pool and manage our own)
    var cluster = new Cluster(clusterName, new ClusterArgs
    {
        InitialNodeCount = 1,
        RemoveDefaultNodePool = true,
        DeletionProtection = false,
        ResourceLabels =
        {
            ["nvidia-aicr"] = "true",
            ["gpu-type"] = "h100",
        },
    });

    // Create a GPU node pool with A3 High-GPU machines (8x H100 80GB each)
    var gpuNodePool = new NodePool("gpu-pool", new NodePoolArgs
    {
        ClusterName = cluster.Name,
        NodeCount = nodeCount,
        NodeConfig = new NodePoolNodeConfigArgs
        {
            MachineType = "a3-highgpu-8g",  // 8x NVIDIA H100 80GB per node
            GuestAccelerators =
            {
                new NodePoolNodeConfigGuestAcceleratorArgs
                {
                    Type = "nvidia-h100-80gb",
                    Count = 8,
                },
            },
            OauthScopes =
            {
                "https://www.googleapis.com/auth/cloud-platform",
            },
            Labels =
            {
                ["nvidia.com/gpu"] = "h100",
            },
        },
    });

    // Construct a kubeconfig from the GKE cluster endpoint and CA certificate.
    // Uses gke-gcloud-auth-plugin for exec-based authentication.
    var kubeconfig = Output.Tuple(cluster.Endpoint, cluster.MasterAuth).Apply(t =>
    {
        var (endpoint, masterAuth) = t;
        var caCert = masterAuth.ClusterCaCertificate;
        return $@"apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: {caCert}
    server: https://{endpoint}
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
";
    });

    // Deploy the NVIDIA AICR-validated GPU training stack
    // This installs ~10 validated Helm charts including:
    //   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
    //   - Kubeflow Training Operator (distributed training with TrainJob)
    //   - KAI Scheduler (GPU-aware scheduling)
    //   - Kube Prometheus Stack (monitoring with GPU metrics)
    //   - cert-manager, NVSentinel, Skyhook, and more
    var gpuStack = new ClusterStack("nvidia-aicr", new ClusterStackArgs
    {
        Kubeconfig = kubeconfig,
        Accelerator = "h100",
        Service = "gke",
        Intent = "training",
        Platform = "kubeflow",
        ComponentOverrides =
        {
            ["gpu-operator"] = new ComponentOverrideArgs
            {
                Values = new InputMap<object>
                {
                    ["driver"] = new Dictionary<string, object>
                    {
                        ["version"] = "580.105.08",
                    },
                },
            },
        },
    }, new ComponentResourceOptions { DependsOn = { gpuNodePool } });

    return new Dictionary<string, object?>
    {
        ["kubeconfig"] = Output.CreateSecret(kubeconfig),
        ["recipeName"] = gpuStack.RecipeName,
        ["deployedComponents"] = gpuStack.DeployedComponents,
        ["componentCount"] = gpuStack.ComponentCount,
    };
});
