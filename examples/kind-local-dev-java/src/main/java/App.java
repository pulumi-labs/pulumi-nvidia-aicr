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

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.nvidiaaicr.ClusterStack;
import com.pulumi.nvidiaaicr.ClusterStackArgs;

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
    }
}
