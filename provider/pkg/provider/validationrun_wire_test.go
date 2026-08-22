package provider

// Wire-level lifecycle tests: drive the provider through the integration
// server so property values take the real encode/decode path (property.Map
// round-trips) that the struct-level mock tests bypass. Regression guard for
// the live-only spurious `~version` replace diff observed 2026-08-13 on the
// EKS rig: every preview after a successful create reported
// `version: "v0.18.0" => "v0.18.0"` as UpdateReplace.

import (
	"context"
	"testing"

	"github.com/blang/semver"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	presource "github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi-labs/pulumi-nvidia-aicr/provider/pkg/aicr"
)

const wireURN = presource.URN("urn:pulumi:dev::wire::nvidia-aicr:index:ValidationRun::vr")

func newWireServer(t *testing.T) integration.Server {
	t.Helper()
	s, err := integration.NewServer(context.Background(),
		"nvidia-aicr", semver.MustParse("0.0.1"),
		integration.WithProvider(NewProvider()),
	)
	require.NoError(t, err)
	return s
}

// rigLikeInputs mirrors the live EKS rig's program inputs: criteria object,
// version assertion, secret kubeconfig contents, explicit requireGpu, and
// triggers containing one nested string-array element.
func rigLikeInputs() property.Map {
	return property.NewMap(map[string]property.Value{
		"criteria": property.New(property.NewMap(map[string]property.Value{
			"accelerator": property.New("h100"),
			"service":     property.New("eks"),
			"intent":      property.New("training"),
			"platform":    property.New("kubeflow"),
			"os":          property.New("ubuntu"),
			"nodes":       property.New(1.0),
		})),
		// Test binaries resolve the embedded recipe-data version as
		// "embedded"; the value still round-trips the same wire path as the
		// live "v0.18.0".
		"recipeDataVersion": property.New("embedded"),
		"kubeconfig": property.New("apiVersion: v1\nkind: Config\n").WithSecret(true),
		"requireGpu": property.New(true),
		"triggers": property.New([]property.Value{
			property.New([]property.Value{
				property.New("gpu-operator"),
				property.New("cert-manager"),
			}),
		}),
	})
}

// TestWireDiffCleanAfterCreate is the repro for the spurious-replace bug:
// Check -> Create -> Check -> Diff with byte-identical program inputs must
// produce an empty diff.
func TestWireDiffCleanAfterCreate(t *testing.T) {
	withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return readinessFailedReport(), nil
	})
	s := newWireServer(t)

	checked, err := s.Check(p.CheckRequest{Urn: wireURN, Inputs: rigLikeInputs()})
	require.NoError(t, err)
	require.Empty(t, checked.Failures)

	created, err := s.Create(p.CreateRequest{Urn: wireURN, Properties: checked.Inputs})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	rechecked, err := s.Check(p.CheckRequest{
		Urn: wireURN, Inputs: rigLikeInputs(), State: created.Properties,
	})
	require.NoError(t, err)
	t.Logf("checked #1 rdv: %v", checked.Inputs.Get("recipeDataVersion"))
	t.Logf("rechecked #2 rdv: %v", rechecked.Inputs.Get("recipeDataVersion"))
	t.Logf("state rdv: %v", created.Properties.Get("recipeDataVersion"))

	diff, err := s.Diff(p.DiffRequest{
		ID:        created.ID,
		Urn:       wireURN,
		State:     created.Properties,
		Inputs:    rechecked.Inputs,
		OldInputs: checked.Inputs,
	})
	require.NoError(t, err)
	assert.False(t, diff.HasChanges, "identical inputs must not diff; got: %+v", diff.DetailedDiff)
	assert.Empty(t, diff.DetailedDiff)
}

// TestWireDiffFlagsRealChange proves the wire path still replaces on a real
// input change (the guard must not be fixed by simply never diffing).
func TestWireDiffFlagsRealChange(t *testing.T) {
	withValidateFake(t, func(aicr.Criteria, aicr.ValidateOptions) (*aicr.ValidationReport, error) {
		return readinessFailedReport(), nil
	})
	s := newWireServer(t)

	checked, err := s.Check(p.CheckRequest{Urn: wireURN, Inputs: rigLikeInputs()})
	require.NoError(t, err)
	created, err := s.Create(p.CreateRequest{Urn: wireURN, Properties: checked.Inputs})
	require.NoError(t, err)

	changed := rigLikeInputs()
	changed = changed.Set("namespace", property.New("custom-validation"))
	recheck, err := s.Check(p.CheckRequest{Urn: wireURN, Inputs: changed, State: created.Properties})
	require.NoError(t, err)

	diff, err := s.Diff(p.DiffRequest{
		ID: created.ID, Urn: wireURN,
		State: created.Properties, Inputs: recheck.Inputs, OldInputs: checked.Inputs,
	})
	require.NoError(t, err)
	assert.True(t, diff.HasChanges)
	pd, ok := diff.DetailedDiff["namespace"]
	require.True(t, ok, "namespace change must be flagged; got %+v", diff.DetailedDiff)
	assert.Equal(t, p.UpdateReplace, pd.Kind)
}
