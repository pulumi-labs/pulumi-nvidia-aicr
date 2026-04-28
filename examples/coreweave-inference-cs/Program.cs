// CoreWeave + NVIDIA AICR H100 Inference Stack
//
// AICR doesn't ship a dedicated CoreWeave overlay yet, so this example uses
// the EKS H100 inference recipe as the closest match (standard GPU operator
// config that installs drivers, with cloud-specific add-ons skipped via
// SkipComponents).
//
// CoreWeave H100 pricing: ~$2.49/GPU/hr ($19.92/node with 8 GPUs).
using Pulumi;
using Pulumi.NvidiaAicr;

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

    return new Dictionary<string, object?>
    {
        ["recipeName"] = inferenceStack.RecipeName,
        ["deployedComponents"] = inferenceStack.DeployedComponents,
        ["componentCount"] = inferenceStack.ComponentCount,
    };
});
