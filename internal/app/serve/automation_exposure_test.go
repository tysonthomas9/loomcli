package serve

import (
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
)

func TestAutomationCapabilityDoesNotExposeRawSystemEventWorkflow(t *testing.T) {
	capabilityType := reflect.TypeOf((*AutomationCapability)(nil))
	rawWorkflowType := reflect.TypeOf((*systemeventing.Workflow)(nil))
	for index := 0; index < capabilityType.NumMethod(); index++ {
		method := capabilityType.Method(index)
		if method.Name == "SystemEventing" {
			t.Fatalf("AutomationCapability exposes raw system workflow through %s", method.Name)
		}
		for output := 0; output < method.Type.NumOut(); output++ {
			if method.Type.Out(output) == rawWorkflowType {
				t.Fatalf("AutomationCapability method %s returns raw system workflow", method.Name)
			}
		}
	}
}
