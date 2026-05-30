package backends

import (
	"encoding/json"
	"strings"
	"sync"
)

type backendProviderMetadataCapture struct {
	mu             sync.RWMutex
	provider       string
	jsonEventCount int
	eventTypes     []string
	eventTypeSeen  map[string]bool
	ids            map[string]string
	providerModel  string
	sessionID      string
	lastEventType  string
}

func (c *backendProviderMetadataCapture) Clear(provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = strings.TrimSpace(provider)
	c.jsonEventCount = 0
	c.eventTypes = nil
	c.eventTypeSeen = nil
	c.ids = nil
	c.providerModel = ""
	c.sessionID = ""
	c.lastEventType = ""
}

func (c *backendProviderMetadataCapture) IngestLine(line string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil || len(event) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jsonEventCount++
	if c.eventTypeSeen == nil {
		c.eventTypeSeen = make(map[string]bool)
	}
	if c.ids == nil {
		c.ids = make(map[string]string)
	}
	if typ := firstProviderStringField(event, "type"); typ != "" {
		c.lastEventType = typ
		if !c.eventTypeSeen[typ] {
			c.eventTypeSeen[typ] = true
			c.eventTypes = append(c.eventTypes, typ)
		}
	}
	for _, key := range []string{
		"id",
		"session_id", "sessionID",
		"conversation_id", "conversationID",
		"thread_id", "threadID",
		"response_id", "responseID",
		"run_id", "runID",
	} {
		if value := firstProviderStringField(event, key); value != "" {
			c.ids[key] = value
		}
	}
	if c.sessionID == "" {
		c.sessionID = firstProviderStringField(event,
			"session_id", "sessionID",
			"conversation_id", "conversationID",
			"thread_id", "threadID",
		)
	}
	if c.sessionID == "" {
		c.sessionID = nestedProviderStringField(event, "session", "id")
	}
	if c.sessionID == "" {
		c.sessionID = nestedProviderStringField(event, "conversation", "id")
	}
	if c.sessionID == "" {
		c.sessionID = nestedProviderStringField(event, "thread", "id")
	}
	if c.providerModel == "" {
		c.providerModel = firstProviderStringField(event, "provider_model", "model", "model_id", "modelID")
	}
	if c.providerModel == "" {
		c.providerModel = nestedProviderStringField(event, "message", "model")
	}
	if c.providerModel == "" {
		c.providerModel = nestedProviderStringField(event, "response", "model")
	}
}

func (c *backendProviderMetadataCapture) LastSessionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

func (c *backendProviderMetadataCapture) Metadata() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.jsonEventCount == 0 {
		return nil
	}
	out := map[string]any{
		"json_event_count": c.jsonEventCount,
	}
	if c.provider != "" {
		out["provider"] = c.provider
	}
	if len(c.eventTypes) > 0 {
		out["event_types"] = append([]string(nil), c.eventTypes...)
		out["last_event_type"] = c.lastEventType
	}
	if len(c.ids) > 0 {
		ids := make(map[string]string, len(c.ids))
		for key, value := range c.ids {
			ids[key] = value
		}
		out["ids"] = ids
	}
	if c.providerModel != "" {
		out["provider_model"] = c.providerModel
	}
	if c.sessionID != "" {
		out["provider_session_id"] = c.sessionID
	}
	return out
}

func firstProviderStringField(event map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := event[key]
		if !ok {
			continue
		}
		if typed, ok := value.(string); ok {
			if out := strings.TrimSpace(typed); out != "" {
				return out
			}
		}
	}
	return ""
}

func nestedProviderStringField(event map[string]any, objectKey, valueKey string) string {
	value, ok := event[objectKey]
	if !ok {
		return ""
	}
	nested, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return firstProviderStringField(nested, valueKey)
}
