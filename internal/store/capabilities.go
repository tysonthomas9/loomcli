package store

// StoreUnwrapper lets decorators expose the store they wrap without claiming
// optional capabilities that the wrapped implementation does not provide.
type StoreUnwrapper interface {
	UnwrapStore() Store
}

// ResolveTaskChangeHandoffStore finds the optional task change handoff
// capability through transparent Store decorators.
func ResolveTaskChangeHandoffStore(value Store) (TaskChangeHandoffStore, bool) {
	for depth := 0; value != nil && depth < 32; depth++ {
		if capability, ok := value.(TaskChangeHandoffStore); ok {
			return capability, true
		}
		unwrapper, ok := value.(StoreUnwrapper)
		if !ok {
			return nil, false
		}
		value = unwrapper.UnwrapStore()
	}
	return nil, false
}

// ResolveTaskRunExecutionContextStore finds the optional TaskRun lifecycle
// capability through transparent Store decorators.
func ResolveTaskRunExecutionContextStore(value Store) (TaskRunExecutionContextStore, bool) {
	for depth := 0; value != nil && depth < 32; depth++ {
		if capability, ok := value.(TaskRunExecutionContextStore); ok {
			return capability, true
		}
		unwrapper, ok := value.(StoreUnwrapper)
		if !ok {
			return nil, false
		}
		value = unwrapper.UnwrapStore()
	}
	return nil, false
}
