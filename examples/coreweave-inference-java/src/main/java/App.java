// CoreWeave + NVIDIA AICR H100 Inference Stack
//
// AICR doesn't ship a dedicated CoreWeave overlay yet, so this example uses
// the EKS H100 inference recipe as the closest match (standard GPU operator
// config that installs drivers, with cloud-specific add-ons skipped via
// skipComponents).
//
// CoreWeave H100 pricing: ~$2.49/GPU/hr ($19.92/node with 8 GPUs).

import java.util.List;
import java.util.Map;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.nvidiaaicr.ClusterStack;
import com.pulumi.nvidiaaicr.ClusterStackArgs;
import com.pulumi.nvidiaaicr.inputs.ComponentOverrideArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    private static void stack(Context ctx) {
        var config = ctx.config();
        var kubeconfigPath = config.get("kubeconfigPath").orElse("~/.kube/config");

        var inferenceStack = new ClusterStack("nvidia-inference", ClusterStackArgs.builder()
            .kubeconfigPath(kubeconfigPath)
            .accelerator("h100")
            .service("eks") // closest match; cloud-specific add-ons skipped below
            .intent("inference")
            .platform("dynamo")
            .skipComponents(List.of("aws-efa", "aws-ebs-csi-driver"))
            .componentOverrides(Map.of(
                "dynamo-platform", ComponentOverrideArgs.builder()
                    .values(Map.of(
                        "etcd", Map.of(
                            "persistence", Map.of("storageClass", "coreweave-ssd")),
                        "nats", Map.of(
                            "config", Map.of(
                                "jetstream", Map.of(
                                    "fileStore", Map.of(
                                        "pvc", Map.of("storageClassName", "coreweave-ssd")))))))
                    .build()))
            .build());

        ctx.export("recipeName", inferenceStack.recipeName());
        ctx.export("deployedComponents", inferenceStack.deployedComponents());
        ctx.export("componentCount", inferenceStack.componentCount());
    }
}
