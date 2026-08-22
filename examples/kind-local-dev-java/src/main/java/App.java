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

import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.labs.nvidiaaicr.ClusterStack;
import com.pulumi.labs.nvidiaaicr.ClusterStackArgs;
import com.pulumi.labs.nvidiaaicr.ValidationRun;
import com.pulumi.labs.nvidiaaicr.ValidationRunArgs;
import com.pulumi.labs.nvidiaaicr.inputs.RecipeCriteriaArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    private static void stack(Context ctx) {
        var config = ctx.config();
        var intent = config.get("intent").orElse("inference");

        var gpuStack = new ClusterStack("kind-aicr", ClusterStackArgs.builder()
            .accelerator("h100")
            .service("kind")
            .intent(intent)
            .skipAwait(true) // kind clusters often can't satisfy GPU readiness; don't block
            .skipComponents(List.of(
                "kube-prometheus-stack"
            ))
            .build());

        ctx.export("recipeName", gpuStack.recipeName());
        ctx.export("recipeVersion", gpuStack.recipeVersion());
        ctx.export("deployedComponents", gpuStack.deployedComponents());
        ctx.export("componentCount", gpuStack.componentCount());

        // Optional: run the recipe's empirical validation against the cluster
        // (config: `pulumi config set validate true`). Adds ~10 minutes to the update.
        //
        // Honest expectations on a GPU-less kind cluster: the readiness pre-flight
        // passes (kind recipes bind no OS or GPU constraints), but deployment health
        // checks for GPU components fail or skip, and GPU conformance checks skip --
        // there is no GPU to validate. Useful for exercising the validation pipeline
        // itself; a real verdict needs real hardware (see the EKS/GKE examples).
        if (config.getBoolean("validate").orElse(false)) {
            var validation = new ValidationRun("kind-validation", ValidationRunArgs.builder()
                // Keep in sync with the ClusterStack criteria above (Python/TS wire
                // the stack's `criteria` output directly; Java cannot).
                .criteria(RecipeCriteriaArgs.builder()
                    .accelerator("h100")
                    .service("kind")
                    .intent(intent)
                    .build())
                .recipeDataVersion(gpuStack.recipeVersion())        // assert same recipe data as deployed
                .requireGpu(false)                        // kind has no GPU nodes
                .triggers(gpuStack.deployedComponents())  // re-run when the stack changes
                .build());
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
}
