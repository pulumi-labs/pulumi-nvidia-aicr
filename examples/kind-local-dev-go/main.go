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
			SkipAwait:    pulumi.BoolRef(true), // kind clusters often can't satisfy GPU readiness; don't block
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

		// Optional: run the recipe's empirical validation against the cluster
		// (config: `pulumi config set validate true`). Adds ~10 minutes to the update.
		//
		// Honest expectations on a GPU-less kind cluster: the readiness pre-flight
		// passes (kind recipes bind no OS or GPU constraints), but deployment health
		// checks for GPU components fail or skip, and GPU conformance checks skip --
		// there is no GPU to validate. Useful for exercising the validation pipeline
		// itself; a real verdict needs real hardware (see the EKS/GKE examples).
		if cfg.GetBool("validate") {
			validation, err := aicr.NewValidationRun(ctx, "kind-validation", &aicr.ValidationRunArgs{
				// Keep in sync with the ClusterStack criteria above (Python/TS wire the stack's `criteria` output directly; Go cannot).
				Criteria: aicr.RecipeCriteriaArgs{
					Accelerator: pulumi.String("h100"),
					Service:     pulumi.String("kind"),
					Intent:      pulumi.String(intent),
				},
				RecipeDataVersion: gpuStack.RecipeVersion,                    // assert same recipe data as deployed
				RequireGpu: pulumi.Bool(false),                        // kind has no GPU nodes
				Triggers:   pulumi.Array{gpuStack.DeployedComponents}, // re-run when the stack changes
			})
			if err != nil {
				return err
			}
			ctx.Export("validationStatus", validation.Status)
			ctx.Export("validationChecks", validation.PhaseResults)
		}
		return nil
	})
}
