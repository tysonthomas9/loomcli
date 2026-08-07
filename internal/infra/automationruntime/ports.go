package trigger

import "github.com/tysonthomas9/loomcli/internal/store"

// workspaceLister is the smallest legacy persistence seam shared by trigger
// sweepers while their orchestration moves behind Automation.
type workspaceLister interface {
	Workspaces() store.WorkspaceStore
}

type cronStore interface {
	workspaceLister
	Awaits() store.AwaitStore
	DriverRuns() store.DriverRunStore
	TriggerBindings() store.TriggerBindingStore
	TriggerRoutes() store.TriggerRouteDispatcher
}

type internalSourceStore interface {
	Awaits() store.AwaitStore
	DriverRuns() store.DriverRunStore
	TriggerEvents() store.TriggerEventStore
	TriggerRoutes() store.TriggerRouteDispatcher
}

type deliverySweepStore interface {
	workspaceLister
	TriggerBindings() store.TriggerBindingStore
	TriggerEvents() store.TriggerEventStore
	TriggerDeliveries() store.TriggerDeliveryStore
	TriggerRoutes() store.TriggerRouteDispatcher
}
