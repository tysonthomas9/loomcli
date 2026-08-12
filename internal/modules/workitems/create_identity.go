package workitems

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// DurableCreateFields returns the canonical persisted create projection shared
// by the local FleetDB adapter and CLI idempotency-key derivation. Fields not
// supported by the durable contract are deliberately absent rather than sent
// through an alternate compatibility request.
func (command CreateCommand) DurableCreateFields() map[string]interface{} {
	request := make(map[string]interface{})
	setCreateString(request, "title", command.Title)
	setCreateString(request, "description", command.Description)
	setCreateString(request, "status", command.Status)
	if command.Priority != 0 {
		request["priority"] = command.Priority
	}
	setCreateString(request, "type", command.IssueType)
	setCreateString(request, "assignee", command.Assignee)
	setCreateString(request, "owner", command.Owner)
	if len(command.Labels) > 0 {
		request["labels"] = command.Labels
	}
	setCreateString(request, "parent_id", command.Parent)
	setCreateString(request, "repo", command.SourceRepo)
	setCreateString(request, "design", command.Design)
	setCreateString(request, "notes", command.Notes)
	setCreateString(request, "external_ref", command.ExternalRef)
	setCreateString(request, "defer_until", command.DeferUntil)
	setCreateString(request, "due_at", command.DueAt)
	return request
}

func (command CreateCommand) DefaultIdempotencyKey(now time.Time) (string, error) {
	body, err := json.Marshal(command.DurableCreateFields())
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	hash.Write([]byte(now.UTC().Format("20060102")))
	hash.Write([]byte{0})
	hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (command CreateCommand) IdempotencyHeaders() map[string]string {
	headers := map[string]string{}
	if command.IdempotencyKey != "" {
		headers["X-Idempotency-Key"] = command.IdempotencyKey
	}
	if command.Force {
		headers["X-Idempotency-Force"] = "true"
	}
	return headers
}

func setCreateString(request map[string]interface{}, key, value string) {
	if value != "" {
		request[key] = value
	}
}
