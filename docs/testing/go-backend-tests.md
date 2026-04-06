# Go Backend Tests

Complete breakdown of all Go test files, organized by package.

---

## Table of Contents

1. [Root Level](#root-level)
2. [internal/cli](#internalcli)
3. [internal/webui](#internalwebui)
4. [internal/rpc](#internalrpc)
5. [internal/kv](#internalkv)
6. [internal/types](#internaltypes)
7. [internal/circuitbreaker](#internalcircuitbreaker)
8. [internal/lockfile](#internallockfile)
9. [internal/debug](#internaldebug)
10. [third_party/beads](#third_partybeads)

---

## Root Level

### `makefile_test.go`

**Purpose**: Meta-tests that validate the build system itself - Makefile targets, configuration files, and dev scripts.

| Test Function | What It Validates |
|---|---|
| `TestMakefilePhonyDeclarations` | `.PHONY` declarations exist for sync-beads, update-beads |
| `TestMakefileBeadsVariables` | `BEADS_REMOTE`, `BEADS_BRANCH`, `BEADS_PREFIX` variables defined |
| `TestMakeHelp_IncludesBeadsTargets` | `make help` output mentions beads targets |
| `TestMakeDryRun_SyncBeads` | Dry-run sync-beads invokes the correct script |
| `TestMakeDryRun_UpdateBeads` | Dry-run update-beads uses `git subtree pull` |
| `TestMakefileDevPhonyDeclarations` | Dev workflow targets are `.PHONY` |
| `TestMakeHelp_IncludesDevTargets` | `make help` lists dev targets |
| `TestMakeDryRun_Dev` | Dev target invokes `scripts/dev.sh` |
| `TestMakeDevCheck` | Dev dependency checking target exists |
| `TestMakeDevCheckFailsWithoutAir` | Fails correctly when `air` is missing |
| `TestMakefileDevTargetDependsOnDevCheck` | `dev` prerequisite chain is correct |
| `TestGitignoreIncludesTmp` | `.gitignore` has `tmp/` entry |
| `TestGitignoreIncludesNodeModules` | `.gitignore` excludes `node_modules` |
| `TestAirTomlExists` | `.air.toml` file exists |
| `TestAirTomlTmpDir` | Air configured with correct temp directory |
| `TestAirTomlBuildCmd` | Air build command is correct |
| `TestAirTomlExcludesFrontend` | Frontend excluded from Air file watching |
| `TestDevShExists` | `scripts/dev.sh` exists and is executable |

**Why**: Prevents silent build breakage. If someone modifies the Makefile or dev config, these tests catch regressions before they reach CI.

**Patterns**: File content parsing via `os.ReadFile`, `exec.Command` for dry-run validation.

---

## internal/cli

The CLI package has **40+ test files** - the largest test surface in the project. Tests cover every CLI command, daemon lifecycle, backend integrations, and utility functions.

### Command Tests

#### `automode_test.go`

**Purpose**: Tests the automated task selection logic that picks the next issue for an agent to work on.

| Test Function | What It Validates |
|---|---|
| `TestHasAvailablePlanningTasks` | Detects issues needing planning (no design field) |
| `TestFilterTasksForAgent` | Filters tasks by status, dependencies, assignee |
| `TestSelectNextTask` | Priority-based task selection algorithm |
| `TestAutoModeLoop` | Full auto-mode iteration cycle |

**Why**: Auto-mode is the core agent orchestration feature. Incorrect task selection leads to agents working on wrong/blocked issues.

**Patterns**: Table-driven tests, JSON fixture files in `testdata/`.

#### `automode_e2e_test.go`

**Purpose**: End-to-end auto-mode tests requiring real tmux sessions.

**Build tag**: `//go:build e2e`

| Test Function | What It Validates |
|---|---|
| `TestE2E_TmuxSessionLifecycle` | Tmux session creation, command execution, cleanup |
| `TestE2E_AutomodeWithRealDaemon` | Full auto-mode with real daemon + tmux |

**Why**: Unit tests mock the daemon; E2E tests verify real system integration.

**Patterns**: `skipIfNoTmux(t)` guard, unique session names, defer cleanup.

#### `agent_cmd_test.go`

**Purpose**: Tests the `loom agent` command for managing agent processes.

**Why**: Agents are the core unit of parallel work. Command correctness ensures proper agent lifecycle management.

#### `claim_test.go`

**Purpose**: Tests `loom claim` command for task claiming.

**Why**: Task claiming must be atomic and handle contention correctly to prevent duplicate work.

#### `complete_test.go`

**Purpose**: Tests `loom complete` command for marking tasks done.

**Why**: Completion triggers state transitions and unblocks dependent work.

#### `config_cmd_test.go`

**Purpose**: Tests `loom config` command for daemon configuration.

**Why**: Misconfigured daemons lead to hard-to-debug failures.

#### `daemon_cmd_test.go` / `daemon_test.go` / `daemon_config_test.go` / `daemon_ensure_test.go`

**Purpose**: Tests daemon lifecycle - starting, stopping, health checking, configuration.

| Area | What It Validates |
|---|---|
| `daemon_cmd_test.go` | CLI command parsing and execution |
| `daemon_test.go` | Core daemon functionality |
| `daemon_config_test.go` | Configuration loading and validation |
| `daemon_ensure_test.go` | Daemon auto-start and health verification |

**Why**: The daemon is the persistent backend process. Lifecycle bugs cause data loss or orphaned processes.

#### `exec_test.go`

**Purpose**: Tests `loom exec` for running commands in agent context.

**Why**: Exec bridges CLI commands to agent workspaces.

#### `init_test.go`

**Purpose**: Tests `loom init` project initialization.

**Why**: Init creates directory structures and config files that all other commands depend on.

#### `lead_test.go`

**Purpose**: Tests leader election for multi-instance coordination.

**Why**: Leader election prevents duplicate work in multi-agent setups.

#### `list_test.go`

**Purpose**: Tests `loom list` for issue listing with filters and formatting.

**Why**: List is the primary issue discovery command. Filter bugs hide available work.

#### `lock_test.go`

**Purpose**: Tests file-based locking for daemon exclusivity.

**Why**: Without proper locking, multiple daemons corrupt the database.

#### `plan_test.go`

**Purpose**: Tests planning-related CLI commands.

**Why**: Planning is the first step in the agent workflow; incorrect plans waste agent time.

#### `pull_test.go` / `push_test.go`

**Purpose**: Tests git pull/push integration for syncing beads data.

**Why**: Data sync is critical for multi-machine collaboration.

#### `recover_test.go` / `reset_test.go`

**Purpose**: Tests recovery from corrupted or stuck states.

**Why**: Recovery commands are the last resort when things go wrong. They must work reliably.

#### `serve_test.go` / `serve_daemon_test.go`

**Purpose**: Tests `loom serve` command that starts the web UI and API servers.

**Why**: Serve is the entry point for the web interface. Port binding, auth, and proxy configuration must be correct.

#### `sync_test.go`

**Purpose**: Tests `loom sync` for beads data synchronization.

**Why**: Sync must handle conflicts, network failures, and partial updates gracefully.

#### `task_test.go`

**Purpose**: Tests task management operations.

**Why**: Tasks are the work units that agents execute.

#### `workspace_*.go` / `worktree_*.go`

**Purpose**: Tests git worktree management for parallel agent workspaces.

**Why**: Each agent gets an isolated worktree. Incorrect management causes git corruption or file conflicts.

### Backend Integration Tests

#### `backend_test.go` / `backend_cmd_construction_test.go`

**Purpose**: Tests how CLI commands are constructed for different AI backends.

**Why**: Each backend has different command-line interfaces. Construction bugs cause agent launch failures.

#### `backend_claude_test.go` / `backend_codex_test.go` / `backend_opencode_test.go`

**Purpose**: Backend-specific tests for each supported AI backend (Claude Code, OpenAI Codex, OpenCode).

**Why**: Each backend has unique flags, environment variables, and invocation patterns. These tests verify correct command construction per backend.

#### `backend_stdin_test.go`

**Purpose**: Tests stdin piping to backend processes.

**Why**: Prompt injection happens via stdin. Correct piping is essential.

#### `backend_pty_integration_test.go`

**Purpose**: Tests PTY (pseudo-terminal) integration with backends.

**Why**: PTY is needed for interactive terminal features. Integration bugs cause garbled output.

### Utility Tests

#### `envfilter_test.go`

**Purpose**: Tests environment variable filtering when spawning agent processes.

**Why**: Prevents leaking sensitive env vars to agent subprocesses.

#### `prompts_test.go`

**Purpose**: Tests prompt template generation.

**Why**: Prompts define agent behavior. Template bugs change agent instructions.

#### `completions_test.go`

**Purpose**: Tests shell completion generation (bash, zsh, fish).

**Why**: Quality-of-life feature. Broken completions frustrate users.

#### `config_test.go`

**Purpose**: Tests configuration loading, defaults, and validation (separate from the `config` CLI command tests).

**Why**: Config drives daemon and CLI behavior. Invalid config causes hard-to-diagnose failures.

#### `signal_dir_unix_test.go`

**Purpose**: Tests Unix signal directory handling for daemon communication.

**Why**: Signal directories are used for inter-process communication on Unix systems.

#### `lock_workspace_test.go`

**Purpose**: Tests workspace-level locking to prevent concurrent operations on the same workspace.

**Why**: Without workspace locks, two agents could corrupt the same worktree.

#### `isatty_darwin_test.go` / `isatty_linux_test.go`

**Purpose**: Platform-specific TTY detection tests.

**Why**: Output formatting (colors, progress bars) depends on TTY detection.

#### `git_test.go`

**Purpose**: Tests git operations (branch, commit, status).

**Why**: Git is integral to worktree management and sync.

#### `pr_test.go`

**Purpose**: Tests pull request creation and management.

**Why**: PR workflow is a key developer-facing feature.

#### `monitor_test.go` / `monitor_detail_test.go`

**Purpose**: Tests the terminal-based monitoring dashboard.

**Why**: Monitor displays real-time agent status. Rendering bugs hide critical information.

#### `integration_test.go`

**Purpose**: Broader CLI integration tests combining multiple components.

**Why**: Catches interaction bugs between components that unit tests miss.

#### `testutil_test.go`

**Purpose**: Tests for test utilities themselves.

**Why**: Ensures test helpers produce correct test data.

---

## internal/webui

The WebUI package has **13 test files** plus subdirectories for fleet and daemon tests.

The WebUI package has **35 test files** including subdirectories for fleet and daemon tests.

### Core HTTP Tests

#### `auth_test.go`

**Purpose**: Tests authentication middleware and API key validation.

| Test Area | What It Validates |
|---|---|
| API key validation | Correct keys pass, invalid keys rejected |
| Token generation | JWT token creation with proper claims |
| Header parsing | Authorization header extraction |

**Why**: Auth is the security boundary. Bypass bugs expose the API.

#### `contract_test.go`

**Purpose**: Tests API contract compliance - request/response schemas.

**Why**: Frontend depends on exact API shapes. Contract breaks cause UI errors.

#### `cors_test.go`

**Purpose**: Tests CORS (Cross-Origin Resource Sharing) headers.

**Why**: Incorrect CORS blocks the frontend from making API calls.

#### `embed_test.go`

**Purpose**: Tests embedded frontend file serving via `//go:embed`.

**Why**: The production frontend is served from embedded files. Embedding bugs show blank pages.

#### `jwt_test.go`

**Purpose**: Tests JWT token generation, validation, and expiration.

| Test Area | What It Validates |
|---|---|
| Token creation | Correct claims, signing method |
| Token validation | Signature verification, expiry checking |
| Token refresh | Refresh flow before expiration |

**Why**: JWT is used for session management. Token bugs cause auth failures or security holes.

#### `log_open_test.go`

**Purpose**: Tests log file opening and streaming.

**Why**: Agent logs are critical for debugging. Access bugs hide diagnostic info.

#### `metrics_test.go`

**Purpose**: Tests metrics collection endpoints.

**Why**: Metrics drive the monitoring dashboard.

#### `ratelimit_test.go`

**Purpose**: Tests rate limiting middleware.

**Why**: Prevents API abuse and protects the daemon from overload.

#### `security_test.go`

**Purpose**: Tests security headers and protections.

**Why**: Missing security headers expose XSS, clickjacking risks.

### Terminal Tests

#### `terminal_auth_test.go` / `terminal_test.go` / `handlers_terminal_test.go`

**Purpose**: Tests WebSocket-based terminal relay to tmux sessions.

| Test Area | What It Validates |
|---|---|
| WebSocket upgrade | HTTP upgrade handshake |
| Terminal input/output | Bidirectional data flow |
| Authentication | Terminal session auth tokens |
| Resize handling | Terminal dimension changes |

**Why**: The terminal is how users interact with agents in the browser. WebSocket bugs cause frozen or garbled terminals.

### SSE Tests

#### `sse_live_test.go`

**Purpose**: Tests live Server-Sent Events connections.

| Test Area | What It Validates |
|---|---|
| Connection setup | SSE stream initialization |
| Event parsing | Message format, event types |
| Reconnection | Auto-reconnect with Last-Event-ID |
| Client cleanup | Proper connection teardown |

**Why**: SSE pushes real-time updates (issue changes, agent status) to the browser. Broken SSE means stale UI.

#### `sse_push_test.go`

**Purpose**: Tests the SSE hub that broadcasts mutation events.

**Why**: The hub fans out events to all connected clients. Hub bugs cause missed updates or memory leaks.

### General Handler & Server Tests

#### `handlers_test.go`

**Purpose**: Tests general HTTP request handlers (issue CRUD, stats, health).

**Why**: These handlers form the main API surface used by the frontend.

#### `handlers_config_test.go`

**Purpose**: Tests configuration API endpoints.

**Why**: Config endpoints control daemon behavior at runtime.

#### `handlers_logs_test.go`

**Purpose**: Tests log streaming HTTP handlers.

**Why**: Log handlers serve agent output to the browser for real-time debugging.

#### `routes_test.go`

**Purpose**: Tests HTTP route registration and path matching.

**Why**: Incorrect routes silently break API endpoints.

#### `server_test.go`

**Purpose**: Tests server initialization, middleware chain, and shutdown.

**Why**: Server startup bugs prevent the entire web UI from working.

#### `subscription_test.go`

**Purpose**: Tests SSE subscription management (client registration, event routing).

**Why**: Subscriptions control which clients receive which events.

#### `sse_test.go`

**Purpose**: Tests SSE event formatting and delivery (separate from `sse_live_test.go` connection tests and `sse_push_test.go` hub tests).

**Why**: Covers the SSE serialization layer between hub and HTTP stream.

#### `fleet_ratelimit_test.go`

**Purpose**: Tests rate limiting specific to fleet API endpoints.

**Why**: Fleet endpoints are network-exposed and need stricter rate limiting than local APIs.

### Fleet Subdirectory (`internal/webui/fleet/`)

#### `metrics_test.go`

**Purpose**: Tests fleet-wide metrics aggregation.

**Why**: Fleet mode manages multiple agents. Metrics bugs hide agent health issues.

#### `signing_key_test.go`

**Purpose**: Tests cryptographic signing key management.

**Why**: Signing keys authenticate inter-node communication.

#### `store_test.go`

**Purpose**: Tests fleet data store operations.

**Why**: Fleet store holds agent state across nodes.

#### `timeout_test.go`

**Purpose**: Tests timeout handling for fleet operations.

**Why**: Network timeouts between nodes must be handled gracefully.

#### `fleet_auth_test.go` / `fleet_handlers_test.go`

**Purpose**: Tests fleet authentication and HTTP handlers.

**Why**: Fleet endpoints are exposed over the network, making auth critical.

### Daemon Subdirectory (`internal/webui/daemon/`)

#### `discovery_test.go`

**Purpose**: Tests service discovery for finding daemon instances.

**Why**: WebUI must discover running daemons to proxy requests.

#### `errors_test.go`

**Purpose**: Tests error types and handling.

**Why**: Proper error typing enables correct retry/fallback behavior.

#### `breaker_test.go`

**Purpose**: Tests circuit breaker integration with daemon connections.

**Why**: Circuit breakers prevent cascading failures when the daemon is down.

#### `connection_test.go`

**Purpose**: Tests Unix socket connection management.

**Why**: All daemon communication goes through Unix sockets. Connection bugs break everything.

#### `pool_test.go`

**Purpose**: Tests connection pooling for daemon connections.

**Why**: Connection pooling reduces latency and resource usage.

#### `testhelpers_test.go`

**Purpose**: Tests for test helper functions used by other daemon tests.

---

## internal/rpc

The RPC package has **6 test files** covering the protocol, client, authentication, mutations, metrics, and socket paths.

### `protocol_test.go`

**Purpose**: Tests the RPC protocol message format - serialization of all request/response types.

| Test Area | What It Validates |
|---|---|
| `TestRequest_JSONRoundTrip` | Request serialization/deserialization |
| `TestResponse_JSONRoundTrip` | Success and error response formats |
| `TestCreateArgs_JSONRoundTrip` | Issue creation arguments |
| `TestCreateArgs_OmitEmpty` | Optional fields omitted correctly |
| `TestUpdateArgs_JSONRoundTrip` | Update arguments with pointer fields |
| `TestUpdateArgs_PointerFields` | Nil vs set pointer field distinction |
| `TestCloseArgs_JSONRoundTrip` | Close operation arguments |
| `TestListArgs_JSONRoundTrip` | List with filters |
| `TestDeleteArgs_JSONRoundTrip` | Delete operation |
| `TestBatchArgs_JSONRoundTrip` | Batch operations |
| `TestOperationConstants` | All 45+ operation constants are defined |
| `TestGateArgs_JSONRoundTrip` | Gate-related arguments |

**Why**: The RPC protocol is the contract between CLI and daemon. Serialization bugs cause silent data corruption.

**Patterns**: JSON marshal/unmarshal round-trips, pointer field nil checks.

### `client_test.go`

**Purpose**: Comprehensive RPC client tests including thread safety, error handling, and all RPC methods.

| Test Category | Tests | What It Validates |
|---|---|---|
| Configuration | `TestClient_SetTimeout/SetDatabasePath/SetActor` | Client configuration setters |
| Connection | `TestTryConnect_NoSocket/WithTimeout` | Socket connection handling |
| Thread Safety | 6 concurrency tests (50-100 goroutines) | RWMutex correctness under load |
| Health | `TestTryConnectWithTimeout_HealthyServer/UnhealthyServer` | Health check integration |
| RPC Methods | 30+ individual method tests | Every RPC operation works correctly |
| Error Paths | `TestClient_UnmarshalErrorPaths` (8 methods) | JSON decode failure handling |
| Execute Errors | `TestClient_ExecuteErrorPaths` (8 methods) | Network/socket error handling |
| Stress | `TestClient_StressTestConcurrentOperations` | 50 setters + 100 readers, 200 ops each |
| Socket | `TestListenAndDialRPC` | Unix socket listen/connect |

**Why**: The RPC client is used by every CLI command. Thread safety is critical because the daemon serves concurrent requests.

**Patterns**: Mock Unix socket server, `sync.WaitGroup` for goroutine coordination, `t.Parallel()`.

### `auth_test.go`

**Purpose**: Tests authentication token loading and management.

| Test Function | What It Validates |
|---|---|
| `TestTokenFilePath` | Token file path generation |
| `TestLoadAuthToken` (file missing) | Graceful handling of missing token |
| `TestLoadAuthToken` (file exists) | Token loaded correctly |
| `TestLoadAuthToken` (whitespace) | Whitespace trimming |
| `TestLoadAuthToken` (empty file) | Empty file handled |

**Why**: Auth tokens protect the daemon from unauthorized access.

### `metrics_test.go`

**Purpose**: Tests RPC metrics collection and reporting.

**Why**: Metrics track RPC call counts, latencies, and error rates for observability.

### `socket_path_test.go`

**Purpose**: Tests Unix socket path generation and validation.

**Why**: Socket paths must be unique per daemon and not exceed OS path length limits.

### `transport_windows_test.go`

**Purpose**: Tests Windows-specific transport (named pipes instead of Unix sockets).

**Why**: Windows doesn't support Unix sockets. This ensures the daemon works on Windows via named pipes.

### `mutation_test.go`

**Purpose**: Tests mutation event types used for SSE broadcasting.

| Test Function | What It Validates |
|---|---|
| `TestMutationConstants` | Mutation type string constants |
| `TestMutationEvent_JSONRoundTrip` | Event serialization |
| `TestMutationEvent_OmitEmpty` | Optional fields omitted in JSON |
| `TestMutationEvent_JSONTagConsistency` | snake_case tag convention |
| `TestMutationEvent_AllTypes` | All mutation types serialize correctly |

**Why**: Mutation events drive real-time UI updates. Incorrect events cause stale or wrong data display.

---

## internal/kv

The KV package has **4 test files** testing Redis-based task coordination.

### `scripts_test.go`

**Purpose**: Tests Lua scripts for atomic Redis operations (task claiming, heartbeats, completion).

| Test Function | What It Validates |
|---|---|
| `TestClaimTask_Success` | Task claim with Redis state verification |
| `TestClaimTask_AlreadyClaimed` | Claim contention handling |
| `TestClaimTask_IdempotentReclaim` | Re-claiming by same worker (idempotent) |
| `TestClaimTask_ReclaimAfterExpiry` | TTL expiration allows new claim |
| `TestHeartbeat_ExtendsAllTTLs` | Heartbeat extends all relevant TTLs |
| `TestHeartbeat_NoActiveSession` | Heartbeat without active claim |
| `TestHeartbeat_OwnershipLost` | Detects lost ownership |
| `TestCompleteTask_Success` | Task completion workflow |
| `TestCompleteTask_NotOwner` | Unauthorized completion rejected |
| `TestCompleteTask_TaskExpired` | Expired task handling |
| `TestClaimThenHeartbeatThenComplete` | Full workflow: claim -> heartbeat -> complete |
| `TestConcurrentClaims` | Race condition under concurrent claims |
| `TestOwnershipTransfer` | Ownership expires and transfers to new worker |

**Why**: Task coordination is the core distributed systems problem. Lua scripts run atomically in Redis. Bugs cause duplicate work, lost tasks, or deadlocks.

**Patterns**: `miniredis.RunT(t)` for in-memory Redis, `mr.FastForward()` for TTL simulation, table-driven tests.

### `client_test.go`

**Purpose**: Tests the Redis client wrapper with circuit breaker integration.

| Test Category | What It Validates |
|---|---|
| Construction | Client creation and configuration |
| Key builders | Redis key generation (`task:{id}:owner`, etc.) |
| Type conversion | `ToInt64`, `ToString` helpers |
| Input validation | ID format validation rules |
| Circuit breaker | All operations with breaker enabled/tripped |
| Leader operations | `SetLeaderKey`, `RenewLeaderKey`, `DeleteLeaderKey` |
| Task owner ops | `DeleteTaskOwner`, `GetTaskOwner` |

**Why**: The client wraps Redis operations with safety (validation, circuit breaker). Wrapper bugs bypass these protections.

### `stale_test.go`

**Purpose**: Tests stale worker detection and cleanup.

| Test Function | What It Validates |
|---|---|
| Leader election (single/multiple instances) | Only one leader at a time |
| `TestStaleDetector_DetectStaleWorkers` | Finds workers past TTL |
| `TestStaleDetector_CleanupWorker` | Removes stale worker data |
| `TestStaleDetector_NoStaleWorkers` | Empty result handling |
| `TestStaleDetector_CleanupWorker_AlreadyExpired` | Idempotent cleanup |
| `TestStaleDetector_CleanupWorker_TaskOwnedByDifferentWorker` | Ownership validation |
| `TestStaleDetector_GracefulShutdown` | Context cancellation |
| `TestStaleDetector_ReleaseLeadership/RenewLeadership` | Leadership lifecycle |
| `TestStaleDetector_Status` | Status reporting |
| `TestStaleDetector_FullCycle` | End-to-end detection + cleanup |
| Configuration tests | Default config with env overrides |
| `TestStaleDetector_RunCycle_WithReconciler` | Reconciler integration |

**Why**: Stale workers hold task locks forever if not cleaned up. Detection bugs cause permanent task lockout.

**Patterns**: `miniredis.FastForward()` for time simulation, context cancellation, env var overrides.

### `reconcile_test.go`

**Purpose**: Tests the reconciler that resets orphaned tasks.

| Test Function | What It Validates |
|---|---|
| `TestNewReconciler_DefaultPath/CustomPath` | Reconciler construction |
| `TestReconciler_ResetOrphanedTask_BinaryNotFound` | Error handling when `bd` binary missing |
| `TestReconciler_MultipleOrphanedTasks` | Batch reconciliation |
| `TestReconciler_EmptyTasks` | Empty input handling |
| `TestReconciler_ResetTask_EmptyTaskID` | Input validation |
| `TestReconciler_ResetTask_InvalidCharacters` | Input sanitization (command injection prevention) |

**Why**: Reconciliation recovers from crashes. Invalid input handling prevents command injection.

---

## internal/types

The types package has **8 test files** covering data validation, ID generation, and federation.

### `types_test.go`

**Purpose**: Tests issue validation rules.

| Test Area | What It Validates |
|---|---|
| Valid issue | Passes all validations |
| Missing title | Rejected |
| Title too long | Rejected |
| Invalid priority (low/high bounds) | Rejected |
| Invalid status | Rejected |
| Invalid issue type | Rejected |
| Negative estimated minutes | Rejected |

**Why**: Validation prevents invalid data from entering the database.

**Patterns**: Table-driven tests with expected error checks.

### `id_generator_test.go`

**Purpose**: Tests unique ID generation.

**Why**: IDs must be unique across all agents and machines. Collision bugs cause data corruption.

### `content_hash_test.go`

**Purpose**: Tests content hashing for change detection.

**Why**: Content hashes detect modifications. Hash bugs cause missed updates or false changes.

### `federation_test.go`

**Purpose**: Tests federation for multi-project data sharing.

**Why**: Federation allows beads to be shared across projects.

### `lock_check_test.go` / `lock_test.go`

**Purpose**: Tests type-level lock operations.

**Why**: Locks prevent concurrent modification of the same issue.

### `orphans_test.go`

**Purpose**: Tests orphan detection for issues without parents.

**Why**: Orphans indicate data integrity problems.

### `process_test.go`

**Purpose**: Tests process-related type operations.

**Why**: Process types track agent process lifecycle.

### `validate_import_test.go`

**Purpose**: Tests import data validation.

**Why**: Import validation prevents corrupted data from external sources.

---

## internal/circuitbreaker

### `breaker_test.go`

**Purpose**: Tests the circuit breaker state machine.

| Test Function | What It Validates |
|---|---|
| `TestBreaker_ClosedState_PassesThrough` | Normal operation (closed state) |
| `TestBreaker_TripsAfterThreshold` | Failure threshold triggers open state |
| `TestBreaker_OpenState_FailsFast` | Open state rejects immediately |
| `TestBreaker_TransitionsToHalfOpen` | Timeout triggers half-open probe |
| `TestBreaker_HalfOpen_ProbeSuccess_ClosesCircuit` | Successful probe closes circuit |
| `TestBreaker_HalfOpen_ProbeFailure_ReopensCircuit` | Failed probe reopens circuit |
| `TestBreaker_HalfOpen_ProbeFailure_AllowsNewProbeAfterTimeout` | Retry after failed probe |
| `TestBreaker_HalfOpen_ExcessProbes_Rejected` | Only one probe at a time |
| `TestBreaker_ShouldTrip_Classification` | Error classification (transient vs permanent) |
| `TestBreaker_SuccessResetsConsecutiveFailures` | Failure counter reset on success |
| `TestBreaker_ConcurrentAccess` | Thread safety with goroutines |
| `TestBreaker_Reset` | Manual reset to closed state |
| `TestBreaker_OnStateChange` | State change callback invocation |
| `TestExecuteWithResult` | Generic result wrapper |
| `TestExecuteWithResult_CircuitOpen` | Result wrapper with open circuit |
| `TestBreaker_StateString` | State string representation |
| `TestBreaker_DefaultConfig` | Configuration defaults |
| `TestBreaker_Stats` | Statistics collection |

**Why**: Circuit breakers protect the system from cascading failures. State machine bugs cause either unnecessary failures (always open) or no protection (never trips).

**Patterns**: State machine testing, time-based simulations, concurrency testing with goroutines.

---

## internal/lockfile

### `lock_test.go`

**Purpose**: Tests file-based locking for daemon process exclusivity.

| Test Category | Tests | What It Validates |
|---|---|---|
| ReadLockInfo | JSON/old format, not found, invalid, whitespace, empty, zero PID | Lock file parsing |
| CheckPIDFile | Not found, invalid PID, not running, current process, whitespace | PID validation |
| TryDaemonLock | No lock, unlocked, held, old format, dead PID, invalid | Lock acquisition |
| Flock | Blocking/non-blocking, unlock, contention | OS-level file locking |
| IsProcessRunning | Current, non-existent, parent, high PID, 0, negative | Process detection |
| Concurrent | `TestConcurrentLockAccess` | Concurrent probing safety |

**Why**: The lock file prevents multiple daemon instances from running simultaneously. Without this, the SQLite database gets corrupted.

**Patterns**: `flock` system call testing, process signal testing (`os.FindProcess`), JSON marshaling.

---

## internal/debug

### `debug_test.go`

**Purpose**: Tests debug output functions.

| Test Function | What It Validates |
|---|---|
| `TestEnabled` | Debug flag state |
| `TestLogf` | Formatted logging to stderr |
| `TestPrintf` | Formatted logging to stdout |
| `TestSetVerbose` | Verbose mode toggling |
| `TestSetQuietAndIsQuiet` | Quiet mode |
| `TestPrintNormal` | Normal output respects quiet mode |
| `TestPrintlnNormal` | Normal println respects quiet mode |

**Why**: Debug output controls what users and agents see. Quiet mode is essential for machine-readable output.

**Patterns**: `os.Pipe()` for output capture, mode flag testing.

---

## third_party/beads

The beads package contains **100+ test files** for the beads daemon - the persistent data layer.

### Key Test Areas

| Directory | Files | What It Tests |
|---|---|---|
| Root beads package | `beads_test.go`, daemon tests | Core beads operations |
| `internal/storage/sqlite/` | SQLite tests, benchmarks | Database operations, migrations |
| `internal/doctor/` | 20+ test files | Database integrity diagnostics |
| `internal/doctor/fix/` | Repair operation tests | Database repair operations |
| `cmd/` | Command tests | CLI command parsing |

### Notable Beads Tests

- **Daemon integration tests**: Full daemon lifecycle with real SQLite
- **Doctor diagnostic tests**: Database corruption detection and repair
- **Cross-backend tests**: JSONL round-trip serialization between backends
- **Routing tests**: Issue routing and federation
- **Benchmark tests**: SQLite performance under load (build tag: `bench`)

**Why**: Beads is the data persistence layer. Database bugs mean data loss. The extensive doctor/fix tests ensure data can always be recovered.
