package archtest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const workflowCatalogLifecyclePatchExpiresAfterPhase = 5

type workflowCatalogPatchField struct {
	Deprecated  bool   `yaml:"deprecated"`
	Description string `yaml:"description"`
}

type workflowCatalogPatchContract struct {
	Components struct {
		Schemas map[string]struct {
			Properties map[string]workflowCatalogPatchField `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// validateWorkflowCatalogLifecyclePatchExtension binds the reviewed Phase 5
// extension to both sides of the compatibility lane. The legacy-driver digest
// freezes Loom's exact current write set; the vendored OpenAPI proves FleetDB
// still accepts the deprecated generic activation and approval fields. Neither
// side may survive once Phase 5 is marked complete.
func validateWorkflowCatalogLifecyclePatchExtension(completedPhase int, inventory DirectWriteInventory, contract []byte) error {
	rows, sites := workflowCatalogLegacyDriverWrites(inventory.LegacyDriver)
	activation, activationDeprecated, approvalMetadata, err := workflowCatalogPatchCompatibilityFields(contract)
	if err != nil {
		return err
	}

	if completedPhase >= workflowCatalogLifecyclePatchExpiresAfterPhase {
		if rows != 0 || sites != 0 || activation || approvalMetadata {
			return fmt.Errorf(
				"MM-2-LEGACY-DRIVER-LIFECYCLE-PATCH expired after Phase %d but completed_phase is %d: workflowcatalog legacy-driver writes=%d/%d active_version_id=%t active_version_id_deprecated=%t approval_metadata=%t",
				workflowCatalogLifecyclePatchExpiresAfterPhase, completedPhase, rows, sites, activation, activationDeprecated, approvalMetadata,
			)
		}
		return nil
	}

	if completedPhase == workflowCatalogLifecyclePatchExpiresAfterPhase-1 {
		if rows != 4 || sites != 4 {
			return fmt.Errorf("MM-2-LEGACY-DRIVER-LIFECYCLE-PATCH Phase 4 extension must freeze workflowcatalog legacy-driver writes at 4 rows/4 sites, got %d/%d", rows, sites)
		}
		if !activation || !activationDeprecated || !approvalMetadata {
			return fmt.Errorf("MM-2-LEGACY-DRIVER-LIFECYCLE-PATCH Phase 4 extension requires deprecated active_version_id and approved_version metadata markers in the vendored FleetDB OpenAPI")
		}
	}
	return nil
}

func workflowCatalogLegacyDriverWrites(baseline *LegacyDirectWriteBaseline) (int, int) {
	if baseline == nil {
		return 0, 0
	}
	for _, owner := range baseline.Owners {
		if owner.CapabilityOwner == "workflowcatalog" {
			return owner.Rows, owner.Sites
		}
	}
	return 0, 0
}

func workflowCatalogPatchCompatibilityFields(contract []byte) (bool, bool, bool, error) {
	var document workflowCatalogPatchContract
	if err := yaml.Unmarshal(contract, &document); err != nil {
		return false, false, false, fmt.Errorf("decode vendored FleetDB OpenAPI for MM-2 compatibility: %w", err)
	}
	update, ok := document.Components.Schemas["UpdateDriverRequest"]
	if !ok {
		return false, false, false, nil
	}
	activeVersion, activeVersionPresent := update.Properties["active_version_id"]
	metadata, metadataPresent := update.Properties["metadata"]
	approvalMetadata := metadataPresent && strings.Contains(metadata.Description, "approved_version")
	return activeVersionPresent, activeVersion.Deprecated, approvalMetadata, nil
}
