package app

import "github.com/tysonthomas9/loomcli/internal/webui/route"

// wsModule is the interface for a group of related HTTP routes that can register
// themselves on a [route.Router].
// Defined as a named type so all facade packages' return types are compatible.
type wsModule = interface{ Register(mux route.Router) }
