package app

import "net/http"

// wsModule is the interface for a group of related HTTP routes that can register
// themselves on a [*http.ServeMux].
// Defined as a named type so all facade packages' return types are compatible.
type wsModule = interface{ Register(mux *http.ServeMux) }
