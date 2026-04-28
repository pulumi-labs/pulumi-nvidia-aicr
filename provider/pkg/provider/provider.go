package provider

import (
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi-go-provider/middleware/schema"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// NewProvider returns the NVIDIA AICR Pulumi provider.
func NewProvider() p.Provider {
	return infer.Provider(infer.Options{
		Metadata: schema.Metadata{
			DisplayName: "NVIDIA AI Cluster Runtime",
			Description: "Deploy validated NVIDIA AI Cluster Runtime (AICR) recipes for GPU-accelerated Kubernetes clusters.",
			Keywords:    []string{"pulumi", "nvidia", "aicr", "gpu", "kubernetes", "category/cloud"},
			Homepage:    "https://github.com/pulumi-labs/pulumi-nvidia-aicr",
			Repository:  "https://github.com/pulumi-labs/pulumi-nvidia-aicr",
			Publisher:   "Pulumi",
			LogoURL:     "https://raw.githubusercontent.com/pulumi-labs/pulumi-nvidia-aicr/main/sdk/dotnet/logo.png",
			License:     "Apache-2.0",
			LanguageMap: map[string]any{
				"go": map[string]any{
					"importBasePath":                 "github.com/pulumi-labs/pulumi-nvidia-aicr/sdk/go/nvidiaaicr",
					"generateResourceContainerTypes": true,
					"respectSchemaVersion":           true,
				},
				"nodejs": map[string]any{
					"packageName":          "@pulumi/nvidia-aicr",
					"packageDescription":   "Deploy validated NVIDIA AI Cluster Runtime (AICR) recipes for GPU-accelerated Kubernetes clusters.",
					"respectSchemaVersion": true,
					"dependencies": map[string]any{
						"@pulumi/pulumi": "^3.142.0",
					},
					"devDependencies": map[string]any{
						"typescript":  "^4.3.5",
						"@types/node": "^18",
					},
				},
				"python": map[string]any{
					"packageName":          "pulumi_nvidia_aicr",
					"respectSchemaVersion": true,
					"requires": map[string]any{
						"pulumi": ">=3.165.0,<4.0.0",
					},
					"pyproject": map[string]any{
						"enabled": true,
					},
				},
				"csharp": map[string]any{
					"rootNamespace":        "Pulumi",
					"respectSchemaVersion": true,
					"packageReferences": map[string]any{
						"Pulumi": "3.*",
					},
				},
				"java": map[string]any{
					"basePackage":          "com.pulumi",
					"respectSchemaVersion": true,
					"dependencies": map[string]any{
						"com.pulumi:pulumi": "1.+",
					},
				},
			},
		},
		Components: []infer.InferredComponent{
			infer.ComponentF(NewClusterStack),
		},
		ModuleMap: map[tokens.ModuleName]tokens.ModuleName{
			"provider": "index",
		},
	})
}
