package recipe

import "fmt"

// TopologicalSort sorts components by their dependency order.
// Components with no dependencies come first. If a cycle is detected,
// an error is returned.
func TopologicalSort(components []ResolvedComponent) ([]ResolvedComponent, error) {
	if len(components) == 0 {
		return nil, nil
	}

	// Build adjacency map and index
	byName := make(map[string]int, len(components))
	for i, c := range components {
		byName[c.Name] = i
	}

	// Kahn's algorithm
	inDegree := make(map[string]int, len(components))
	dependents := make(map[string][]string, len(components))

	for _, c := range components {
		if _, ok := inDegree[c.Name]; !ok {
			inDegree[c.Name] = 0
		}
		for _, dep := range c.DependsOn {
			if _, ok := byName[dep]; !ok {
				// Dependency not in component set — skip (it's external or already deployed)
				continue
			}
			inDegree[c.Name]++
			dependents[dep] = append(dependents[dep], c.Name)
		}
	}

	// Seed queue with zero-indegree nodes
	var queue []string
	for _, c := range components {
		if inDegree[c.Name] == 0 {
			queue = append(queue, c.Name)
		}
	}

	var sorted []ResolvedComponent
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		sorted = append(sorted, components[byName[name]])

		for _, dep := range dependents[name] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(components) {
		return nil, fmt.Errorf("dependency cycle detected among components")
	}

	return sorted, nil
}
