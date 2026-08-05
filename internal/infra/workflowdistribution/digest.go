package workflowdistribution

import "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

// SourceDigest delegates the canonical identity recipe to Workflow Catalog.
// Distribution may compute it for local source trees but does not own it.
func SourceDigest(files map[string]string) (string, error) {
	return workflowcatalog.SourceDigest(files)
}
