// Package dto defines Data Transfer Objects for the loomcli HTTP API.
// These types represent the JSON wire format for API requests and responses.
//
// DTOs are a transport leaf. Work Items owns the canonical aggregate
// projections and validation policy; this package owns only HTTP wire shapes
// and presentation-specific validation errors.
//
// Request DTOs (CreateIssueRequest, PatchIssueRequest) represent incoming
// JSON payloads. Response DTOs (IssueResponse) represent outgoing JSON
// payloads.
package dto
