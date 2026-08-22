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
import java.util.stream.Collectors;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.labs.nvidiaaicr.ClusterStack;
import com.pulumi.labs.nvidiaaicr.ClusterStackArgs;
import com.pulumi.labs.nvidiaaicr.ValidationRun;
import com.pulumi.labs.nvidiaaicr.ValidationRunArgs;
import com.pulumi.labs.nvidiaaicr.inputs.ComponentOverrideArgs;
import com.pulumi.labs.nvidiaaicr.inputs.RecipeCriteriaArgs;

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
            .os("ubuntu")
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

        // Validate the deployed stack empirically: a snapshot agent captures
        // cluster state, then the recipe's deployment and conformance checks run
        // as Jobs in the cluster (~10 minutes). With the default strict=false the
        // update always succeeds and the verdict is data -- read the exported
        // status and per-check results. Set strict=true to fail the update on
        // failed checks instead.
        //
        // Validation pods tolerate all taints by default, so tainted GPU node
        // groups need no configuration; the `tolerations` input exists to narrow
        // that.
        var validation = new ValidationRun("nvidia-inference-validation", ValidationRunArgs.builder()
            // Keep in sync with the ClusterStack criteria above (Python/TS wire
            // the stack's `criteria` output directly; Java cannot).
            .criteria(RecipeCriteriaArgs.builder()
                .accelerator("h100")
                .service("eks") // closest match, same as the ClusterStack
                .intent("inference")
                .platform("dynamo")
                .os("ubuntu")
                .build())
            .recipeDataVersion(inferenceStack.recipeVersion())        // assert same recipe data as deployed
            .kubeconfigPath(kubeconfigPath)                 // same kubeconfig as the ClusterStack
            .triggers(inferenceStack.deployedComponents())  // re-validate when the stack changes
            .build());

        ctx.export("recipeName", inferenceStack.recipeName());
        ctx.export("deployedComponents", inferenceStack.deployedComponents());
        ctx.export("componentCount", inferenceStack.componentCount());
        ctx.export("validationStatus", validation.status());
        // pulumi-java cannot serialize generated output types (CheckResult) as
        // stack outputs -- project to plain maps first.
        ctx.export("validationChecks", validation.phaseResults().applyValue(rs ->
            rs.stream().map(c -> Map.of(
                "name", c.name(),
                "phase", c.phase(),
                "status", c.status(),
                "message", c.message()))
              .collect(Collectors.toList())));
    }
}
