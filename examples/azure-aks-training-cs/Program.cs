// Azure AKS + NVIDIA AICR H100 Training Stack
//
// Creates an AKS cluster with H100 GPU worker nodes, then deploys the full
// AICR-validated Kubeflow training stack.
//
// COST WARNING: Standard_ND96isr_H100_v5 VMs cost ~$40/hr each. Default
// nodeCount is 2, so plan on ~$80/hr while the cluster is up.
using System;
using System.Collections.Generic;
using System.Linq;
using Pulumi;
using Pulumi.AzureNative.ContainerService;
using Pulumi.AzureNative.ContainerService.Inputs;
using Pulumi.AzureNative.Resources;
using Pulumi.NvidiaAicr;

return await Deployment.RunAsync(() =>
{
    var config = new Config();
    var clusterName = config.Get("clusterName") ?? "aicr-training";
    var nodeCount = config.GetInt32("nodeCount") ?? 2;

    var resourceGroup = new ResourceGroup("gpu-rg", new ResourceGroupArgs
    {
        ResourceGroupName = $"{clusterName}-rg",
        Tags =
        {
            ["nvidia.com/aicr"] = "true",
            ["nvidia.com/gpu"] = "h100",
        },
    });

    var cluster = new ManagedCluster(clusterName, new ManagedClusterArgs
    {
        ResourceGroupName = resourceGroup.Name,
        ResourceName = clusterName,
        DnsPrefix = clusterName,
        KubernetesVersion = "1.30",
        Identity = new ManagedClusterIdentityArgs
        {
            Type = ResourceIdentityType.SystemAssigned,
        },
        AgentPoolProfiles =
        {
            new ManagedClusterAgentPoolProfileArgs
            {
                Name = "system",
                Mode = AgentPoolMode.System,
                VmSize = "Standard_D4s_v3",
                Count = 1,
                OsType = OSType.Linux,
            },
            new ManagedClusterAgentPoolProfileArgs
            {
                Name = "gpunodes",
                Mode = AgentPoolMode.User,
                VmSize = "Standard_ND96isr_H100_v5", // 8x NVIDIA H100 80GB per node
                Count = nodeCount,
                OsType = OSType.Linux,
                NodeLabels =
                {
                    ["nvidia.com/gpu.present"] = "true",
                },
                NodeTaints = { "nvidia.com/gpu=present:NoSchedule" },
            },
        },
        Tags =
        {
            ["nvidia.com/aicr"] = "true",
            ["nvidia.com/gpu"] = "h100",
        },
    });

    // Retrieve the kubeconfig from the AKS cluster
    var kubeconfig = Output.Tuple(resourceGroup.Name, cluster.Name).Apply(t =>
    {
        var (rgName, name) = t;
        return ListManagedClusterUserCredentials.InvokeAsync(
            new ListManagedClusterUserCredentialsArgs
            {
                ResourceGroupName = rgName,
                ResourceName = name,
            });
    }).Apply(creds =>
    {
        var encoded = creds.Kubeconfigs.First().Value;
        return System.Text.Encoding.UTF8.GetString(Convert.FromBase64String(encoded));
    });

    var gpuStack = new ClusterStack("nvidia-aicr", new ClusterStackArgs
    {
        Kubeconfig = kubeconfig,
        Accelerator = "h100",
        Service = "aks",
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
    });

    return new Dictionary<string, object?>
    {
        ["kubeconfig"] = Output.CreateSecret(kubeconfig),
        ["recipeName"] = gpuStack.RecipeName,
        ["deployedComponents"] = gpuStack.DeployedComponents,
        ["componentCount"] = gpuStack.ComponentCount,
    };
});
