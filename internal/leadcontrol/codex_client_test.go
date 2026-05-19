package leadcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"nhooyr.io/websocket" //nolint:staticcheck // package under test uses nhooyr/websocket
)

func TestCodexClientWebsocketRPC(t *testing.T) {
	var methods []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var req rpcRequest
			if err := json.Unmarshal(data, &req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			methods = append(methods, req.Method)
			if req.ID == 0 {
				continue
			}
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"id":999,"result":{}}`)); err != nil {
				return
			}
			idJSON := strconv.FormatInt(req.ID, 10)
			if req.Method == "thread/list" {
				idJSON = strconv.Quote(idJSON)
			}
			result := `{}`
			switch req.Method {
			case "thread/list":
				result = `{"data":[{"id":"thread-1","cwd":"/work","status":{"type":"idle"}}]}`
			case "thread/read":
				result = `{"thread":{"id":"thread-1","preview":"hello","cwd":"/work","status":{"type":"active"}}}`
			case "turn/start":
				result = `{}`
			case "initialize":
				result = `{}`
			default:
				t.Errorf("unexpected method %q", req.Method)
				return
			}
			msg := fmt.Sprintf(`{"id":%s,"result":%s}`, idJSON, result)
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(msg)); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	endpoint := "ws" + strings.TrimPrefix(ts.URL, "http")
	client, err := DialCodexAppServer(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("DialCodexAppServer: %v", err)
	}
	defer client.Close("test complete")

	threads, err := client.ListThreads(context.Background(), " /work ", 5)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "thread-1" {
		t.Fatalf("threads = %+v", threads)
	}

	thread, err := client.ReadThread(context.Background(), " thread-1 ")
	if err != nil {
		t.Fatalf("ReadThread: %v", err)
	}
	if thread.Status.RuntimeStatus() != RuntimeStatusActive {
		t.Fatalf("thread = %+v", thread)
	}

	if err := client.StartTurn(context.Background(), " thread-1 ", " continue "); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	for _, want := range []string{"initialize", "initialized", "thread/list", "thread/read", "turn/start"} {
		found := false
		for _, got := range methods {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("method %q not observed in %v", want, methods)
		}
	}
}

func TestCodexClientValidationAndRPCErrorHelpers(t *testing.T) {
	if _, err := DialCodexAppServer(context.Background(), " "); err == nil {
		t.Fatal("empty endpoint should fail")
	}
	if err := (*CodexClient)(nil).Close(""); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
	var closed CodexClient
	if err := closed.Notify(context.Background(), "event", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed Notify err = %v", err)
	}
	if err := closed.Call(context.Background(), "method", nil, nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed Call err = %v", err)
	}
	if _, err := closed.ReadThread(context.Background(), " "); err == nil {
		t.Fatal("empty ReadThread id should fail")
	}
	if err := closed.StartTurn(context.Background(), "", "text"); err == nil {
		t.Fatal("empty StartTurn id should fail")
	}
	if err := closed.StartTurn(context.Background(), "thread", " "); err == nil {
		t.Fatal("empty StartTurn text should fail")
	}
	if !rpcIDMatches(json.RawMessage(`"7"`), 7) || !rpcIDMatches(json.RawMessage(`7`), 7) {
		t.Fatal("rpcIDMatches should accept numeric and string IDs")
	}
	if rpcIDMatches(json.RawMessage(`"8"`), 7) || rpcIDMatches(json.RawMessage(`{}`), 7) || rpcIDMatches(nil, 7) {
		t.Fatal("rpcIDMatches accepted non-matching IDs")
	}
	if got := (*rpcError)(nil).Error(); got != "" {
		t.Fatalf("nil rpcError string = %q", got)
	}
	errWithData := (&rpcError{Code: -1, Message: "failed", Data: json.RawMessage(`{"x":1}`)}).Error()
	if !strings.Contains(errWithData, `{"x":1}`) {
		t.Fatalf("rpcError with data = %q", errWithData)
	}
}
