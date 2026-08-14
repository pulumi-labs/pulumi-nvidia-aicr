// AICR Quickstart -- Deploy on an Existing Kubernetes Cluster
//
// The simplest way to deploy NVIDIA AICR. Uses your ambient kubeconfig
// (~/.kube/config) and configurable criteria via `pulumi config`.
//
// Usage:
//
//	pulumi config set accelerator h100
//	pulumi config set service eks
//	pulumi config set intent training
//	pulumi up
package main

import (
	aicr "github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		accelerator := cfg.Require("accelerator")
		service := cfg.Require("service")
		intent := cfg.Require("intent")

		args := &aicr.ClusterStackArgs{
			// Uses ambient kubeconfig when no Kubeconfig/KubeconfigPath is set
			Accelerator: accelerator,
			Service:     service,
			Intent:      intent,
		}

		// Keep in sync with the ClusterStack criteria (Python/TS wire the stack's `criteria` output directly; Go cannot).
		criteria := aicr.RecipeCriteriaArgs{
			Accelerator: pulumi.String(accelerator),
			Service:     pulumi.String(service),
			Intent:      pulumi.String(intent),
		}

		// Optional fields
		if platform := cfg.Get("platform"); platform != "" {
			args.Platform = pulumi.StringRef(platform)
			criteria.Platform = pulumi.StringPtr(platform)
		}
		if os := cfg.Get("os"); os != "" {
			args.Os = pulumi.StringRef(os)
			criteria.Os = pulumi.StringPtr(os)
		}
		if skipAwait := cfg.GetBool("skipAwait"); skipAwait {
			args.SkipAwait = pulumi.BoolRef(true)
		}

		gpuStack, err := aicr.NewClusterStack(ctx, "aicr", args)
		if err != nil {
			return err
		}

		// Validate the deployed stack empirically: a snapshot agent captures cluster
		// state, then the recipe's deployment and conformance checks run as Jobs in
		// the cluster (~10 minutes). Uses the same ambient kubeconfig as the
		// ClusterStack. With the default strict=false the update always succeeds
		// and the verdict is data -- read the exported status and per-check
		// results. Set Strict: pulumi.Bool(true) to fail the update on failed
		// checks instead.
		//
		// Validation pods tolerate all taints by default, so tainted GPU node groups
		// need no configuration; the `Tolerations` input exists to narrow that.
		validation, err := aicr.NewValidationRun(ctx, "aicr-validation", &aicr.ValidationRunArgs{
			Criteria:          criteria,                                  // same recipe criteria as the stack
			RecipeDataVersion: gpuStack.RecipeVersion,                    // assert same recipe data as deployed
			Triggers:          pulumi.Array{gpuStack.DeployedComponents}, // re-validate when the stack changes
		})
		if err != nil {
			return err
		}

		ctx.Export("recipeName", gpuStack.RecipeName)
		ctx.Export("recipeVersion", gpuStack.RecipeVersion)
		ctx.Export("deployedComponents", gpuStack.DeployedComponents)
		ctx.Export("componentCount", gpuStack.ComponentCount)
		ctx.Export("validationStatus", validation.Status)
		ctx.Export("validationChecks", validation.PhaseResults)
		return nil
	})
}
