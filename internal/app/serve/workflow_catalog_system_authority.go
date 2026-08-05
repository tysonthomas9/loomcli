package serve

import (
	"errors"
	"fmt"
)

type workflowCatalogRuntimeComponent string

const (
	// Keep this ID synchronized with the canonical component registration in
	// internal/archtest/testdata/runtime-components.yaml.
	workflowCatalogCronSchedulerComponent       workflowCatalogRuntimeComponent = "serve-trigger-cron-scheduler"
	workflowCatalogBuiltinDistributionComponent workflowCatalogRuntimeComponent = "workflow-catalog-builtin-distribution"
)

var (
	errUnregisteredWorkflowCatalogRuntimeComponent = errors.New("workflow catalog: unregistered runtime component")

	workflowCatalogEffectiveVersionRuntimeComponents = map[workflowCatalogRuntimeComponent]struct{}{
		workflowCatalogCronSchedulerComponent: {},
	}
	workflowCatalogManagedAuthoringComponents = map[workflowCatalogRuntimeComponent]struct{}{
		workflowCatalogBuiltinDistributionComponent: {},
	}
)

func validateWorkflowCatalogRuntimeComponent(component workflowCatalogRuntimeComponent) error {
	if _, ok := workflowCatalogEffectiveVersionRuntimeComponents[component]; !ok {
		return fmt.Errorf("%w: %q", errUnregisteredWorkflowCatalogRuntimeComponent, component)
	}
	return nil
}

func validateWorkflowCatalogManagedAuthoringComponent(component workflowCatalogRuntimeComponent) error {
	if _, ok := workflowCatalogManagedAuthoringComponents[component]; !ok {
		return fmt.Errorf("%w: %q", errUnregisteredWorkflowCatalogRuntimeComponent, component)
	}
	return nil
}
