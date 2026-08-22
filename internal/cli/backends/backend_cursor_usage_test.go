package backends

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// cursor-agent 2026.08 emits camelCase usage keys on the result event; the
// collector used to look only for snake_case and recorded zero for every
// cursor session.
func TestCollectCursorStreamUsageAcceptsCamelCaseResult(t *testing.T) {
	c := usage.NewCollector("cursor", "nova")
	collectCursorStreamUsage(`{"type":"system","subtype":"init","model":"Composer 2.5","session_id":"s"}`, c)
	collectCursorStreamUsage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`, c)
	collectCursorStreamUsage(`{"type":"result","subtype":"success","is_error":false,"result":"hi","usage":{"inputTokens":134,"outputTokens":20,"cacheReadTokens":19328,"cacheWriteTokens":7}}`, c)
	in, out, cr, cw := c.Totals()
	if in != 134 || out != 20 || cr != 19328 || cw != 7 {
		t.Fatalf("totals = %d/%d/%d/%d, want 134/20/19328/7", in, out, cr, cw)
	}
}

func TestCollectCursorStreamUsageStillAcceptsSnakeCase(t *testing.T) {
	c := usage.NewCollector("cursor", "nova")
	collectCursorStreamUsage(`{"type":"result","usage":{"input_tokens":10,"output_tokens":3}}`, c)
	collectCursorStreamUsage(`not json`, c)
	collectCursorStreamUsage(`{"type":"result"}`, c)
	in, out, cr, cw := c.Totals()
	if in != 10 || out != 3 || cr != 0 || cw != 0 {
		t.Fatalf("totals = %d/%d/%d/%d, want 10/3/0/0", in, out, cr, cw)
	}
}
