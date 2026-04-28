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
	aicr "github.com/pulumi/pulumi-nvidia-aicr/sdk/go/nvidiaaicr"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")

		args := &aicr.ClusterStackArgs{
			// Uses ambient kubeconfig when no Kubeconfig/KubeconfigPath is set
			Accelerator: cfg.Require("accelerator"),
			Service:     cfg.Require("service"),
			Intent:      cfg.Require("intent"),
		}

		// Optional fields
		if platform := cfg.Get("platform"); platform != "" {
			args.Platform = pulumi.StringPtr(platform)
		}
		if os := cfg.Get("os"); os != "" {
			args.Os = pulumi.StringPtr(os)
		}
		if skipAwait := cfg.GetBool("skipAwait"); skipAwait {
			args.SkipAwait = pulumi.BoolPtr(true)
		}

		gpuStack, err := aicr.NewClusterStack(ctx, "aicr", args)
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
