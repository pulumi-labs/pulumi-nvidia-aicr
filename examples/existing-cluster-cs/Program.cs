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
using System.Collections.Generic;
using Pulumi;
using Pulumi.Labs.NvidiaAicr;
using Pulumi.Labs.NvidiaAicr.Inputs;

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

    // Validate the deployed stack empirically: a snapshot agent captures cluster
    // state, then the recipe's deployment and conformance checks run as Jobs in
    // the cluster (~10 minutes). With the default Strict = false the update always
    // succeeds and the verdict is data -- read the exported status and per-check
    // results. Set Strict = true to fail the update on failed checks instead.
    //
    // Uses the same ambient kubeconfig as the ClusterStack (neither kubeconfig
    // nor kubeconfigPath is set). Validation pods tolerate all taints by default,
    // so tainted GPU node groups need no configuration.
    var validation = new ValidationRun("aicr-validation", new ValidationRunArgs
    {
        // Keep in sync with the ClusterStack criteria (Python/TS wire the output directly; C# cannot).
        // Both read the same `pulumi config` values, so they cannot drift here.
        Criteria = new RecipeCriteriaArgs
        {
            Accelerator = config.Require("accelerator"),
            Service = config.Require("service"),
            Intent = config.Require("intent"),
            Platform = config.Get("platform"),
            Os = config.Get("os"),
        },
        RecipeDataVersion = gpuStack.RecipeVersion,   // assert same recipe data as deployed
        // Re-validate when the stack changes
        Triggers = gpuStack.DeployedComponents.Apply(cs => new object[] { cs }),
    });

    return new Dictionary<string, object?>
    {
        ["recipeName"] = gpuStack.RecipeName,
        ["recipeVersion"] = gpuStack.RecipeVersion,
        ["deployedComponents"] = gpuStack.DeployedComponents,
        ["componentCount"] = gpuStack.ComponentCount,
        ["validationStatus"] = validation.Status,
        ["validationChecks"] = validation.PhaseResults,
    };
});
