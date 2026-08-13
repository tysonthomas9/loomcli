package leadoccupant

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFromEnvThreeStates(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		baseURL   string
		workspace string
		want      State
	}{
		{"absent", "", "", "", StateAbsent},
		{"token only", "token", "", "", StatePartial},
		{"token and URL", "token", "https://loom.test", "", StatePartial},
		{"token and workspace", "token", "", "WS", StatePartial},
		{"complete", "token", "https://loom.test/", "WS", StateComplete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvOccupantToken, tt.token)
			t.Setenv(EnvLeadAPIURL, tt.baseURL)
			t.Setenv(EnvWorkspace, tt.workspace)
			t.Setenv(EnvPlacementID, "p1")
			env, state := FromEnv()
			if state != tt.want {
				t.Fatalf("state = %v, want %v", state, tt.want)
			}
			if env.BaseURL != strings.TrimRight(tt.baseURL, "/") || env.Workspace != tt.workspace || env.PlacementID != "p1" || env.EnvToken != tt.token {
				t.Fatalf("env = %+v", env)
			}
		})
	}
}

func TestWriteTokenPermissionsAndRead(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	if err := WriteToken("token-one"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	path, err := TokenPath()
	if err != nil {
		t.Fatalf("TokenPath: %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("token mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat token dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("token dir mode = %o, want 700", got)
	}
	if got := ReadToken(); got != "token-one" {
		t.Fatalf("ReadToken = %q, want token-one", got)
	}
}

func TestWriteTokenIsAtomicForConcurrentReaders(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	tokenA := strings.Repeat("a", 16<<10)
	tokenB := strings.Repeat("b", 16<<10)
	if err := WriteToken(tokenA); err != nil {
		t.Fatalf("initial WriteToken: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan string, 1)
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				got := ReadToken()
				if got != tokenA && got != tokenB {
					select {
					case errCh <- got:
					default:
					}
					return
				}
			}
		}
	}()
	for i := 0; i < 50; i++ {
		token := tokenA
		if i%2 == 1 {
			token = tokenB
		}
		if err := WriteToken(token); err != nil {
			t.Fatalf("WriteToken %d: %v", i, err)
		}
	}
	close(done)
	wg.Wait()
	select {
	case torn := <-errCh:
		t.Fatalf("reader observed torn token of length %d", len(torn))
	default:
	}
}

func TestWriteTokenFailureObservable(t *testing.T) {
	baseFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(baseFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	t.Setenv("LOOM_CONFIG_DIR", baseFile)
	if err := WriteToken("token"); err == nil {
		t.Fatal("WriteToken succeeded with a file as LOOM_CONFIG_DIR")
	}
}

func TestReadTokenMissingAndCorrupt(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if got := ReadToken(); got != "" {
		t.Fatalf("missing ReadToken = %q, want empty", got)
	}
	path, err := TokenPath()
	if err != nil {
		t.Fatalf("TokenPath: %v", err)
	}
	if err := os.WriteFile(path, []byte("not a valid\ntoken"), 0o600); err != nil {
		t.Fatalf("write corrupt token: %v", err)
	}
	if got := ReadToken(); got != "" {
		t.Fatalf("corrupt ReadToken = %q, want empty", got)
	}
}

func TestEnvCurrentTokenFileThenEnv(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	env := Env{EnvToken: "env-token"}
	if got := env.CurrentToken(); got != "env-token" {
		t.Fatalf("CurrentToken without file = %q", got)
	}
	if err := WriteToken("file-token"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	if got := env.CurrentToken(); got != "file-token" {
		t.Fatalf("CurrentToken with file = %q", got)
	}
}

type fakeDoer struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	do       func(int, *http.Request) (*http.Response, error)
}

func (d *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	d.requests = append(d.requests, clone)
	d.bodies = append(d.bodies, body)
	return d.do(len(d.requests), req)
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestOccupantTransportClonesRequestAndSetsBearer(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	doer := &fakeDoer{do: func(_ int, _ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, "ok"), nil
	}}
	transport := Env{EnvToken: "env-token"}.Transport()
	transport.base = doer
	req, err := http.NewRequest(http.MethodGet, "https://loom.test/data", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Test", "original")
	resp, err := transport.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if req.Header.Get("Authorization") != "" {
		t.Fatalf("caller Authorization mutated to %q", req.Header.Get("Authorization"))
	}
	if got := doer.requests[0].Header.Get("Authorization"); got != "Bearer env-token" {
		t.Fatalf("sent Authorization = %q", got)
	}
}

func TestOccupantTransportClosesCallerRequestBody(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	doer := &fakeDoer{do: func(_ int, _ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, "ok"), nil
	}}
	transport := Env{EnvToken: "env-token"}.Transport()
	transport.base = doer
	body := &trackingBody{Reader: strings.NewReader("payload")}
	req, err := http.NewRequest(http.MethodPost, "https://loom.test/data", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := transport.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if !body.closed {
		t.Fatal("caller request body was not closed")
	}
}

func TestOccupantTransportDefaultTimeout(t *testing.T) {
	transport := Env{EnvToken: "token"}.Transport()
	client, ok := transport.base.(*http.Client)
	if !ok {
		t.Fatalf("default doer = %T, want *http.Client", transport.base)
	}
	if client.Timeout != 30*time.Second {
		t.Fatalf("timeout = %s, want 30s", client.Timeout)
	}
}

func TestOccupantTransportRetriesOnceWithFresherFileToken(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	firstBody := &trackingBody{Reader: strings.NewReader("stale")}
	doer := &fakeDoer{do: func(call int, _ *http.Request) (*http.Response, error) {
		if call == 1 {
			if err := WriteToken("new-token"); err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: firstBody}, nil
		}
		return response(http.StatusUnauthorized, "still stale"), nil
	}}
	transport := Env{EnvToken: "old-token"}.Transport()
	transport.base = doer
	req, err := http.NewRequest(http.MethodPost, "https://loom.test/data", bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := transport.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if len(doer.requests) != 2 {
		t.Fatalf("requests = %d, want exactly 2", len(doer.requests))
	}
	if got := doer.requests[0].Header.Get("Authorization"); got != "Bearer old-token" {
		t.Fatalf("first Authorization = %q", got)
	}
	if got := doer.requests[1].Header.Get("Authorization"); got != "Bearer new-token" {
		t.Fatalf("retry Authorization = %q", got)
	}
	if string(doer.bodies[0]) != "payload" || string(doer.bodies[1]) != "payload" {
		t.Fatalf("request bodies = %q / %q, want replayed payload", doer.bodies[0], doer.bodies[1])
	}
	if !firstBody.closed {
		t.Fatal("first 401 response body was not closed before retry")
	}
}

func TestOccupantTransportRestoresOriginal401WithoutFresherToken(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	original := response(http.StatusUnauthorized, "original body")
	original.Header.Set("X-Original", "yes")
	doer := &fakeDoer{do: func(_ int, _ *http.Request) (*http.Response, error) { return original, nil }}
	transport := Env{EnvToken: "old-token"}.Transport()
	transport.base = doer
	req, _ := http.NewRequest(http.MethodGet, "https://loom.test/data", nil)
	resp, err := transport.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if resp != original || resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("X-Original") != "yes" || string(body) != "original body" || resp.ContentLength != int64(len(body)) {
		t.Fatalf("restored response = %#v body=%q", resp, body)
	}
	if len(doer.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.requests))
	}
}

func TestOccupantTransportBoundsRestored401Body(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	doer := &fakeDoer{do: func(_ int, _ *http.Request) (*http.Response, error) {
		return response(http.StatusUnauthorized, strings.Repeat("x", maxUnauthorizedBodyBytes+1024)), nil
	}}
	transport := Env{EnvToken: "old-token"}.Transport()
	transport.base = doer
	req, _ := http.NewRequest(http.MethodGet, "https://loom.test/data", nil)
	resp, err := transport.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != maxUnauthorizedBodyBytes {
		t.Fatalf("restored body length = %d, want %d", len(body), maxUnauthorizedBodyBytes)
	}
	if resp.ContentLength != maxUnauthorizedBodyBytes {
		t.Fatalf("restored ContentLength = %d, want %d", resp.ContentLength, maxUnauthorizedBodyBytes)
	}
}

func TestOccupantTransportRequiresReplayableBodyForRetry(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	doer := &fakeDoer{do: func(call int, _ *http.Request) (*http.Response, error) {
		if call == 1 {
			if err := WriteToken("new-token"); err != nil {
				return nil, err
			}
		}
		return response(http.StatusUnauthorized, "stale"), nil
	}}
	transport := Env{EnvToken: "old-token"}.Transport()
	transport.base = doer
	req, _ := http.NewRequest(http.MethodPost, "https://loom.test/data", io.NopCloser(strings.NewReader("payload")))
	if req.GetBody != nil {
		t.Fatal("test request unexpectedly replayable")
	}
	_, err := transport.Do(req)
	if err == nil || !strings.Contains(err.Error(), "cannot be replayed") {
		t.Fatalf("error = %v, want replay guard", err)
	}
	if len(doer.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.requests))
	}
}

func TestOccupantTransportPropagatesDoerError(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	want := errors.New("transport broke")
	doer := &fakeDoer{do: func(_ int, _ *http.Request) (*http.Response, error) { return nil, want }}
	transport := Env{EnvToken: "old-token"}.Transport()
	transport.base = doer
	req, _ := http.NewRequest(http.MethodGet, "https://loom.test/data", nil)
	if _, err := transport.Do(req); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
