package tsfirst

import (
	"bufio"
	"encoding/json"
	"strings"
)

type providerMetadataCollector struct {
	jsonEventCount int
	eventTypes     []string
	eventTypeSeen  map[string]bool
	ids            map[string]string
	providerModel  string
	sessionID      string
	lastEventType  string
}

func (c *providerMetadataCollector) ingestLine(line string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(localJSONLinePayload(line)), &event); err != nil || len(event) == 0 {
		return
	}
	c.jsonEventCount++
	if c.eventTypeSeen == nil {
		c.eventTypeSeen = make(map[string]bool)
	}
	if c.ids == nil {
		c.ids = make(map[string]string)
	}
	if typ := firstStringField(event, "type"); typ != "" {
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
		if value := firstStringField(event, key); value != "" {
			c.ids[key] = value
		}
	}
	if c.sessionID == "" {
		c.sessionID = firstStringField(event,
			"session_id", "sessionID",
			"conversation_id", "conversationID",
			"thread_id", "threadID",
		)
	}
	if c.providerModel == "" {
		c.providerModel = firstStringField(event, "provider_model", "model", "model_id", "modelID")
		if c.providerModel == "" {
			c.providerModel = nestedStringField(event, "message", "model")
		}
		if c.providerModel == "" {
			c.providerModel = nestedStringField(event, "response", "model")
		}
	}
}

func (c *providerMetadataCollector) metadata() map[string]any {
	if c == nil || c.jsonEventCount == 0 {
		return nil
	}
	out := map[string]any{
		"json_event_count": c.jsonEventCount,
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

func localJSONLinePayload(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
		return line
	}
	if start, end := strings.Index(line, "{"), strings.LastIndex(line, "}"); start >= 0 && end > start {
		return line[start : end+1]
	}
	if start, end := strings.Index(line, "["), strings.LastIndex(line, "]"); start >= 0 && end > start {
		return line[start : end+1]
	}
	return line
}

func providerMetadataFromOutput(output string) (metadata map[string]any, sessionID, providerModel string) {
	var collector providerMetadataCollector
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		collector.ingestLine(scanner.Text())
	}
	return collector.metadata(), collector.sessionID, collector.providerModel
}

func firstStringField(event map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := event[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if out := strings.TrimSpace(typed); out != "" {
				return out
			}
		}
	}
	return ""
}

func nestedStringField(event map[string]any, objectKey, valueKey string) string {
	value, ok := event[objectKey]
	if !ok {
		return ""
	}
	nested, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return firstStringField(nested, valueKey)
}
