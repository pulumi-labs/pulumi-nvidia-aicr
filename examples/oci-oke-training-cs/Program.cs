// Oracle Cloud OKE + NVIDIA AICR GB200 Training Stack
//
// Creates an OKE cluster with GB200 bare-metal GPU nodes, then deploys the full
// AICR-validated Kubeflow training stack.
//
// This is the only example using NVIDIA GB200 (Grace Blackwell) GPUs --
// the newest and most powerful NVIDIA data-center GPU architecture.
//
// COST WARNING: BM.GPU.GB200.4 bare-metal instances are premium-priced.
// Contact OCI sales for current pricing. Destroy when done!
using Pulumi;
using Pulumi.Oci.Core;
using Pulumi.Oci.Core.Inputs;
using Pulumi.Oci.ContainerEngine;
using Pulumi.Oci.ContainerEngine.Inputs;
using Pulumi.NvidiaAicr;

return await Deployment.RunAsync(() =>
{
    var config = new Config();
    var clusterName = config.Get("clusterName") ?? "aicr-training";
    var nodeCount = config.GetInt32("nodeCount") ?? 2;
    var compartmentId = config.Require("compartmentId");
    var availabilityDomain = config.Require("availabilityDomain");

    // Create a VCN for the OKE cluster
    var vcn = new Vcn("gpu-vcn", new VcnArgs
    {
        CompartmentId = compartmentId,
        CidrBlocks = { "10.0.0.0/16" },
        DisplayName = $"{clusterName}-vcn",
        DnsLabel = "gpuvcn",
    });

    // Create an internet gateway for public access
    var internetGateway = new InternetGateway("igw", new InternetGatewayArgs
    {
        CompartmentId = compartmentId,
        VcnId = vcn.Id,
        DisplayName = $"{clusterName}-igw",
    });

    // Create a route table with internet access
    var routeTable = new RouteTable("public-rt", new RouteTableArgs
    {
        CompartmentId = compartmentId,
        VcnId = vcn.Id,
        DisplayName = $"{clusterName}-public-rt",
        RouteRules =
        {
            new RouteTableRouteRuleArgs
            {
                NetworkEntityId = internetGateway.Id,
                Destination = "0.0.0.0/0",
                DestinationType = "CIDR_BLOCK",
            },
        },
    });

    // Create a security list allowing necessary traffic
    var securityList = new SecurityList("oke-sl", new SecurityListArgs
    {
        CompartmentId = compartmentId,
        VcnId = vcn.Id,
        DisplayName = $"{clusterName}-oke-sl",
        EgressSecurityRules =
        {
            new SecurityListEgressSecurityRuleArgs
            {
                Destination = "0.0.0.0/0",
                Protocol = "all",
            },
        },
        IngressSecurityRules =
        {
            new SecurityListIngressSecurityRuleArgs
            {
                Source = "10.0.0.0/16",
                Protocol = "all",
            },
            new SecurityListIngressSecurityRuleArgs
            {
                Source = "0.0.0.0/0",
                Protocol = "6", // TCP
                TcpOptions = new SecurityListIngressSecurityRuleTcpOptionsArgs
                {
                    Min = 6443,
                    Max = 6443,
                },
            },
        },
    });

    // Create a subnet for the OKE cluster and node pool
    var subnet = new Subnet("oke-subnet", new SubnetArgs
    {
        CompartmentId = compartmentId,
        VcnId = vcn.Id,
        CidrBlock = "10.0.1.0/24",
        DisplayName = $"{clusterName}-subnet",
        RouteTableId = routeTable.Id,
        SecurityListIds = { securityList.Id },
        DnsLabel = "okesubnet",
    });

    // Create the OKE cluster
    var cluster = new Cluster(clusterName, new ClusterArgs
    {
        CompartmentId = compartmentId,
        VcnId = vcn.Id,
        KubernetesVersion = "v1.30.1",
        Name = clusterName,
        Options = new ClusterOptionsArgs
        {
            ServiceLbSubnetIds = { subnet.Id },
            KubernetesNetworkConfig = new ClusterOptionsKubernetesNetworkConfigArgs
            {
                PodsCidr = "10.244.0.0/16",
                ServicesCidr = "10.96.0.0/16",
            },
        },
    });

    // Create a GPU node pool with NVIDIA GB200 bare-metal shapes.
    // BM.GPU.GB200.4 provides 4 NVIDIA GB200 GPUs per bare-metal node --
    // the most powerful GPU shape available on OCI for AI/ML training.
    var nodePool = new NodePool("gpu-nodes", new NodePoolArgs
    {
        CompartmentId = compartmentId,
        ClusterId = cluster.Id,
        KubernetesVersion = "v1.30.1",
        Name = $"{clusterName}-gb200-pool",
        NodeShape = "BM.GPU.GB200.4",
        NodeConfigDetails = new NodePoolNodeConfigDetailsArgs
        {
            Size = nodeCount,
            PlacementConfigs =
            {
                new NodePoolNodeConfigDetailsPlacementConfigArgs
                {
                    AvailabilityDomain = availabilityDomain,
                    SubnetId = subnet.Id,
                },
            },
        },
        InitialNodeLabels =
        {
            new NodePoolInitialNodeLabelArgs
            {
                Key = "nvidia.com/gpu",
                Value = "gb200",
            },
        },
    });

    // Retrieve kubeconfig from the OKE cluster
    var kubeconfig = GetClusterKubeConfig.Invoke(new GetClusterKubeConfigInvokeArgs
    {
        ClusterId = cluster.Id,
    });

    // Deploy the NVIDIA AICR-validated GPU training stack.
    // This installs the full set of validated Helm charts including:
    //   - NVIDIA GPU Operator (driver management, device plugin, DCGM)
    //   - Kubeflow Training Operator (distributed training with TrainJob)
    //   - KAI Scheduler (GPU-aware scheduling)
    //   - Kube Prometheus Stack (monitoring with GPU metrics)
    //   - cert-manager, NVSentinel, Skyhook, and more
    //
    // The "gb200" accelerator selects the recipe family optimized for
    // NVIDIA Grace Blackwell architecture.
    var gpuStack = new ClusterStack("nvidia-aicr", new ClusterStackArgs
    {
        Kubeconfig = kubeconfig.Apply(kc => kc.Content),
        Accelerator = "gb200",
        Service = "oke",
        Intent = "training",
        Platform = "kubeflow",
    });

    return new Dictionary<string, object?>
    {
        ["recipeName"] = gpuStack.RecipeName,
        ["deployedComponents"] = gpuStack.DeployedComponents,
        ["componentCount"] = gpuStack.ComponentCount,
    };
});
