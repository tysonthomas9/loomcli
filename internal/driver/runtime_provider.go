package driver

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

func runtimeProvider(workspace string) string {
	provider, _ := bootstrap.RuntimeProvider(workspace)
	return strings.TrimSpace(provider)
}
