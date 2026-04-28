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
import com.pulumi.nvidiaaicr.ClusterStack;
import com.pulumi.nvidiaaicr.ClusterStackArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    private static void stack(Context ctx) {
        var config = ctx.config();

        var gpuStack = new ClusterStack("aicr", ClusterStackArgs.builder()
            .accelerator(config.require("accelerator"))
            .service(config.require("service"))
            .intent(config.require("intent"))
            .platform(config.get("platform").orElse(null))
            .os(config.get("os").orElse(null))
            .skipAwait(config.getBoolean("skipAwait").orElse(false))
            .build());

        ctx.export("recipeName", gpuStack.recipeName());
        ctx.export("recipeVersion", gpuStack.recipeVersion());
        ctx.export("deployedComponents", gpuStack.deployedComponents());
        ctx.export("componentCount", gpuStack.componentCount());
    }
}
