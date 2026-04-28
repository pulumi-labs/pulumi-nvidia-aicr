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
using Pulumi;
using Pulumi.NvidiaAicr;

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

    return new Dictionary<string, object?>
    {
        ["recipeName"] = stack.RecipeName,
        ["recipeVersion"] = stack.RecipeVersion,
        ["deployedComponents"] = stack.DeployedComponents,
        ["componentCount"] = stack.ComponentCount,
    };
});
