package recipe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopologicalSortSimple(t *testing.T) {
	components := []ResolvedComponent{
		{Name: "b", DependsOn: []string{"a"}},
		{Name: "a"},
		{Name: "c", DependsOn: []string{"b"}},
	}

	sorted, err := TopologicalSort(components)
	require.NoError(t, err)
	require.Len(t, sorted, 3)

	// a must come before b, b before c
	assert.Equal(t, "a", sorted[0].Name)
	assert.Equal(t, "b", sorted[1].Name)
	assert.Equal(t, "c", sorted[2].Name)
}

func TestTopologicalSortMultipleDeps(t *testing.T) {
	components := []ResolvedComponent{
		{Name: "gpu-operator", DependsOn: []string{"cert-manager", "kube-prometheus-stack"}},
		{Name: "cert-manager"},
		{Name: "kube-prometheus-stack"},
		{Name: "kai-scheduler", DependsOn: []string{"gpu-operator"}},
	}

	sorted, err := TopologicalSort(components)
	require.NoError(t, err)
	require.Len(t, sorted, 4)

	// cert-manager and kube-prometheus-stack must come before gpu-operator
	gpuIdx := indexOf(sorted, "gpu-operator")
	certIdx := indexOf(sorted, "cert-manager")
	promIdx := indexOf(sorted, "kube-prometheus-stack")
	kaiIdx := indexOf(sorted, "kai-scheduler")

	assert.Less(t, certIdx, gpuIdx)
	assert.Less(t, promIdx, gpuIdx)
	assert.Less(t, gpuIdx, kaiIdx)
}

func TestTopologicalSortNoDeps(t *testing.T) {
	components := []ResolvedComponent{
		{Name: "a"},
		{Name: "b"},
		{Name: "c"},
	}

	sorted, err := TopologicalSort(components)
	require.NoError(t, err)
	assert.Len(t, sorted, 3)
}

func TestTopologicalSortCycleDetection(t *testing.T) {
	components := []ResolvedComponent{
		{Name: "a", DependsOn: []string{"b"}},
		{Name: "b", DependsOn: []string{"a"}},
	}

	_, err := TopologicalSort(components)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestTopologicalSortEmpty(t *testing.T) {
	sorted, err := TopologicalSort(nil)
	require.NoError(t, err)
	assert.Nil(t, sorted)
}

func TestTopologicalSortExternalDeps(t *testing.T) {
	// Dependencies on components not in the set should be ignored
	components := []ResolvedComponent{
		{Name: "a", DependsOn: []string{"external-dep"}},
		{Name: "b", DependsOn: []string{"a"}},
	}

	sorted, err := TopologicalSort(components)
	require.NoError(t, err)
	assert.Len(t, sorted, 2)
	assert.Equal(t, "a", sorted[0].Name)
	assert.Equal(t, "b", sorted[1].Name)
}

func indexOf(components []ResolvedComponent, name string) int {
	for i, c := range components {
		if c.Name == name {
			return i
		}
	}
	return -1
}
