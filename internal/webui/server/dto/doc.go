// Package dto defines Data Transfer Objects for the loomcli HTTP API.
// These types represent the JSON wire format for API requests and responses.
//
// DTOs are a leaf package: they import only internal/entity (for domain
// enums and value types) and the Go standard library.
// They do NOT import service, handler, or any infrastructure packages.
//
// Request DTOs (CreateIssueRequest, PatchIssueRequest) represent incoming
// JSON payloads. Response DTOs (IssueResponse) represent outgoing JSON
// payloads. Mapping between DTOs and entity/service types is provided by
// separate mapper functions (see IssueFromEntity in a sibling task).
package dto
