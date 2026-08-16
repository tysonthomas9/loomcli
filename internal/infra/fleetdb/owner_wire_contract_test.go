package fleetdb

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

func TestOwnerRecordsDecodedDirectlyFromFleetDBDeclareWireNames(t *testing.T) {
	// These records are decoded directly by the shared FleetDB transport. A
	// missing tag silently leaves snake_case response fields at their zero
	// values; a fixture that marshals the same Go type on the server side masks
	// that failure by producing matching CamelCase JSON.
	records := []any{
		agents.AgentServiceRecord{}, agents.OwnershipRecord{},
		artifacts.Artifact{},
		automation.Binding{}, automation.Delivery{},
		execution.AuditPage{}, execution.DriverRunRecord{}, execution.DriverStepRecord{},
		execution.StaleDriverRunRecoveryResult{}, execution.StaleTaskRunRecoveryResult{},
		execution.TaskRunLogEntry{}, execution.TaskRunRecord{}, execution.WorkerNode{}, execution.WorkerProfile{},
		interaction.InboxRecord{}, interaction.LeaseRecord{}, interaction.SessionRecord{}, interaction.TerminalRecord{},
		workflowcatalog.Driver{}, workflowcatalog.DriverVersion{},
		workspaceowner.Workspace{}, workspaceowner.Repository{},
	}
	for _, record := range records {
		recordType := reflect.TypeOf(record)
		t.Run(recordType.PkgPath()+"."+recordType.Name(), func(t *testing.T) {
			for index := 0; index < recordType.NumField(); index++ {
				field := recordType.Field(index)
				if !field.IsExported() {
					continue
				}
				wireName := strings.Split(field.Tag.Get("json"), ",")[0]
				if wireName == "" {
					t.Errorf("%s.%s has no explicit JSON wire name", recordType.Name(), field.Name)
				}
			}
		})
	}
}
