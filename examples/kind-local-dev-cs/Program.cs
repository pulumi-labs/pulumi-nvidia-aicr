// AICR on a local kind cluster -- for development of the deployment pipeline
// without real GPU hardware.
//
// Prerequisites:
//   - kind installed and a cluster running:
//       kind create cluster --name aicr-dev
//   - kubectl context pointing at it (kind sets this automatically)
//
// The `kind` overlay disables driver installation and several other
// GPU-Operator subcomponents that would otherwise hang in a kind cluster.
// Many GPU pods will not actually be Ready, but the Helm releases will
// install -- which is enough for iterating on the deployment graph.
using System.Collections.Generic;
using Pulumi;
using Pulumi.Labs.NvidiaAicr;
using Pulumi.Labs.NvidiaAicr.Inputs;

return await Deployment.RunAsync(() =>
{
    var config = new Config();
    var intent = config.Get("intent") ?? "inference";

    var stack = new ClusterStack("kind-aicr", new ClusterStackArgs
    {
        Accelerator = "h100",
        Service = "kind",
        Intent = intent,
        SkipAwait = true, // kind clusters often can't satisfy GPU readiness; don't block
        SkipComponents = new[]
        {
            "kube-prometheus-stack",
        },
    });

    var outputs = new Dictionary<string, object?>
    {
        ["recipeName"] = stack.RecipeName,
        ["recipeVersion"] = stack.RecipeVersion,
        ["deployedComponents"] = stack.DeployedComponents,
        ["componentCount"] = stack.ComponentCount,
    };

    // Optional: run the recipe's empirical validation against the cluster
    // (config: `pulumi config set validate true`). Adds ~10 minutes to the update.
    //
    // Honest expectations on a GPU-less kind cluster: the readiness pre-flight
    // passes (kind recipes bind no OS or GPU constraints), but deployment health
    // checks for GPU components fail or skip, and GPU conformance checks skip --
    // there is no GPU to validate. Useful for exercising the validation pipeline
    // itself; a real verdict needs real hardware (see the EKS/GKE examples).
    if (config.GetBoolean("validate") ?? false)
    {
        var validation = new ValidationRun("kind-validation", new ValidationRunArgs
        {
            // Keep in sync with the ClusterStack criteria (Python/TS wire the output directly; C# cannot).
            Criteria = new RecipeCriteriaArgs
            {
                Accelerator = "h100",
                Service = "kind",
                Intent = intent,
            },
            RecipeDataVersion = stack.RecipeVersion,   // assert same recipe data as deployed
            RequireGpu = false,              // kind has no GPU nodes
            // Re-run when the stack changes
            Triggers = stack.DeployedComponents.Apply(cs => new object[] { cs }),
        });
        outputs["validationStatus"] = validation.Status;
        outputs["validationChecks"] = validation.PhaseResults;
    }

    return outputs;
});
