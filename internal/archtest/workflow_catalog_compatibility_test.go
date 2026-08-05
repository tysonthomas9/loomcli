package archtest

import (
	"strings"
	"testing"
)

const workflowCatalogLifecyclePatchFixture = `
components:
  schemas:
    UpdateDriverRequest:
      type: object
      properties:
        active_version_id:
          type: string
          deprecated: true
          description: Deprecated legacy activation field.
        metadata:
          type: object
          description: Deprecated approved_version compatibility operation.
`

func TestWorkflowCatalogLifecyclePatchExtensionAcceptsFrozenPhase4Lane(t *testing.T) {
	inventory := directWriteInventoryWithWorkflowCatalogLegacyDriver(4, 4)
	if err := validateWorkflowCatalogLifecyclePatchExtension(4, inventory, []byte(workflowCatalogLifecyclePatchFixture)); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowCatalogLifecyclePatchExtensionRejectsPhase4Drift(t *testing.T) {
	inventory := directWriteInventoryWithWorkflowCatalogLegacyDriver(3, 4)
	err := validateWorkflowCatalogLifecyclePatchExtension(4, inventory, []byte(workflowCatalogLifecyclePatchFixture))
	if err == nil || !strings.Contains(err.Error(), "must freeze workflowcatalog legacy-driver writes at 4 rows/4 sites") {
		t.Fatalf("validation error = %v, want Phase 4 write-set drift rejection", err)
	}

	inventory = directWriteInventoryWithWorkflowCatalogLegacyDriver(4, 4)
	err = validateWorkflowCatalogLifecyclePatchExtension(4, inventory, []byte("components: {schemas: {UpdateDriverRequest: {properties: {}}}}"))
	if err == nil || !strings.Contains(err.Error(), "requires deprecated active_version_id and approved_version metadata markers") {
		t.Fatalf("validation error = %v, want Phase 4 contract-marker rejection", err)
	}
}

func TestWorkflowCatalogLegacyDriverWritesUsesPrimaryInventory(t *testing.T) {
	inventory := DirectWriteInventory{
		Writes: []DirectWriteUse{
			{
				File: "internal/driver/approval.go", AggregateOwner: "workflowcatalog", Count: 1,
			},
			{
				File: "internal/driver/register.go", AggregateOwner: "workflowcatalog", Count: 2,
			},
			{
				File: "internal/driver/unrelated.go", AggregateOwner: "execution", Count: 8,
			},
			{
				File: "internal/modules/workflowcatalog/service.go", AggregateOwner: "workflowcatalog", Count: 8,
			},
		},
		LegacyDriver: &LegacyDirectWriteBaseline{
			Owners: []LegacyDirectWriteOwnerBaseline{{
				CapabilityOwner: "workflowcatalog", Rows: 9, Sites: 9,
			}},
		},
	}
	rows, sites := workflowCatalogLegacyDriverWrites(inventory)
	if rows != 2 || sites != 3 {
		t.Fatalf("workflowcatalog internal/driver writes = %d/%d, want 2/3", rows, sites)
	}
}

func TestWorkflowCatalogLifecyclePatchExtensionExpiresAtPhase5Completion(t *testing.T) {
	inventory := directWriteInventoryWithWorkflowCatalogLegacyDriver(4, 4)
	err := validateWorkflowCatalogLifecyclePatchExtension(5, inventory, []byte(workflowCatalogLifecyclePatchFixture))
	if err == nil || !strings.Contains(err.Error(), "expired after Phase 5") {
		t.Fatalf("validation error = %v, want Phase 5 expiry rejection", err)
	}

	inventory.LegacyDriver.Owners = nil
	if err := validateWorkflowCatalogLifecyclePatchExtension(5, inventory, []byte("components: {schemas: {UpdateDriverRequest: {properties: {}}}}")); err != nil {
		t.Fatalf("retired Phase 5 lane must pass: %v", err)
	}

	retiredMetadataContract := `
components:
  schemas:
    UpdateDriverRequest:
      type: object
      properties:
        metadata:
          type: object
          description: Ordinary replacement preserves approved_version lifecycle markers; adding or changing them is rejected.
`
	if err := validateWorkflowCatalogLifecyclePatchExtension(5, inventory, []byte(retiredMetadataContract)); err != nil {
		t.Fatalf("ordinary metadata with retired approval semantics must pass: %v", err)
	}

	contract := strings.Replace(workflowCatalogLifecyclePatchFixture, "deprecated: true", "deprecated: false", 1)
	err = validateWorkflowCatalogLifecyclePatchExtension(5, inventory, []byte(contract))
	if err == nil || !strings.Contains(err.Error(), "active_version_id=true") {
		t.Fatalf("validation error = %v, want Phase 5 active_version_id presence rejection", err)
	}
}

func directWriteInventoryWithWorkflowCatalogLegacyDriver(rows, sites int) DirectWriteInventory {
	return DirectWriteInventory{LegacyDriver: &LegacyDirectWriteBaseline{
		Owners: []LegacyDirectWriteOwnerBaseline{{
			CapabilityOwner: "workflowcatalog",
			Rows:            rows,
			Sites:           sites,
		}},
	}}
}
