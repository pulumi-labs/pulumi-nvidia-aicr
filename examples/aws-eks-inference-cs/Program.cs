// AWS EKS + NVIDIA AICR H100 vLLM Inference Stack
//
// Creates an EKS cluster with H100 GPU worker nodes, then deploys the full
// AICR-validated NIM inference stack for vLLM model serving.
//
// COST WARNING: p5.48xlarge instances cost ~$98.32/hr each. Default
// nodeCount is 2, so plan on ~$196/hr while the cluster is up.
using Pulumi;
using Pulumi.Aws.Ec2;
using Pulumi.Aws.Ec2.Inputs;
using Pulumi.Eks;
using Pulumi.NvidiaAicr;

return await Deployment.RunAsync(() =>
{
    var config = new Config();
    var clusterName = config.Get("clusterName") ?? "aicr-inference";
    var nodeCount = config.GetInt32("nodeCount") ?? 2;

    var vpc = new Vpc("gpu-vpc", new VpcArgs
    {
        CidrBlock = "10.0.0.0/16",
        EnableDnsHostnames = true,
        EnableDnsSupport = true,
        Tags = { ["Name"] = $"{clusterName}-vpc" },
    });

    var publicSubnet1 = new Subnet("public-1", new SubnetArgs
    {
        VpcId = vpc.Id,
        CidrBlock = "10.0.1.0/24",
        AvailabilityZone = "us-east-1a",
        MapPublicIpOnLaunch = true,
        Tags = { ["Name"] = $"{clusterName}-public-1" },
    });

    var publicSubnet2 = new Subnet("public-2", new SubnetArgs
    {
        VpcId = vpc.Id,
        CidrBlock = "10.0.2.0/24",
        AvailabilityZone = "us-east-1b",
        MapPublicIpOnLaunch = true,
        Tags = { ["Name"] = $"{clusterName}-public-2" },
    });

    var igw = new InternetGateway("igw", new InternetGatewayArgs
    {
        VpcId = vpc.Id,
    });

    var routeTable = new RouteTable("public-rt", new RouteTableArgs
    {
        VpcId = vpc.Id,
        Routes =
        {
            new RouteTableRouteArgs
            {
                CidrBlock = "0.0.0.0/0",
                GatewayId = igw.Id,
            },
        },
    });

    new RouteTableAssociation("public-1-rta", new RouteTableAssociationArgs
    {
        SubnetId = publicSubnet1.Id,
        RouteTableId = routeTable.Id,
    });
    new RouteTableAssociation("public-2-rta", new RouteTableAssociationArgs
    {
        SubnetId = publicSubnet2.Id,
        RouteTableId = routeTable.Id,
    });

    var cluster = new Cluster(clusterName, new ClusterArgs
    {
        VpcId = vpc.Id,
        SubnetIds = { publicSubnet1.Id, publicSubnet2.Id },
        InstanceType = "p5.48xlarge",   // 8x NVIDIA H100 80GB per node
        DesiredCapacity = nodeCount,
        MinSize = 1,
        MaxSize = nodeCount * 2,
        NodeAssociatePublicIpAddress = false,
        CreateOidcProvider = true,
        Tags =
        {
            ["nvidia.com/aicr"] = "true",
            ["nvidia.com/gpu"] = "h100",
        },
    });

    var gpuStack = new ClusterStack("nvidia-aicr", new ClusterStackArgs
    {
        Kubeconfig = cluster.KubeconfigJson,
        Accelerator = "h100",
        Service = "eks",
        Intent = "inference",
        Platform = "nim",
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
    });

    return new Dictionary<string, object?>
    {
        ["kubeconfig"] = Output.CreateSecret(cluster.KubeconfigJson),
        ["recipeName"] = gpuStack.RecipeName,
        ["deployedComponents"] = gpuStack.DeployedComponents,
        ["componentCount"] = gpuStack.ComponentCount,
    };
});
