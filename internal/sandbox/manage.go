package sandbox

// ManageRuntime registers or reuses an opt-in persistent runtime in registry.
func ManageRuntime(registry *RuntimeRegistry, cfg Config, backend SandboxRunner) (SandboxRunner, error) {
	if registry == nil {
		registry = DefaultRuntimeRegistry()
	}
	return registry.Manage(RuntimeSpec{Config: cfg, Backend: backend})
}

// ListRuntimes lists runtimes from registry, optionally filtered by scope.
func ListRuntimes(registry *RuntimeRegistry, scope RuntimeScope) []RuntimeInfo {
	if registry == nil {
		registry = DefaultRuntimeRegistry()
	}
	return registry.List(scope)
}
