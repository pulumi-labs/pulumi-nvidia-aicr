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

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.core.Output;
import com.pulumi.labs.nvidiaaicr.ClusterStack;
import com.pulumi.labs.nvidiaaicr.ClusterStackArgs;
import com.pulumi.labs.nvidiaaicr.ValidationRun;
import com.pulumi.labs.nvidiaaicr.ValidationRunArgs;
import com.pulumi.labs.nvidiaaicr.inputs.RecipeCriteriaArgs;
import java.util.Map;
import java.util.stream.Collectors;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    private static void stack(Context ctx) {
        var config = ctx.config();
        var accelerator = config.require("accelerator");
        var service = config.require("service");
        var intent = config.require("intent");
        var platform = config.get("platform");
        var os = config.get("os");

        var gpuStack = new ClusterStack("aicr", ClusterStackArgs.builder()
            .accelerator(accelerator)
            .service(service)
            .intent(intent)
            .platform(platform.orElse(null))
            .os(os.orElse(null))
            .skipAwait(config.getBoolean("skipAwait").orElse(false))
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
        //
        // Keep in sync with the ClusterStack criteria above (Python/TS wire the
        // stack's `criteria` output directly; Java cannot) -- built here from the
        // same config values.
        var criteria = RecipeCriteriaArgs.builder()
            .accelerator(accelerator)
            .service(service)
            .intent(intent);
        platform.ifPresent(criteria::platform);
        os.ifPresent(criteria::os);

        var validation = new ValidationRun("aicr-validation", ValidationRunArgs.builder()
            .criteria(criteria.build())
            .recipeDataVersion(gpuStack.recipeVersion())        // assert same recipe data as deployed
            // No kubeconfig: uses the same ambient kubeconfig as the ClusterStack.
            .triggers(gpuStack.deployedComponents())  // re-validate when the stack changes
            .build());

        ctx.export("recipeName", gpuStack.recipeName());
        ctx.export("recipeVersion", gpuStack.recipeVersion());
        ctx.export("deployedComponents", gpuStack.deployedComponents());
        ctx.export("componentCount", gpuStack.componentCount());
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
