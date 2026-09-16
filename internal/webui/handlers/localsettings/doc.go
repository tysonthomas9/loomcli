// Package localsettings serves the operator preferences stored on the server's
// own machine.
//
// HandleGet returns the settings for a data directory and HandlePatch applies
// a partial update, so a client can change one preference without resending
// the whole document and racing another writer.
//
// These are machine-local preferences, deliberately not workspace state: they
// belong to the install, and they do not travel with a workspace to another
// host.
package localsettings
