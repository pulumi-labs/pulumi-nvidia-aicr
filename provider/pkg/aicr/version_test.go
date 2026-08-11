package aicr

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// withBuildInfo swaps the build-info source for the duration of a test.
// The seam exists because Go test binaries carry incomplete dependency
// build info: the real debug.ReadBuildInfo can never return the SDK dep
// here, so each behavior must be exercised with fabricated info.
func withBuildInfo(t *testing.T, info *debug.BuildInfo, ok bool) {
	t.Helper()
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
	t.Cleanup(func() { readBuildInfo = prev })
}

func depInfo(dep debug.Module) *debug.BuildInfo {
	return &debug.BuildInfo{Deps: []*debug.Module{&dep}}
}

func TestSDKModuleVersion(t *testing.T) {
	cases := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{
			name: "normal pinned dependency",
			info: depInfo(debug.Module{Path: aicrModulePath, Version: "v0.18.0"}),
			ok:   true,
			want: "v0.18.0",
		},
		{
			name: "version replace directive wins",
			info: depInfo(debug.Module{
				Path: aicrModulePath, Version: "v0.18.0",
				Replace: &debug.Module{Path: "example.com/fork/aicr", Version: "v0.18.1"},
			}),
			ok:   true,
			want: "v0.18.1",
		},
		{
			name: "filesystem replace with empty version falls back to required version",
			info: depInfo(debug.Module{
				Path: aicrModulePath, Version: "v0.18.0",
				Replace: &debug.Module{Path: "../aicr", Version: ""},
			}),
			ok:   true,
			want: "v0.18.0",
		},
		{
			name: "filesystem replace with (devel) falls back to required version",
			info: depInfo(debug.Module{
				Path: aicrModulePath, Version: "v0.18.0",
				Replace: &debug.Module{Path: "../aicr", Version: "(devel)"},
			}),
			ok:   true,
			want: "v0.18.0",
		},
		{
			name: "dependency absent",
			info: depInfo(debug.Module{Path: "github.com/other/dep", Version: "v1.0.0"}),
			ok:   true,
			want: "embedded",
		},
		{
			name: "no build info at all",
			info: nil,
			ok:   false,
			want: "embedded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withBuildInfo(t, tc.info, tc.ok)
			assert.Equal(t, tc.want, sdkModuleVersion())
		})
	}
}
