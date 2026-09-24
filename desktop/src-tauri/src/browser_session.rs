//! Native bridge for local-desktop browser operator sessions.
//!
//! Workspace windows (loopback web UI) ask the shell for a short-lived
//! operator bearer so they can call the workspace-scoped browser routes with
//! `X-Loom-Operator-Session`. The shell never mints the bearer itself: it runs
//! the bundled `loom` sidecar (`loom local browser-session <action>`), which
//! talks to the local runtime over its peer-credential socket.
//!
//! Secrecy rules enforced here:
//! - a bearer only ever reaches the sidecar on stdin (never argv or env);
//! - issued bearers live in memory only ([`BrowserSessionState`]), keyed by the
//!   invoking webview and workspace, so the shell can revoke a window's bearers
//!   when it closes and every bearer on exit;
//! - re-issuing replaces and revokes only the calling webview's own bearer, so
//!   two windows on one workspace never invalidate each other;
//! - bearers and sidecar output are never logged.
//!
//! Caller rules: Tauri's capability ACL admits any loopback origin, which is
//! broader than the Loom UI (a workspace window can navigate to another local
//! port). The command therefore also checks the invoking webview's actual URL:
//! its origin must equal the runtime origin the shell itself opened through
//! `open_workspace_window`, and its route must be `/ws/<workspace>/...` for
//! the requested workspace. Anything else is denied before the sidecar runs.

use std::{
    collections::HashMap,
    io::{Read, Write},
    process::{Child, Command as StdCommand, Stdio},
    sync::{Arc, Mutex},
    thread,
    time::{Duration, Instant},
};

use serde_json::{json, Map, Value};
use tauri::{AppHandle, Manager, Runtime, Url, Webview};
use tauri_plugin_shell::ShellExt;

const SIDECAR_NAME: &str = "loom";
const MAX_WORKSPACE_LEN: usize = 128;
const MAX_TOKEN_LEN: usize = 4096;
const SIDECAR_TIMEOUT: Duration = Duration::from_secs(20);
const EXIT_REVOKE_TIMEOUT: Duration = Duration::from_secs(3);
const MAX_STDOUT_BYTES: u64 = 64 * 1024;

pub const CODE_INVALID: &str = "browser_invalid";
pub const CODE_BRIDGE_UNAVAILABLE: &str = "browser_operator_bridge_unavailable";
pub const CODE_CALLER_FORBIDDEN: &str = "browser_operator_caller_forbidden";

/// Identifies one bearer slot: the invoking webview's label plus workspace.
/// Each webview keeps its bearer in its own page memory, so the shell must
/// never let one webview's issue replace or revoke another's.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct TokenKey {
    webview: String,
    workspace: String,
}

impl TokenKey {
    pub fn new(webview: &str, workspace: &str) -> Self {
        Self {
            webview: webview.to_string(),
            workspace: workspace.to_string(),
        }
    }
}

/// Operator bearers issued through this shell, keyed by [`TokenKey`], plus the
/// origin of the Loom runtime the shell last opened. Memory only; a webview's
/// bearers are revoked when it is destroyed and all of them when the app exits.
#[derive(Default)]
pub struct BrowserSessionState {
    tokens: Mutex<HashMap<TokenKey, String>>,
    runtime_origin: Mutex<Option<String>>,
    // One async lock per slot serializes a webview's issue/refresh/revoke for
    // a workspace, so read-previous, sidecar call and record are atomic and a
    // racing call can never displace a live bearer without revoking it.
    slot_locks: Mutex<HashMap<TokenKey, Arc<tauri::async_runtime::Mutex<()>>>>,
}

impl BrowserSessionState {
    fn slot_lock(&self, key: &TokenKey) -> Arc<tauri::async_runtime::Mutex<()>> {
        let mut locks = self
            .slot_locks
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        locks.entry(key.clone()).or_default().clone()
    }

    fn current(&self, key: &TokenKey) -> Option<String> {
        self.tokens.lock().ok().and_then(|t| t.get(key).cloned())
    }

    /// Remove and return every bearer issued to `webview`, across workspaces.
    fn drain_webview(&self, webview: &str) -> Vec<(String, String)> {
        let Ok(mut tokens) = self.tokens.lock() else {
            return Vec::new();
        };
        let keys: Vec<TokenKey> = tokens
            .keys()
            .filter(|k| k.webview == webview)
            .cloned()
            .collect();
        keys.into_iter()
            .filter_map(|k| tokens.remove(&k).map(|t| (k.workspace, t)))
            .collect()
    }

    fn drain_all(&self) -> Vec<(String, String)> {
        match self.tokens.lock() {
            Ok(mut tokens) => tokens.drain().map(|(k, t)| (k.workspace, t)).collect(),
            Err(_) => Vec::new(),
        }
    }
}

/// Record `url`'s origin as the current Loom runtime origin. Called only by
/// the shell after the launcher opens a verified-healthy loopback runtime;
/// the latest runtime replaces any earlier one.
pub fn trust_runtime_origin<R: Runtime>(app: &AppHandle<R>, url: &Url) {
    if let Some(state) = app.try_state::<BrowserSessionState>() {
        if let Ok(mut origin) = state.runtime_origin.lock() {
            *origin = Some(url.origin().ascii_serialization());
        }
    }
}

/// Decide whether a webview at `caller` may manage the operator session for
/// `workspace`. The caller must be an `http` page on exactly the trusted
/// runtime origin, on a route under `/ws/<workspace>`.
pub fn authorize_caller(
    caller: &Url,
    trusted_origin: Option<&str>,
    workspace: &str,
) -> Result<(), String> {
    let Some(trusted) = trusted_origin else {
        return Err("no Loom runtime has been opened by this shell".to_string());
    };
    if caller.scheme() != "http" || caller.origin().ascii_serialization() != trusted {
        return Err("caller is not the Loom runtime this shell opened".to_string());
    }
    let mut segments = caller.path_segments().into_iter().flatten();
    let bound = match (segments.next(), segments.next()) {
        (Some("ws"), Some(ws)) => percent_decode(ws),
        _ => None,
    };
    if bound.as_deref() != Some(workspace) {
        return Err("caller route is not bound to the requested workspace".to_string());
    }
    Ok(())
}

/// Minimal percent-decoding for one path segment; `None` on malformed escapes
/// or non-UTF-8 results, which then fail the workspace comparison.
fn percent_decode(segment: &str) -> Option<String> {
    let bytes = segment.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            let hex = segment.get(i + 1..i + 3)?;
            out.push(u8::from_str_radix(hex, 16).ok()?);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8(out).ok()
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SessionAction {
    Issue,
    Refresh,
    Revoke,
}

impl SessionAction {
    fn as_str(self) -> &'static str {
        match self {
            SessionAction::Issue => "issue",
            SessionAction::Refresh => "refresh",
            SessionAction::Revoke => "revoke",
        }
    }
}

pub fn parse_action(raw: &str) -> Result<SessionAction, String> {
    match raw {
        "issue" => Ok(SessionAction::Issue),
        "refresh" => Ok(SessionAction::Refresh),
        "revoke" => Ok(SessionAction::Revoke),
        _ => Err("action must be one of issue, refresh, revoke".to_string()),
    }
}

/// A workspace key is passed to the sidecar as a separate argv element, so it
/// must be non-empty, bounded, free of whitespace/control characters, and must
/// not look like a flag.
pub fn validate_workspace(raw: &str) -> Result<(), String> {
    if raw.is_empty() {
        return Err("workspace is required".to_string());
    }
    if raw.chars().count() > MAX_WORKSPACE_LEN {
        return Err(format!(
            "workspace must be at most {MAX_WORKSPACE_LEN} characters"
        ));
    }
    if raw.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return Err("workspace must not contain whitespace or control characters".to_string());
    }
    if raw.starts_with('-') {
        return Err("workspace must not start with '-'".to_string());
    }
    Ok(())
}

/// Bearers travel to the sidecar as one stdin line, so they must be a single
/// bounded line of printable, non-whitespace characters.
pub fn validate_token(raw: &str) -> Result<(), String> {
    if raw.is_empty() || raw.len() > MAX_TOKEN_LEN {
        return Err("operator session token is missing or malformed".to_string());
    }
    if raw.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return Err("operator session token is missing or malformed".to_string());
    }
    Ok(())
}

fn failure(error: impl Into<String>, code: &str) -> Value {
    json!({ "ok": false, "error": error.into(), "code": code })
}

/// Parse the sidecar's stdout: exactly one JSON object. Anything else is a
/// bridge failure (the raw output is never echoed back or logged).
pub fn parse_sidecar_output(stdout: &[u8], exit_code: Option<i32>) -> Value {
    let text = String::from_utf8_lossy(stdout);
    match serde_json::from_str::<Value>(text.trim()) {
        Ok(Value::Object(mut obj)) => {
            let success = exit_code == Some(0);
            let reported_ok = obj.get("ok").and_then(Value::as_bool).unwrap_or(false);
            if !success || !reported_ok {
                obj.insert("ok".into(), Value::Bool(false));
                // A failed response must never carry a usable bearer.
                obj.remove("token");
                if !obj.contains_key("code") {
                    obj.insert("code".into(), Value::String(CODE_BRIDGE_UNAVAILABLE.into()));
                }
                if !obj.contains_key("error") {
                    obj.insert(
                        "error".into(),
                        Value::String("browser operator session helper failed".into()),
                    );
                }
            }
            Value::Object(obj)
        }
        _ => failure(
            match exit_code {
                Some(code) => {
                    format!("browser operator session helper returned no JSON (exit {code})")
                }
                None => "browser operator session helper returned no JSON".to_string(),
            },
            CODE_BRIDGE_UNAVAILABLE,
        ),
    }
}

fn sidecar_command<R: Runtime>(
    app: &AppHandle<R>,
    action: SessionAction,
    workspace: &str,
) -> Result<StdCommand, String> {
    let command = app
        .shell()
        .sidecar(SIDECAR_NAME)
        .map_err(|err| format!("loom sidecar unavailable: {err}"))?
        .args([
            "local",
            "browser-session",
            action.as_str(),
            "--workspace",
            workspace,
            "--json",
        ]);
    let mut std_command: StdCommand = command.into();
    std_command
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    Ok(std_command)
}

/// Run one sidecar invocation, writing `stdin_line` (if any) followed by a
/// newline and closing stdin. Blocking; call from a blocking context.
fn run_sidecar(mut command: StdCommand, stdin_line: Option<&str>, timeout: Duration) -> Value {
    let mut child: Child = match command.spawn() {
        Ok(child) => child,
        Err(err) => {
            return failure(
                format!("failed to start loom sidecar: {err}"),
                CODE_BRIDGE_UNAVAILABLE,
            )
        }
    };

    if let Some(mut stdin) = child.stdin.take() {
        if let Some(line) = stdin_line {
            let _ = stdin.write_all(line.as_bytes());
            let _ = stdin.write_all(b"\n");
        }
        // Dropping stdin closes it so the sidecar sees EOF.
    }

    let stdout_reader = child.stdout.take().map(|stdout| {
        thread::spawn(move || {
            let mut buf = Vec::new();
            let _ = stdout.take(MAX_STDOUT_BYTES).read_to_end(&mut buf);
            buf
        })
    });

    let deadline = Instant::now() + timeout;
    let exit_code = loop {
        match child.try_wait() {
            Ok(Some(status)) => break status.code(),
            Ok(None) if Instant::now() >= deadline => {
                let _ = child.kill();
                let _ = child.wait();
                return failure(
                    "browser operator session helper timed out",
                    CODE_BRIDGE_UNAVAILABLE,
                );
            }
            Ok(None) => thread::sleep(Duration::from_millis(20)),
            Err(err) => {
                let _ = child.kill();
                return failure(
                    format!("browser operator session helper failed: {err}"),
                    CODE_BRIDGE_UNAVAILABLE,
                );
            }
        }
    };

    let stdout = stdout_reader
        .and_then(|handle| handle.join().ok())
        .unwrap_or_default();
    parse_sidecar_output(&stdout, exit_code)
}

fn record_result(
    state: &BrowserSessionState,
    action: SessionAction,
    key: &TokenKey,
    sent_token: Option<&str>,
    result: &Value,
) {
    let Ok(mut tokens) = state.tokens.lock() else {
        return;
    };
    let ok = result.get("ok").and_then(Value::as_bool).unwrap_or(false);
    match action {
        SessionAction::Issue | SessionAction::Refresh => {
            if !ok {
                if action == SessionAction::Refresh {
                    // An expired/revoked bearer is useless; forget it.
                    if let Some(sent) = sent_token {
                        if tokens.get(key).map(String::as_str) == Some(sent) {
                            tokens.remove(key);
                        }
                    }
                }
                return;
            }
            if let Some(token) = result.get("token").and_then(Value::as_str) {
                if validate_token(token).is_ok() {
                    // Keep only this webview's current bearer for the
                    // workspace; its replaced bearer is revoked by the caller.
                    tokens.insert(key.clone(), token.to_string());
                }
            }
        }
        SessionAction::Revoke => {
            if let Some(sent) = sent_token {
                if tokens.get(key).map(String::as_str) == Some(sent) {
                    tokens.remove(key);
                }
            }
        }
    }
}

/// Record `result` for `key` and return the bearer that the call replaced, if
/// any, so the caller can revoke it. Only `key`'s own previous bearer is ever
/// returned; another webview's bearer for the same workspace is untouched.
fn apply_result(
    state: &BrowserSessionState,
    action: SessionAction,
    key: &TokenKey,
    previous: Option<String>,
    sent_token: Option<&str>,
    result: &Value,
) -> Option<String> {
    record_result(state, action, key, sent_token, result);
    let previous = previous?;
    let ok = result.get("ok").and_then(Value::as_bool).unwrap_or(false);
    let issued = result.get("token").and_then(Value::as_str);
    (action == SessionAction::Issue && ok && issued != Some(previous.as_str())).then_some(previous)
}

/// Issue, refresh, or revoke the local-desktop operator bearer for one
/// workspace. Always resolves with the sidecar's JSON object (`ok: false` plus
/// `error`/`code` on failure) so the web UI can render a precise state.
#[tauri::command]
pub async fn browser_operator_session<R: Runtime>(
    app: AppHandle<R>,
    webview: Webview<R>,
    state: tauri::State<'_, BrowserSessionState>,
    action: String,
    workspace: String,
    token: Option<String>,
) -> Result<Value, String> {
    let action = match parse_action(&action) {
        Ok(action) => action,
        Err(err) => return Ok(failure(err, CODE_INVALID)),
    };
    if let Err(err) = validate_workspace(&workspace) {
        return Ok(failure(err, CODE_INVALID));
    }
    let caller = match webview.url() {
        Ok(url) => url,
        Err(_) => return Ok(failure("caller URL is unavailable", CODE_CALLER_FORBIDDEN)),
    };
    let trusted = state.runtime_origin.lock().ok().and_then(|o| o.clone());
    if let Err(err) = authorize_caller(&caller, trusted.as_deref(), &workspace) {
        return Ok(failure(err, CODE_CALLER_FORBIDDEN));
    }
    let key = TokenKey::new(webview.label(), &workspace);
    let slot = state.slot_lock(&key);
    let _slot_guard = slot.lock().await;

    let stdin_token = match action {
        SessionAction::Issue => None,
        SessionAction::Refresh | SessionAction::Revoke => {
            let provided = token
                .filter(|t| !t.is_empty())
                .or_else(|| state.current(&key));
            match provided {
                Some(t) if validate_token(&t).is_ok() => Some(t),
                _ => {
                    return Ok(failure(
                        "operator session token is missing or malformed",
                        "browser_operator_session_required",
                    ))
                }
            }
        }
    };

    let previous = if action == SessionAction::Issue {
        state.current(&key)
    } else {
        None
    };

    let command = match sidecar_command(&app, action, &workspace) {
        Ok(command) => command,
        Err(err) => return Ok(failure(err, CODE_BRIDGE_UNAVAILABLE)),
    };
    let line = stdin_token.clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        run_sidecar(command, line.as_deref(), SIDECAR_TIMEOUT)
    })
    .await
    .unwrap_or_else(|_| {
        failure(
            "browser operator session helper panicked",
            CODE_BRIDGE_UNAVAILABLE,
        )
    });

    // A re-issue replaces this webview's previous bearer for the workspace;
    // revoke that one best-effort so it does not linger until its idle TTL.
    let replaced = apply_result(
        &state,
        action,
        &key,
        previous,
        stdin_token.as_deref(),
        &result,
    );
    if let Some(replaced) = replaced {
        if let Ok(command) = sidecar_command(&app, SessionAction::Revoke, &workspace) {
            tauri::async_runtime::spawn_blocking(move || {
                let _ = run_sidecar(command, Some(&replaced), SIDECAR_TIMEOUT);
            });
        }
    }

    Ok(sanitize_for_webview(result))
}

/// Only forward the contract fields to the webview.
fn sanitize_for_webview(result: Value) -> Value {
    const FIELDS: [&str; 9] = [
        "ok",
        "token",
        "session_id",
        "workspace",
        "expires_at",
        "absolute_expires_at",
        "idle_timeout_seconds",
        "error",
        "code",
    ];
    match result {
        Value::Object(obj) => {
            let mut out = Map::new();
            for (key, value) in obj {
                if FIELDS.contains(&key.as_str()) {
                    out.insert(key, value);
                }
            }
            Value::Object(out)
        }
        other => other,
    }
}

/// Revoke every bearer issued to one webview when its window is destroyed.
/// Best-effort and off the event loop; other windows' bearers are untouched.
pub fn revoke_webview_on_close<R: Runtime>(app: &AppHandle<R>, webview: &str) {
    let Some(state) = app.try_state::<BrowserSessionState>() else {
        return;
    };
    for (workspace, token) in state.drain_webview(webview) {
        if let Ok(command) = sidecar_command(app, SessionAction::Revoke, &workspace) {
            tauri::async_runtime::spawn_blocking(move || {
                let _ = run_sidecar(command, Some(&token), SIDECAR_TIMEOUT);
            });
        }
    }
}

/// Revoke every bearer this shell issued. Best-effort, bounded, and run once:
/// the map is drained so repeated exit events do nothing.
pub fn revoke_all_on_exit<R: Runtime>(app: &AppHandle<R>) {
    let drained = match app.try_state::<BrowserSessionState>() {
        Some(state) => state.drain_all(),
        None => return,
    };
    let handles: Vec<_> = drained
        .into_iter()
        .filter_map(|(workspace, token)| {
            let command = sidecar_command(app, SessionAction::Revoke, &workspace).ok()?;
            Some(thread::spawn(move || {
                let _ = run_sidecar(command, Some(&token), EXIT_REVOKE_TIMEOUT);
            }))
        })
        .collect();
    for handle in handles {
        let _ = handle.join();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_known_actions_only() {
        assert_eq!(parse_action("issue"), Ok(SessionAction::Issue));
        assert_eq!(parse_action("refresh"), Ok(SessionAction::Refresh));
        assert_eq!(parse_action("revoke"), Ok(SessionAction::Revoke));
        for bad in ["", "Issue", "issue ", "delete", "revoke;rm"] {
            assert!(parse_action(bad).is_err(), "{bad:?}");
        }
    }

    #[test]
    fn accepts_plain_workspace_keys() {
        for ok in ["LOCAL", "KERNEL-BROWSER", "ws_1.2", &"a".repeat(128)] {
            assert!(validate_workspace(ok).is_ok(), "{ok:?}");
        }
    }

    #[test]
    fn rejects_bad_workspace_keys() {
        let long = "a".repeat(129);
        for bad in [
            "",
            "has space",
            "tab\there",
            "new\nline",
            "nul\0byte",
            "bell\u{7}",
            "nbsp\u{a0}x",
            "--json",
            "-x",
            long.as_str(),
        ] {
            assert!(validate_workspace(bad).is_err(), "{bad:?}");
        }
    }

    #[test]
    fn rejects_multiline_or_empty_tokens() {
        assert!(validate_token("lbo_abc123").is_ok());
        for bad in ["", "a b", "a\nb", "a\rb", &"x".repeat(MAX_TOKEN_LEN + 1)] {
            assert!(validate_token(bad).is_err(), "{bad:?}");
        }
    }

    #[test]
    fn parses_successful_sidecar_json() {
        let out = br#"{"ok":true,"token":"t","session_id":"s","workspace":"W"}"#;
        let v = parse_sidecar_output(out, Some(0));
        assert_eq!(v["ok"], json!(true));
        assert_eq!(v["token"], json!("t"));
    }

    #[test]
    fn failed_exit_never_forwards_a_token() {
        let out = br#"{"ok":true,"token":"t"}"#;
        let v = parse_sidecar_output(out, Some(1));
        assert_eq!(v["ok"], json!(false));
        assert!(v.get("token").is_none());
        assert_eq!(v["code"], json!(CODE_BRIDGE_UNAVAILABLE));
    }

    #[test]
    fn preserves_sidecar_error_code() {
        let out = br#"{"ok":false,"error":"expired","code":"browser_operator_session_expired"}"#;
        let v = parse_sidecar_output(out, Some(1));
        assert_eq!(v["code"], json!("browser_operator_session_expired"));
        assert_eq!(v["error"], json!("expired"));
    }

    #[test]
    fn non_json_output_is_a_bridge_failure() {
        let v = parse_sidecar_output(b"panic: boom", Some(2));
        assert_eq!(v["ok"], json!(false));
        assert_eq!(v["code"], json!(CODE_BRIDGE_UNAVAILABLE));
        assert!(!v["error"].as_str().unwrap().contains("boom"));
    }

    #[test]
    fn sanitize_drops_unknown_fields() {
        let v = sanitize_for_webview(json!({"ok": true, "token": "t", "debug": "x"}));
        assert!(v.get("debug").is_none());
        assert_eq!(v["token"], json!("t"));
    }

    #[cfg(unix)]
    #[test]
    fn token_reaches_child_on_stdin_only() {
        // `sh -c` echoes its argv and the first stdin line back as JSON; the
        // token must appear only in the stdin field.
        let mut cmd = StdCommand::new("/bin/sh");
        cmd.args([
            "-c",
            r#"read line; printf '{"ok":true,"argv":"%s","stdin":"%s"}' "$*" "$line""#,
            "sh",
            "refresh",
            "--workspace",
            "W",
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
        let v = run_sidecar(cmd, Some("secret-token"), Duration::from_secs(5));
        assert_eq!(v["ok"], json!(true));
        assert_eq!(v["stdin"], json!("secret-token"));
        assert!(!v["argv"].as_str().unwrap().contains("secret-token"));
    }

    #[cfg(unix)]
    #[test]
    fn hung_sidecar_times_out() {
        let mut cmd = StdCommand::new("/bin/sh");
        cmd.args(["-c", "sleep 5"])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null());
        let v = run_sidecar(cmd, None, Duration::from_millis(100));
        assert_eq!(v["ok"], json!(false));
        assert_eq!(v["code"], json!(CODE_BRIDGE_UNAVAILABLE));
    }

    fn issued(token: &str) -> Value {
        json!({"ok": true, "token": token})
    }

    /// Simulate the command's issue path for one webview: read its previous
    /// bearer, record the new one, and return what would be revoked.
    fn issue(state: &BrowserSessionState, webview: &str, ws: &str, token: &str) -> Option<String> {
        let key = TokenKey::new(webview, ws);
        let previous = state.current(&key);
        apply_result(
            state,
            SessionAction::Issue,
            &key,
            previous,
            None,
            &issued(token),
        )
    }

    #[test]
    fn slot_locks_are_per_webview_and_workspace() {
        let state = BrowserSessionState::default();
        let a = state.slot_lock(&TokenKey::new("w1", "LOCAL"));
        assert!(Arc::ptr_eq(
            &a,
            &state.slot_lock(&TokenKey::new("w1", "LOCAL"))
        ));
        assert!(!Arc::ptr_eq(
            &a,
            &state.slot_lock(&TokenKey::new("w2", "LOCAL"))
        ));
        assert!(!Arc::ptr_eq(
            &a,
            &state.slot_lock(&TokenKey::new("w1", "OTHER"))
        ));
        // Holding one slot never blocks another webview's slot.
        let _held = a.try_lock().unwrap();
        assert!(state
            .slot_lock(&TokenKey::new("w1", "LOCAL"))
            .try_lock()
            .is_err());
        assert!(state
            .slot_lock(&TokenKey::new("w2", "LOCAL"))
            .try_lock()
            .is_ok());
    }

    #[test]
    fn two_windows_same_workspace_keep_independent_bearers() {
        let state = BrowserSessionState::default();
        let a = TokenKey::new("workspace-1", "LOCAL");
        let b = TokenKey::new("workspace-2", "LOCAL");

        assert_eq!(issue(&state, "workspace-1", "LOCAL", "tA1"), None);
        // B issuing for the same workspace must not revoke A.
        assert_eq!(issue(&state, "workspace-2", "LOCAL", "tB1"), None);
        assert_eq!(state.current(&a).as_deref(), Some("tA1"));
        assert_eq!(state.current(&b).as_deref(), Some("tB1"));

        // Alternating re-issues revoke only the caller's own earlier bearer.
        assert_eq!(
            issue(&state, "workspace-1", "LOCAL", "tA2").as_deref(),
            Some("tA1")
        );
        assert_eq!(state.current(&b).as_deref(), Some("tB1"));
        assert_eq!(
            issue(&state, "workspace-2", "LOCAL", "tB2").as_deref(),
            Some("tB1")
        );
        assert_eq!(state.current(&a).as_deref(), Some("tA2"));

        // A failed refresh by B forgets only B's bearer.
        record_result(
            &state,
            SessionAction::Refresh,
            &b,
            Some("tB2"),
            &json!({"ok": false, "code": "browser_operator_session_expired"}),
        );
        assert_eq!(state.current(&b), None);
        assert_eq!(state.current(&a).as_deref(), Some("tA2"));
    }

    #[test]
    fn revoke_by_one_window_leaves_the_other() {
        let state = BrowserSessionState::default();
        issue(&state, "w1", "LOCAL", "t1");
        issue(&state, "w2", "LOCAL", "t2");
        let w1 = TokenKey::new("w1", "LOCAL");
        // w1 presenting w2's bearer cannot clear w2's slot.
        record_result(
            &state,
            SessionAction::Revoke,
            &w1,
            Some("t2"),
            &json!({"ok": true}),
        );
        assert_eq!(
            state.current(&TokenKey::new("w2", "LOCAL")).as_deref(),
            Some("t2")
        );
        record_result(
            &state,
            SessionAction::Revoke,
            &w1,
            Some("t1"),
            &json!({"ok": true}),
        );
        assert_eq!(state.current(&w1), None);
        assert_eq!(
            state.current(&TokenKey::new("w2", "LOCAL")).as_deref(),
            Some("t2")
        );
    }

    #[test]
    fn closing_a_window_drains_only_its_bearers() {
        let state = BrowserSessionState::default();
        issue(&state, "w1", "LOCAL", "t1");
        issue(&state, "w1", "OTHER", "t1o");
        issue(&state, "w2", "LOCAL", "t2");
        let mut drained = state.drain_webview("w1");
        drained.sort();
        assert_eq!(
            drained,
            vec![
                ("LOCAL".to_string(), "t1".to_string()),
                ("OTHER".to_string(), "t1o".to_string())
            ]
        );
        assert_eq!(
            state.current(&TokenKey::new("w2", "LOCAL")).as_deref(),
            Some("t2")
        );
        assert_eq!(
            state.drain_all(),
            vec![("LOCAL".to_string(), "t2".to_string())]
        );
        assert!(state.drain_all().is_empty());
    }

    #[test]
    fn failed_issue_revokes_nothing() {
        let state = BrowserSessionState::default();
        issue(&state, "w1", "LOCAL", "t1");
        let key = TokenKey::new("w1", "LOCAL");
        let replaced = apply_result(
            &state,
            SessionAction::Issue,
            &key,
            state.current(&key),
            None,
            &json!({"ok": false, "code": "browser_operator_bridge_unavailable"}),
        );
        assert_eq!(replaced, None);
        assert_eq!(state.current(&key).as_deref(), Some("t1"));
    }

    const RUNTIME: &str = "http://127.0.0.1:4567";

    fn url(raw: &str) -> Url {
        Url::parse(raw).unwrap()
    }

    #[test]
    fn caller_on_trusted_runtime_workspace_route_is_allowed() {
        for raw in [
            "http://127.0.0.1:4567/ws/LOCAL",
            "http://127.0.0.1:4567/ws/LOCAL/",
            "http://127.0.0.1:4567/ws/LOCAL/agents/lead?tab=browser#x",
        ] {
            assert_eq!(
                authorize_caller(&url(raw), Some(RUNTIME), "LOCAL"),
                Ok(()),
                "{raw}"
            );
        }
        assert_eq!(
            authorize_caller(
                &url("http://127.0.0.1:4567/ws/A%2DB/x"),
                Some(RUNTIME),
                "A-B"
            ),
            Ok(())
        );
    }

    #[test]
    fn foreign_loopback_origin_is_denied() {
        // The capability admits every loopback port; the command must not.
        for raw in [
            "http://127.0.0.1:9999/ws/LOCAL/agents",
            "http://localhost:4567/ws/LOCAL/agents",
            "http://[::1]:4567/ws/LOCAL/agents",
            "https://127.0.0.1:4567/ws/LOCAL/agents",
            "tauri://localhost/ws/LOCAL",
        ] {
            assert!(
                authorize_caller(&url(raw), Some(RUNTIME), "LOCAL").is_err(),
                "{raw}"
            );
        }
    }

    #[test]
    fn no_trusted_runtime_denies_every_caller() {
        assert!(authorize_caller(&url("http://127.0.0.1:4567/ws/LOCAL"), None, "LOCAL").is_err());
    }

    #[test]
    fn caller_route_must_bind_requested_workspace() {
        for raw in [
            "http://127.0.0.1:4567/",
            "http://127.0.0.1:4567/ws/OTHER/agents",
            "http://127.0.0.1:4567/ws/LOCALX",
            "http://127.0.0.1:4567/api/ws/LOCAL",
            "http://127.0.0.1:4567/ws/%ZZ",
            "http://127.0.0.1:4567/?ws=LOCAL",
        ] {
            assert!(
                authorize_caller(&url(raw), Some(RUNTIME), "LOCAL").is_err(),
                "{raw}"
            );
        }
    }

    fn capabilities() -> Vec<Value> {
        [
            include_str!("../capabilities/default.json"),
            include_str!("../capabilities/workspace.json"),
            include_str!("../capabilities/workspace-ipc.json"),
        ]
        .iter()
        .map(|raw| serde_json::from_str(raw).unwrap())
        .collect()
    }

    fn grants(cap: &Value, permission: &str) -> bool {
        cap["permissions"]
            .as_array()
            .unwrap()
            .iter()
            .any(|p| p.as_str() == Some(permission))
    }

    #[test]
    fn operator_session_command_is_granted_only_to_loopback_remote_origins() {
        let granting: Vec<_> = capabilities()
            .into_iter()
            .filter(|cap| grants(cap, "allow-browser-operator-session"))
            .collect();
        assert_eq!(granting.len(), 1);
        let cap = &granting[0];
        assert_eq!(cap["local"], json!(false));
        let urls = cap["remote"]["urls"].as_array().unwrap();
        assert!(!urls.is_empty());
        for url in urls {
            let url = url.as_str().unwrap();
            assert!(
                url == "http://127.0.0.1:*" || url == "http://localhost:*",
                "non-loopback origin {url}"
            );
        }
    }

    #[test]
    fn every_app_command_stays_granted_somewhere() {
        // Declaring an app manifest turns ACL checks on for every app command;
        // each registered command must remain reachable from its caller.
        let caps = capabilities();
        for permission in [
            "allow-open-workspace-window",
            "allow-take-workspace-recovery-path",
            "allow-needs-relocation",
            "allow-pick-folder",
            "allow-browser-operator-session",
        ] {
            assert!(
                caps.iter().any(|cap| grants(cap, permission)),
                "{permission} is not granted by any capability"
            );
        }
    }
}
