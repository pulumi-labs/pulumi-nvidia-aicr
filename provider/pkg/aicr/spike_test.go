package aicr

// Phase 1 spike: prove we can reach the SDK's embedded recipe data and
// inspect the component/bundle/ordering shape. Deleted once the real
// adapter tests exist.

import (
	"context"
	"testing"

	aicrclient "github.com/NVIDIA/aicr/pkg/client/v1"
)

func TestSpikeResolveAndBundle(t *testing.T) {
	client, err := aicrclient.NewClient(
		aicrclient.WithRecipeSource(aicrclient.EmbeddedSource()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	result, err := client.ResolveRecipe(ctx, aicrclient.RecipeRequest{
		Service:     "eks",
		Accelerator: "h100",
		Intent:      "training",
		OS:          "ubuntu",
		Platform:    "kubeflow",
	})
	if err != nil {
		t.Fatalf("ResolveRecipe: %v", err)
	}

	t.Logf("Name=%q Version=%q components=%d", result.Name, result.Version, len(result.Components))

	internal := result.Resolved()
	t.Logf("DeploymentOrder=%v", internal.DeploymentOrder)
	for _, ref := range internal.ComponentRefs {
		t.Logf("internal ref %s: deps=%v manifests=%v preManifests=%v",
			ref.Name, ref.DependencyRefs, ref.ManifestFiles, ref.PreManifestFiles)
	}

	bundles, err := client.BundleComponents(ctx, result)
	if err != nil {
		t.Fatalf("BundleComponents: %v", err)
	}
	for _, b := range bundles {
		c := b.Component
		t.Logf("bundle %s: kind=%s chart=%q source=%q version=%q ns=%q values=%dB manifests=%dB",
			c.Name, c.Kind, c.Chart, c.Source, c.Version, c.Namespace,
			len(b.HelmValues), len(b.Manifests))
	}
}

func TestSpikeOSUnsetVsUbuntu(t *testing.T) {
	client, err := aicrclient.NewClient(
		aicrclient.WithRecipeSource(aicrclient.EmbeddedSource()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	for _, os := range []string{"", "ubuntu"} {
		result, err := client.ResolveRecipe(context.Background(), aicrclient.RecipeRequest{
			Service: "eks", Accelerator: "h100", Intent: "training", OS: os, Platform: "kubeflow",
		})
		if err != nil {
			t.Errorf("os=%q: %v", os, err)
			continue
		}
		internal := result.Resolved()
		t.Logf("os=%q -> name=%q version=%q GetVersion=%q order=%v",
			os, result.Name, result.Version, internal.GetVersion(), internal.DeploymentOrder)
		t.Logf("os=%q metadata=%+v", os, internal.Metadata)
	}
}

func TestSpikeGB200PreManifests(t *testing.T) {
	client, err := aicrclient.NewClient(
		aicrclient.WithRecipeSource(aicrclient.EmbeddedSource()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	result, err := client.ResolveRecipe(context.Background(), aicrclient.RecipeRequest{
		Service: "eks", Accelerator: "gb200", Intent: "training",
	})
	if err != nil {
		t.Fatalf("ResolveRecipe: %v", err)
	}
	for _, ref := range result.Resolved().ComponentRefs {
		if len(ref.PreManifestFiles) > 0 || len(ref.ManifestFiles) > 0 {
			t.Logf("%s: pre=%v post=%v", ref.Name, ref.PreManifestFiles, ref.ManifestFiles)
		}
	}
}

func TestSpikeMatrix(t *testing.T) {
	client, err := aicrclient.NewClient(
		aicrclient.WithRecipeSource(aicrclient.EmbeddedSource()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	type combo struct{ svc, acc, intent, os, plat string }
	var combos []combo
	for _, svc := range []string{"aks", "eks", "gke", "kind", "oke"} {
		for _, acc := range []string{"h100", "gb200", "b200"} {
			for _, intent := range []string{"training", "inference"} {
				for _, os := range []string{"", "ubuntu"} {
					combos = append(combos, combo{svc, acc, intent, os, ""})
				}
			}
		}
	}
	combos = append(combos,
		combo{"gke", "h100", "training", "cos", ""},
		combo{"gke", "h100", "inference", "cos", ""},
		combo{"eks", "h100", "inference", "ubuntu", "nim"},
		combo{"eks", "h100", "inference", "", "nim"},
		combo{"eks", "h100", "inference", "ubuntu", "dynamo"},
		combo{"eks", "h100", "inference", "", "dynamo"},
		combo{"kind", "h100", "inference", "", "dynamo"},
	)

	for _, c := range combos {
		result, err := client.ResolveRecipe(context.Background(), aicrclient.RecipeRequest{
			Service: c.svc, Accelerator: c.acc, Intent: c.intent, OS: c.os, Platform: c.plat,
		})
		if err != nil {
			t.Logf("FAIL %s/%s/%s/os=%q/plat=%q: %v", c.svc, c.acc, c.intent, c.os, c.plat, err)
			continue
		}
		m := result.Resolved().Metadata
		leaf := ""
		if n := len(m.AppliedOverlays); n > 0 {
			leaf = m.AppliedOverlays[n-1]
		}
		t.Logf("OK   %s/%s/%s/os=%q/plat=%q -> leaf=%s n=%d", c.svc, c.acc, c.intent, c.os, c.plat, leaf, len(result.Components))
	}
}

func TestSpikeKindResolves(t *testing.T) {
	client, err := aicrclient.NewClient(
		aicrclient.WithRecipeSource(aicrclient.EmbeddedSource()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	for _, req := range []aicrclient.RecipeRequest{
		{Service: "kind", Accelerator: "h100", Intent: "inference"},
		{Service: "kind", Accelerator: "h100", Intent: "training"},
		{Service: "kind", Accelerator: "h100", Intent: "inference", Platform: "dynamo"},
		{Service: "kind", Accelerator: "h100", Intent: "inference", OS: "ubuntu"},
	} {
		result, err := client.ResolveRecipe(context.Background(), req)
		if err != nil {
			t.Errorf("ResolveRecipe(%+v): %v", req, err)
			continue
		}
		names := make([]string, 0, len(result.Components))
		for _, c := range result.Components {
			names = append(names, c.Name)
		}
		t.Logf("req=%+v -> %q (%d): %v", req, result.Name, len(names), names)
	}
}
