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
package main

import (
	aicr "github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		intent := cfg.Get("intent")
		if intent == "" {
			intent = "inference"
		}

		gpuStack, err := aicr.NewClusterStack(ctx, "kind-aicr", &aicr.ClusterStackArgs{
			Accelerator:  "h100",
			Service:      "kind",
			Intent:       intent,
			SkipAwait:    pulumi.BoolPtr(true), // kind clusters often can't satisfy GPU readiness; don't block
			SkipComponents: pulumi.ToStringArray([]string{
				"kube-prometheus-stack",
			}),
		})
		if err != nil {
			return err
		}

		ctx.Export("recipeName", gpuStack.RecipeName)
		ctx.Export("recipeVersion", gpuStack.RecipeVersion)
		ctx.Export("deployedComponents", gpuStack.DeployedComponents)
		ctx.Export("componentCount", gpuStack.ComponentCount)
		return nil
	})
}
