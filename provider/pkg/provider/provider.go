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
			Homepage:    "https://github.com/pulumi/pulumi-nvidia-aicr",
			Repository:  "https://github.com/pulumi/pulumi-nvidia-aicr",
			Publisher:   "Pulumi",
			License:     "Apache-2.0",
			LanguageMap: map[string]any{
				"go": map[string]any{
					"importBasePath":                 "github.com/pulumi/pulumi-nvidia-aicr/sdk/go/nvidiaaicr",
					"generateResourceContainerTypes": true,
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
