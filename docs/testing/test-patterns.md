# Test Patterns & Conventions

> **Status:** Current · *audited 2026-07-23*

Common testing patterns, mocking strategies, and best practices used across the loomcli codebase.

---

## Table of Contents

1. [Go Test Patterns](#go-test-patterns)
2. [Frontend Test Patterns](#frontend-test-patterns)
3. [Mocking Strategies](#mocking-strategies)
4. [Concurrency Testing](#concurrency-testing)
5. [Test Helpers & Utilities](#test-helpers--utilities)
6. [Anti-Patterns to Avoid](#anti-patterns-to-avoid)

---

## Go Test Patterns

### Table-Driven Tests

The dominant pattern across the codebase. Used for testing multiple scenarios with the same assertion logic.

```go
func TestValidation(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    bool
        wantErr bool
    }{
        {name: "valid input", input: "abc", want: true},
        {name: "empty input", input: "", wantErr: true},
        {name: "too long", input: strings.Repeat("a", 256), wantErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            got, err := Validate(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
            }
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

**When to use**: Any function with multiple input/output combinations. Practically all validation, parsing, and transformation functions.

**Key conventions**:
- Always use `t.Run(tt.name, ...)` for subtests
- Always call `t.Parallel()` in subtests
- Name test cases descriptively (they appear in failure output)

### Setup/Teardown with Helpers

```go
func setupTest(t *testing.T) (*Client, *miniredis.Miniredis) {
    t.Helper()
    mr := miniredis.RunT(t)
    rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
    t.Cleanup(func() { rdb.Close() })
    return NewClientFromRedis(rdb), mr
}
```

**Key conventions**:
- Always call `t.Helper()` first (fixes stack traces)
- Use `t.Cleanup()` instead of `defer` (runs after test, not after function)
- Use `t.TempDir()` for temporary directories (auto-cleaned)
- Return dependencies that tests need for assertions

### JSON Round-Trip Testing

Used extensively in the RPC package to verify serialization correctness.

```go
func TestArgs_JSONRoundTrip(t *testing.T) {
    original := CreateArgs{
        Title:    "Test Issue",
        Priority: intPtr(2),
    }

    data, err := json.Marshal(original)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }

    var decoded CreateArgs
    if err := json.Unmarshal(data, &decoded); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }

    if !reflect.DeepEqual(original, decoded) {
        t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", decoded, original)
    }
}
```

**When to use**: Any struct that crosses process boundaries (RPC, API, SSE events).

### Pointer Field Testing

Critical for Update operations where nil means "don't change" and a pointer means "set to this value".

```go
func TestUpdateArgs_PointerFields(t *testing.T) {
    // Nil pointer = field not included in JSON
    args := UpdateArgs{Title: nil}
    data, _ := json.Marshal(args)
    if strings.Contains(string(data), "title") {
        t.Error("nil pointer should be omitted")
    }

    // Set pointer = field included
    title := "New Title"
    args = UpdateArgs{Title: &title}
    data, _ = json.Marshal(args)
    if !strings.Contains(string(data), "title") {
        t.Error("set pointer should be included")
    }
}
```

### HTTP Handler Testing

Used in the WebUI package with `httptest`.

```go
func TestHandler(t *testing.T) {
    handler := NewMyHandler(deps)

    req := httptest.NewRequest("GET", "/api/issues", nil)
    req.Header.Set("Authorization", "Bearer test-token")
    w := httptest.NewRecorder()

    handler.ServeHTTP(w, req)

    if w.Code != http.StatusOK {
        t.Errorf("status = %d, want 200", w.Code)
    }

    var resp []Issue
    json.NewDecoder(w.Body).Decode(&resp)
    // assert on resp...
}
```

### Process and File Testing

```go
func TestLockFile(t *testing.T) {
    dir := t.TempDir()
    lockPath := filepath.Join(dir, "daemon.lock")

    // Create lock
    err := TryDaemonLock(lockPath)
    if err != nil {
        t.Fatalf("lock: %v", err)
    }

    // Verify lock file content
    data, _ := os.ReadFile(lockPath)
    var info LockInfo
    json.Unmarshal(data, &info)
    if info.PID != os.Getpid() {
        t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
    }
}
```

---

## Frontend Test Patterns

### Hook Testing with renderHook

```typescript
import { renderHook, act, waitFor } from '@testing-library/react';

test('useIssues fetches on mount', async () => {
    const mockFetch = vi.fn().mockResolvedValue([{ id: '1', title: 'Test' }]);
    vi.mocked(fetchIssues).mockImplementation(mockFetch);

    const { result } = renderHook(() => useIssues());

    await waitFor(() => {
        expect(result.current.issues).toHaveLength(1);
    });

    expect(mockFetch).toHaveBeenCalledOnce();
});
```

**When to use**: All custom hooks. `renderHook` provides proper React lifecycle.

### Accessibility-First Component Queries

The codebase prioritizes semantic queries over test IDs.

```typescript
// Preferred (accessibility-first)
screen.getByRole('button', { name: 'Create Issue' });
screen.getByLabelText('Priority');
screen.getByText('No issues found');

// Acceptable (for complex components)
screen.getByTestId('kanban-column-open');

// Avoid (fragile)
container.querySelector('.btn-primary');
```

**Query priority** (from React Testing Library docs):
1. `getByRole` - Accessible role
2. `getByLabelText` - Form labels
3. `getByText` - Visible text
4. `getByTestId` - Last resort

### Optimistic Update Testing

```typescript
test('optimistic update with rollback', async () => {
    // Setup: API will reject
    vi.mocked(updateIssue).mockRejectedValue(new Error('Server error'));

    const { result } = renderHook(() => useIssues());

    // Wait for initial load
    await waitFor(() => expect(result.current.issues).toHaveLength(1));

    // Perform optimistic update
    await act(async () => {
        result.current.updateStatus('issue-1', 'closed');
    });

    // Verify optimistic update applied immediately
    expect(result.current.issues[0].status).toBe('closed');

    // Verify rollback after API failure
    await waitFor(() => {
        expect(result.current.issues[0].status).toBe('open');
    });
});
```

**Why**: Optimistic updates give instant feedback. The test verifies both the happy path (immediate update) and error path (rollback).

### Fake Timer Testing

```typescript
test('retry with exponential backoff', async () => {
    vi.useFakeTimers();

    const { result } = renderHook(() => useAgents());

    // First retry at 1s
    await act(async () => {
        vi.advanceTimersByTime(1000);
    });
    expect(fetchAgents).toHaveBeenCalledTimes(2);

    // Second retry at 2s
    await act(async () => {
        vi.advanceTimersByTime(2000);
    });
    expect(fetchAgents).toHaveBeenCalledTimes(3);

    vi.useRealTimers();
});
```

**Important**: Always restore real timers. Fake timers break `AbortController` and other time-dependent APIs.

### Playwright E2E with Page Object Model

The real page objects live in `internal/webui/frontend/tests/pages/`:
`app.page.ts`, `kanban.page.ts` (`KanbanPage` at `tests/pages/kanban.page.ts:14`),
`monitor.page.ts`, `table.page.ts`. The sketch below is the shape they follow.

```typescript
// Page Object
class KanbanPage {
    constructor(private page: Page) {}

    async getColumnCardCount(status: string): Promise<number> {
        const column = this.page.locator(`[data-column="${status}"]`);
        return column.locator('[data-testid="issue-card"]').count();
    }

    async dragCard(cardId: string, targetColumn: string) {
        // Drag implementation
    }
}

// Test
test('move card between columns', async ({ page }) => {
    const kanban = new KanbanPage(page);

    await expect(kanban.getColumnCardCount('open')).toBe(3);
    await kanban.dragCard('issue-1', 'in_progress');
    await expect(kanban.getColumnCardCount('open')).toBe(2);
    await expect(kanban.getColumnCardCount('in_progress')).toBe(1);
});
```

### Playwright Fixture Pattern

The real fixtures are defined in
`internal/webui/frontend/tests/fixtures/base.ts:31-60` and re-exported from
`tests/fixtures/index.ts`. Import `{ test, expect }` from `../fixtures`, not
from `@playwright/test`.

```typescript
// tests/fixtures/base.ts
type TestFixtures = {
    mockApi: MockApi;
    mockSSE: MockSSE;
    appPage: AppPage;
};

export const test = base.extend<TestFixtures>({
    mockApi: async ({ page }, use) => {
        const mock = new MockApi(page);
        await mock.setup();
        await use(mock);
    },
    mockSSE: async ({ page }, use) => {
        const mock = new MockSSE(page);
        await use(mock);
    },
    appPage: async ({ page, mockApi, mockSSE }, use) => {
        const app = new AppPage(page);
        await app.goto();
        await use(app);
    },
});

// Usage in tests
test('kanban shows issues', async ({ appPage, mockApi }) => {
    await mockApi.respondWith('/api/issues', mockIssues);
    await expect(appPage.issueCount()).toBe(5);
});
```

---

## Mocking Strategies

### Go: Mock Unix Socket Server

Used in RPC client tests.

```go
func startMockServer(t *testing.T) (string, func()) {
    t.Helper()
    dir := t.TempDir()
    socketPath := filepath.Join(dir, "test.sock")

    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        t.Fatal(err)
    }

    go func() {
        for {
            conn, err := listener.Accept()
            if err != nil {
                return
            }
            go handleMockConnection(conn)
        }
    }()

    return socketPath, func() { listener.Close() }
}
```

### Go: miniredis for Redis

```go
func setupRedis(t *testing.T) (*Client, *miniredis.Miniredis) {
    mr := miniredis.RunT(t)
    client := NewClient(mr.Addr())
    t.Cleanup(func() { client.Close() })
    return client, mr
}

// Time simulation
mr.FastForward(30 * time.Second)  // Expire TTLs
```

**Why miniredis over real Redis**: No external dependency, deterministic time control, parallel-safe.

### Frontend: API Module Mock

```typescript
vi.mock('@/api', () => ({
    fetchIssues: vi.fn(),
    updateIssue: vi.fn(),
    fetchAgents: vi.fn(),
}));

// In test
vi.mocked(fetchIssues).mockResolvedValue(mockIssues);
vi.mocked(updateIssue).mockRejectedValue(new Error('fail'));
```

### Frontend: Custom MockEventSource

```typescript
class MockEventSource {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSED = 2;

    readyState = MockEventSource.CONNECTING;
    onopen: ((ev: Event) => void) | null = null;
    onerror: ((ev: Event) => void) | null = null;

    simulateOpen() {
        this.readyState = MockEventSource.OPEN;
        this.onopen?.(new Event('open'));
    }

    simulateError() {
        this.readyState = MockEventSource.CLOSED;
        this.onerror?.(new Event('error'));
    }

    simulateMutation(data: any) {
        // Dispatch event to registered listeners
    }
}
```

**Why custom over library**: Full control over EventSource behavior, deterministic event ordering, easy state inspection.

### Frontend: Hoisted Mock Pattern

```typescript
const { mockUseSSE, mockUseIssues } = vi.hoisted(() => ({
    mockUseSSE: vi.fn().mockReturnValue({ connected: true }),
    mockUseIssues: vi.fn().mockReturnValue({ issues: [], loading: false }),
}));

vi.mock('@/hooks/useSSE', () => ({ useSSE: mockUseSSE }));
vi.mock('@/hooks/useIssues', () => ({ useIssues: mockUseIssues }));
```

**Why hoisted**: Ensures mock is defined before `vi.mock()` runs (which is hoisted by Vitest).

---

## Concurrency Testing

### Go: Basic Goroutine Safety

```go
func TestConcurrentAccess(t *testing.T) {
    client := NewClient()
    var wg sync.WaitGroup

    // 10 writers
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 100; j++ {
                client.SetTimeout(time.Duration(j) * time.Second)
            }
        }()
    }

    // 10 readers
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 100; j++ {
                _ = client.GetTimeout()
            }
        }()
    }

    wg.Wait()
}
```

**Run with**: `go test -race ./...` to detect data races.

### Go: Stress Testing

```go
func TestStressTest(t *testing.T) {
    client := NewClient()
    var wg sync.WaitGroup

    // 50 setters + 100 readers, 200 ops each
    for i := 0; i < 50; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 200; j++ {
                client.SetActor("agent-" + strconv.Itoa(j))
            }
        }()
    }

    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 200; j++ {
                _ = client.Execute(Request{Op: "ping"})
            }
        }()
    }

    wg.Wait()
}
```

### Go: Redis Concurrent Claims

```go
func TestConcurrentClaims(t *testing.T) {
    client, mr := setupRedis(t)

    var wg sync.WaitGroup
    results := make([]error, 10)

    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            results[idx] = client.ClaimTask(ctx, "task-1", fmt.Sprintf("worker-%d", idx))
        }(i)
    }

    wg.Wait()

    // Exactly one should succeed
    successes := 0
    for _, err := range results {
        if err == nil {
            successes++
        }
    }
    if successes != 1 {
        t.Errorf("expected 1 success, got %d", successes)
    }
}
```

### Frontend: Race Condition Prevention

```typescript
test('SSE mutation during refetch preserves data', async () => {
    const { result } = renderHook(() => useIssues());

    // Trigger refetch
    await act(async () => {
        result.current.refetch();
    });

    // While refetch is in-flight, simulate SSE mutation
    await act(async () => {
        mockSSE.simulateMutation({
            type: 'issue_updated',
            data: { id: 'issue-1', status: 'closed' },
        });
    });

    // Both refetch result AND SSE mutation should be present
    await waitFor(() => {
        expect(result.current.issues.find(i => i.id === 'issue-1')?.status).toBe('closed');
    });
});
```

---

## Test Helpers & Utilities

### Go Helpers

| Helper | Location | Purpose |
|--------|----------|---------|
| `setupTest()` | Multiple packages | Create test client with dependencies |
| `mustJSON()` | `internal/cli/compat_test.go:29`, `internal/cli/automode/automode_test.go:508` (wrapped at `internal/cli/agent/testhelpers_test.go:54`) | Marshal to JSON or panic |
| `runMake()` | `makefile_test.go:27` | Execute make target and return output |
| `skipIfNoTmux()` | `internal/cli/automode_e2e_test.go:17` | Skip test if tmux unavailable |
| `uniqueSessionName()` | `internal/cli/automode_e2e_test.go:25` | Generate unique tmux session name |
| `intPtr()` / `strPtr()` | `internal/types/types_test.go:1227`, `internal/backend/api/params_test.go:11,13` | Create pointers to literals |

### Frontend Helpers

Paths are relative to `internal/webui/frontend/`. Note the split: browser
helpers live under `tests/`, not `tests/e2e/` — `tests/e2e/helpers/` holds only
`fleet.ts`.

| Helper | Location | Purpose |
|--------|----------|---------|
| `createIssue()` | `tests/helpers/test-data.ts:29` | Factory with sensible defaults |
| `createStats()` | `tests/helpers/test-data.ts:77` | Mock statistics |
| `createKanbanData()` | `tests/helpers/test-data.ts:148` | Complete board data |
| `resetIdCounter()` | `tests/helpers/test-data.ts:23` | Reset between tests |
| `generateTestId()` | `tests/e2e/api/api-client.ts:711` | Unique IDs: `test-{ts}-{random}` |
| `waitFor()` | `tests/e2e/api/api-client.ts:719` | Polling with timeout and predicate |

Other helper modules in `tests/helpers/`: `api-mock.ts`, `sse-mock.ts`,
`fixture-routes.ts`, `keyboard-setup.ts`, `terminal-seed.ts`, `wait.ts`.

---

## Anti-Patterns to Avoid

### 1. Testing Implementation Details

```go
// Bad: tests internal state
if client.retryCount != 3 {
    t.Error("wrong retry count")
}

// Good: tests observable behavior
resp, err := client.Execute(req)
if err != nil {
    t.Error("request should succeed after retries")
}
```

### 2. Shared Mutable State Between Tests

```go
// Bad: global variable modified by tests
var globalClient *Client

func TestA(t *testing.T) {
    globalClient = NewClient()  // Races with TestB
}

// Good: each test creates its own
func TestA(t *testing.T) {
    t.Parallel()
    client := NewClient()  // Isolated
}
```

### 3. Sleeping Instead of Waiting

```typescript
// Bad: arbitrary sleep
await new Promise(resolve => setTimeout(resolve, 1000));
expect(result).toBe(expected);

// Good: wait for condition
await waitFor(() => {
    expect(result).toBe(expected);
});
```

### 4. Testing Without Race Detection

```bash
# Bad: misses data races
go test ./...

# Good: catches data races
go test -race ./...
```

### 5. Non-Deterministic Test Order

```go
// Bad: test depends on other test running first
func TestB(t *testing.T) {
    // Assumes TestA already ran and set up state
}

// Good: each test sets up its own state
func TestB(t *testing.T) {
    t.Parallel()
    state := setupFreshState(t)
}
```

### 6. Brittle Selectors in E2E

```typescript
// Bad: breaks when CSS changes
page.locator('.MuiButton-root.MuiButton-contained');

// Good: semantic selector
page.getByRole('button', { name: 'Create Issue' });
```

### Mechanically enforced anti-patterns

Some "don't do X" rules are not conventions any more — they fail the gate.
They run as steps of `check-go` and `check-frontend` (`Makefile:486`, `:520`):

| Rule | Enforced by |
|---|---|
| No raw `exec.Command` in production code | `scripts/check-no-raw-exec.sh` (check-go step 8) |
| No `log.Printf` — use structured logging | `scripts/check-no-log-printf.sh` (step 9) |
| No new production beads/`bd` references | `scripts/check-no-beads-prod.sh` (step 10) |
| No raw `fetch()` in the frontend — use the API client | `npm run check:no-raw-fetch` (check-frontend step 4) |
| No hardcoded URLs in the frontend | `npm run check:no-hardcoded-urls` (step 4) |
| Component/layer boundaries | `npm run check:boundaries` (step 4); Go equivalent is `depguard` in `.golangci.yml` |

See [test-infrastructure.md](test-infrastructure.md) for the full step lists.

---

## Related

- [README.md](README.md) — testing docs index
- [test-infrastructure.md](test-infrastructure.md) — CI, make targets, gate steps, coverage thresholds
- [frontend-tests.md](frontend-tests.md) — where the frontend tests actually live
- [../testing-terminology.md](../testing-terminology.md) — depth / realness / provisioning / polarity and the trap words
