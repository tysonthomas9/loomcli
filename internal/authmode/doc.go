// Package authmode defines the API server's authentication modes and the
// validation applied when a mode arrives from configuration.
//
// ModeOpen serves the API unauthenticated; ModeOIDC requires a verified OIDC
// identity. The constants live in their own leaf package so the CLI's HTTP
// client and the server's config endpoint report the mode identically rather
// than comparing against scattered string literals.
//
// Note that the running server does not select its mode from this package: it
// infers the mode from whether an auth URL was supplied. These constants name
// the result for clients, not the switch that produces it.
package authmode
