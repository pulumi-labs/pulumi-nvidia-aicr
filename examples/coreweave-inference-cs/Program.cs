// CoreWeave + NVIDIA AICR H100 Inference Stack
//
// AICR doesn't ship a dedicated CoreWeave overlay yet, so this example uses
// the EKS H100 inference recipe as the closest match (standard GPU operator
// config that installs drivers, with cloud-specific add-ons skipped via
// SkipComponents).
//
// CoreWeave H100 pricing: ~$2.49/GPU/hr ($19.92/node with 8 GPUs).
using System.Collections.Generic;
using Pulumi;
using Pulumi.Labs.NvidiaAicr;
using Pulumi.Labs.NvidiaAicr.Inputs;

return await Deployment.RunAsync(() =>
{
    var config = new Config();
    var kubeconfigPath = config.Get("kubeconfigPath") ?? "~/.kube/config";

    var inferenceStack = new ClusterStack("nvidia-inference", new ClusterStackArgs
    {
        KubeconfigPath = kubeconfigPath,
        Accelerator = "h100",
        Service = "eks", // closest match; cloud-specific add-ons skipped below
        Intent = "inference",
        Platform = "dynamo",
        Os = "ubuntu",
        SkipComponents = { "aws-efa", "aws-ebs-csi-driver" },
        ComponentOverrides =
        {
            ["dynamo-platform"] = new ComponentOverrideArgs
            {
                Values = new InputMap<object>
                {
                    ["etcd"] = new Dictionary<string, object>
                    {
                        ["persistence"] = new Dictionary<string, object>
                        {
                            ["storageClass"] = "coreweave-ssd",
                        },
                    },
                    ["nats"] = new Dictionary<string, object>
                    {
                        ["config"] = new Dictionary<string, object>
                        {
                            ["jetstream"] = new Dictionary<string, object>
                            {
                                ["fileStore"] = new Dictionary<string, object>
                                {
                                    ["pvc"] = new Dictionary<string, object>
                                    {
                                        ["storageClassName"] = "coreweave-ssd",
                                    },
                                },
                            },
                        },
                    },
                },
            },
        },
    });

    // Validate the deployed stack empirically: a snapshot agent captures cluster
    // state, then the recipe's deployment and conformance checks run as Jobs in
    // the cluster (~10 minutes). With the default Strict = false the update always
    // succeeds and the verdict is data -- read the exported status and per-check
    // results. Set Strict = true to fail the update on failed checks instead.
    //
    // Validation pods tolerate all taints by default, so tainted GPU node groups
    // need no configuration; the Tolerations input exists to narrow that.
    var validation = new ValidationRun("nvidia-aicr-validation", new ValidationRunArgs
    {
        // Keep in sync with the ClusterStack criteria (Python/TS wire the output directly; C# cannot).
        Criteria = new RecipeCriteriaArgs
        {
            Accelerator = "h100",
            Service = "eks", // closest match, same as the ClusterStack above
            Intent = "inference",
            Platform = "dynamo",
            Os = "ubuntu",
        },
        RecipeDataVersion = inferenceStack.RecipeVersion,   // assert same recipe data as deployed
        KubeconfigPath = kubeconfigPath,
        // Re-validate when the stack changes
        Triggers = inferenceStack.DeployedComponents.Apply(cs => new object[] { cs }),
    });

    return new Dictionary<string, object?>
    {
        ["recipeName"] = inferenceStack.RecipeName,
        ["deployedComponents"] = inferenceStack.DeployedComponents,
        ["componentCount"] = inferenceStack.ComponentCount,
        ["validationStatus"] = validation.Status,
        ["validationChecks"] = validation.PhaseResults,
    };
});
