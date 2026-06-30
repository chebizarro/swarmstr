package sandbox

import "time"

// PruneRuntimes marks stale/expired runtime records pruned and removes them from active tracking.
func (r *RuntimeRegistry) Prune(scope RuntimeScope, olderThan time.Duration, now time.Time) []RuntimeInfo {
	if now.IsZero() {
		now = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	wanted := normalizeRuntimeScope(string(scope))
	pruned := []RuntimeInfo{}
	for key, rt := range r.runtimes {
		if scope != "" && rt.Scope != wanted {
			continue
		}
		if rt.Status == RuntimeStatusStale || olderThan <= 0 || now.Sub(rt.LastUsedAt) >= olderThan {
			rt.Status = RuntimeStatusPruned
			pruned = append(pruned, rt.RuntimeInfo)
			delete(r.runtimes, key)
		}
	}
	return pruned
}

// PruneRuntimes prunes runtimes from registry, defaulting to the process registry when nil.
func PruneRuntimes(registry *RuntimeRegistry, scope RuntimeScope, olderThan time.Duration) []RuntimeInfo {
	if registry == nil {
		registry = DefaultRuntimeRegistry()
	}
	return registry.Prune(scope, olderThan, time.Now())
}
