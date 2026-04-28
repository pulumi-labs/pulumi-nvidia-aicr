package main

import (
	"context"
	"fmt"
	"os"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/provider"
	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/version"
)

func main() {
	v := version.Version
	if v == "" {
		v = "0.0.1-dev"
	}
	err := p.RunProvider(context.Background(), "nvidia-aicr", v, provider.NewProvider())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
