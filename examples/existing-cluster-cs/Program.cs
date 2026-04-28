// AICR Quickstart — Deploy on an Existing Kubernetes Cluster
//
// The simplest way to deploy NVIDIA AICR. Uses your ambient kubeconfig
// (~/.kube/config) and configurable criteria via `pulumi config`.
//
// Usage:
//   pulumi config set accelerator h100
//   pulumi config set service eks
//   pulumi config set intent training
//   pulumi up
using Pulumi;
using Pulumi.NvidiaAicr;

return await Deployment.RunAsync(() =>
{
    var config = new Config();

    var gpuStack = new ClusterStack("aicr", new ClusterStackArgs
    {
        // Uses ambient kubeconfig when no kubeconfig/kubeconfigPath is set
        Accelerator = config.Require("accelerator"),
        Service = config.Require("service"),
        Intent = config.Require("intent"),
        Platform = config.Get("platform"),
        Os = config.Get("os"),
        SkipAwait = config.GetBoolean("skipAwait") ?? false,
    });

    return new Dictionary<string, object?>
    {
        ["recipeName"] = gpuStack.RecipeName,
        ["recipeVersion"] = gpuStack.RecipeVersion,
        ["deployedComponents"] = gpuStack.DeployedComponents,
        ["componentCount"] = gpuStack.ComponentCount,
    };
});
