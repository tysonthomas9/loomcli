package authoring

import (
	"io/fs"

	workflowdistribution "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution"
)

// packagedBuiltinFS remains overrideable by legacy facade tests. Production
// source and tagged packaged builds are owned by the distribution adapter.
var packagedBuiltinFS fs.FS = workflowdistribution.PackagedBuiltinFS()
