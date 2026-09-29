// Package misc holds the webui endpoints that do not belong to a resource of
// their own: session reads, agent logs, backend health, file checkouts, client
// error reporting, and auth configuration.
//
// It is a deliberate catch-all rather than an accident of naming. A route
// earns its own package when it has a coherent resource behind it; these do
// not, and inventing a package per endpoint would obscure that.
//
// Two of them are rate limited by construction: HandleAuthConfig and
// HandleClientErrors take a limiter, because both are reachable before a
// client is authenticated and would otherwise be free to call in a loop.
package misc
