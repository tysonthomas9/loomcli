use std::{
    collections::HashMap,
    io::{Read, Write},
    net::{SocketAddr, TcpStream},
    path::Path,
    sync::Mutex,
    time::Duration,
};

use tauri::{
    menu::{Menu, MenuItem, MenuItemKind, PredefinedMenuItem, Submenu, WINDOW_SUBMENU_ID},
    AppHandle, LogicalPosition, LogicalSize, Manager, RunEvent, Runtime, Url, WebviewUrl,
    WebviewWindow, WebviewWindowBuilder,
};

const MENU_NEW_WORKSPACE_WINDOW: &str = "new-workspace-window";
const MENU_NEW_WORKSPACE_WINDOW_ALT: &str = "new-workspace-window-window-menu";
const PRIMARY_WORKSPACE_WINDOW_LABEL: &str = "main";
const ADDITIONAL_WORKSPACE_WINDOW_PREFIX: &str = "workspace-";
const WORKSPACE_WINDOW_WIDTH: f64 = 1280.0;
const WORKSPACE_WINDOW_HEIGHT: f64 = 800.0;
const WORKSPACE_MIN_WIDTH: f64 = 720.0;
const WORKSPACE_MIN_HEIGHT: f64 = 520.0;
const STALE_RUNTIME_HEALTH_TIMEOUT: Duration = Duration::from_millis(300);

#[derive(Default)]
struct WorkspaceRecoveryState {
    pending: Mutex<HashMap<String, String>>,
}

/// True when macOS is running this bundle from a read-only/randomized location
/// (App Translocation for a quarantined app, or straight off a mounted disk
/// image). In that state the bundled `loom` sidecar cannot be launched, so the
/// launcher UI must tell the user to move the app into /Applications instead of
/// silently failing to start the runtime.
#[tauri::command]
fn needs_relocation() -> bool {
    std::env::current_exe()
        .map(|path| path_needs_relocation(&path))
        .unwrap_or(false)
}

fn path_needs_relocation(path: &Path) -> bool {
    let path = path.to_string_lossy();
    path.contains("/AppTranslocation/") || path.starts_with("/Volumes/")
}

/// JS shim read by the launcher frontend (src/main.ts) before it boots.
fn relocation_init_script() -> String {
    format!("window.__LOOM_NEEDS_RELOCATION__ = {};", needs_relocation())
}

fn additional_workspace_launcher_init_script() -> String {
    format!(
        "{} window.__LOOM_OPEN_ADDITIONAL_WORKSPACE_WINDOW__ = true;",
        relocation_init_script()
    )
}

pub fn run() {
    tauri::Builder::default()
        .manage(WorkspaceRecoveryState::default())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            open_workspace_window,
            pick_folder,
            needs_relocation,
            take_workspace_recovery
        ])
        .menu(build_menu)
        .on_menu_event(|app, event| match event.id().as_ref() {
            MENU_NEW_WORKSPACE_WINDOW | MENU_NEW_WORKSPACE_WINDOW_ALT => {
                let _ = open_additional_workspace_window(app);
            }
            _ => {}
        })
        .setup(|app| {
            #[cfg(target_os = "macos")]
            {
                app.set_activation_policy(tauri::ActivationPolicy::Regular);
                let _ = app.app_handle().show();
            }
            show_launcher_window(app.handle());
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building Loom desktop app")
        .run(|app, event| match event {
            RunEvent::Ready | RunEvent::Resumed => show_primary_window(app),
            RunEvent::Reopen { .. } => show_primary_window(app),
            RunEvent::Opened { .. } => show_primary_window(app),
            _ => {}
        });
}

#[tauri::command]
fn open_workspace_window<R: Runtime>(
    app: AppHandle<R>,
    caller: WebviewWindow<R>,
    runtime_url: String,
    force_new: bool,
) -> Result<(), String> {
    open_workspace_window_native(&app, &caller, &runtime_url, force_new)
        .map_err(|err| err.to_string())
}

#[tauri::command]
fn take_workspace_recovery<R: Runtime>(
    app: AppHandle<R>,
    caller: WebviewWindow<R>,
    state: tauri::State<'_, WorkspaceRecoveryState>,
) -> Result<Option<String>, String> {
    let caller_url = caller.url().map_err(|err| err.to_string())?;
    let launcher = launcher_url(&app).map_err(|err| err.to_string())?;
    if caller_url != launcher {
        return Err("workspace recovery must originate from bundled launcher content".to_string());
    }
    Ok(state
        .pending
        .lock()
        .ok()
        .and_then(|mut pending| pending.remove(caller.label())))
}

#[tauri::command]
fn pick_folder<R: Runtime>(app: AppHandle<R>) -> Result<Option<String>, String> {
    let (tx, rx) = std::sync::mpsc::channel();
    app.run_on_main_thread(move || {
        let _ = tx.send(pick_folder_native());
    })
    .map_err(|err| err.to_string())?;

    rx.recv()
        .map_err(|err| format!("folder picker did not return: {err}"))?
}

#[cfg(target_os = "macos")]
fn pick_folder_native() -> Result<Option<String>, String> {
    use objc2::rc::autoreleasepool;
    use objc2_foundation::NSString;

    autoreleasepool(|_| {
        // SAFETY: Tauri schedules this function through run_on_main_thread
        // before touching AppKit.
        let mtm = unsafe { objc2::MainThreadMarker::new_unchecked() };
        let panel = objc2_app_kit::NSOpenPanel::openPanel(mtm);
        panel.setCanChooseFiles(false);
        panel.setCanChooseDirectories(true);
        panel.setAllowsMultipleSelection(false);
        panel.setCanCreateDirectories(true);

        let title = NSString::from_str("Choose Folder");
        let prompt = NSString::from_str("Choose");
        panel.setTitle(Some(&title));
        panel.setPrompt(Some(&prompt));

        if panel.runModal() != objc2_app_kit::NSModalResponseOK {
            return Ok(None);
        }

        let Some(url) = panel.URLs().firstObject() else {
            return Ok(None);
        };
        let Some(path) = url.path() else {
            return Ok(None);
        };
        Ok(Some(path.to_string()))
    })
}

#[cfg(not(target_os = "macos"))]
fn pick_folder_native() -> Result<Option<String>, String> {
    Err("folder picker is only implemented for the macOS desktop app".to_string())
}

fn build_menu<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<Menu<R>> {
    let menu = Menu::default(app)?;

    let new_window = MenuItem::with_id(
        app,
        MENU_NEW_WORKSPACE_WINDOW,
        "New Workspace Window",
        true,
        Some("CmdOrCtrl+N"),
    )?;
    let file_separator = PredefinedMenuItem::separator(app)?;
    if let Some(file_menu) = find_submenu_by_text(&menu, "File")? {
        file_menu.prepend_items(&[&new_window, &file_separator])?;
    } else {
        let close = PredefinedMenuItem::close_window(app, None)?;
        let file_menu =
            Submenu::with_items(app, "File", true, &[&new_window, &file_separator, &close])?;
        menu.prepend(&file_menu)?;
    }

    if let Some(MenuItemKind::Submenu(window_menu)) = menu.get(WINDOW_SUBMENU_ID) {
        let window_new_window = MenuItem::with_id(
            app,
            MENU_NEW_WORKSPACE_WINDOW_ALT,
            "New Workspace Window",
            true,
            None::<&str>,
        )?;
        let window_separator = PredefinedMenuItem::separator(app)?;
        window_menu.prepend_items(&[&window_new_window, &window_separator])?;
    }

    Ok(menu)
}

fn find_submenu_by_text<R: Runtime>(
    menu: &Menu<R>,
    text: &str,
) -> tauri::Result<Option<Submenu<R>>> {
    for item in menu.items()? {
        if let MenuItemKind::Submenu(submenu) = item {
            if submenu.text()? == text {
                return Ok(Some(submenu));
            }
        }
    }
    Ok(None)
}

fn show_primary_window<R: Runtime>(app: &AppHandle<R>) {
    let mut primary_recovered = false;
    for window in app.webview_windows().values() {
        if window.label() != PRIMARY_WORKSPACE_WINDOW_LABEL
            && !window
                .label()
                .starts_with(ADDITIONAL_WORKSPACE_WINDOW_PREFIX)
        {
            continue;
        }
        if matches!(recover_stale_workspace_window(app, window), Ok(true))
            && window.label() == PRIMARY_WORKSPACE_WINDOW_LABEL
        {
            primary_recovered = true;
        }
    }
    if primary_recovered {
        return;
    }
    if let Some(window) = app.get_webview_window(PRIMARY_WORKSPACE_WINDOW_LABEL) {
        reveal_window(app, &window);
        return;
    }

    show_launcher_window(app);
}

fn show_launcher_window<R: Runtime>(app: &AppHandle<R>) {
    if let Some(window) = app.get_webview_window("main") {
        reveal_window(app, &window);
        return;
    }

    match WebviewWindowBuilder::new(app, "main", WebviewUrl::default())
        .title("Loom")
        .initialization_script(&relocation_init_script())
        .inner_size(520.0, 300.0)
        .min_inner_size(420.0, 260.0)
        .content_protected(false)
        .focused(true)
        .build()
    {
        Ok(window) => {
            reveal_window(app, &window);
        }
        Err(_) => {}
    }
}

fn show_app<R: Runtime>(app: &AppHandle<R>) {
    #[cfg(target_os = "macos")]
    {
        let _ = app.show();
        let _ = app.run_on_main_thread(activate_app_ignoring_other_apps);
    }
}

fn reveal_window<R: Runtime>(app: &AppHandle<R>, window: &WebviewWindow<R>) {
    perform_window_reveal(app, window);

    schedule_window_reveal(app, window, 250);
    schedule_window_reveal(app, window, 1_000);
    schedule_window_reveal(app, window, 2_200);
}

fn recover_stale_workspace_window<R: Runtime>(
    app: &AppHandle<R>,
    window: &WebviewWindow<R>,
) -> tauri::Result<bool> {
    let url = window.url()?;
    let launcher = launcher_url(app)?;
    if url == launcher {
        return Ok(false);
    }

    if !is_loopback_runtime_url(&url) {
        return Ok(false);
    }

    if runtime_health_probe(&url, STALE_RUNTIME_HEALTH_TIMEOUT) {
        return Ok(false);
    }

    let state = app.state::<WorkspaceRecoveryState>();
    let route = workspace_recovery_route(&url);
    state
        .pending
        .lock()
        .map_err(|_| invalid_workspace_url("workspace recovery state is unavailable"))?
        .insert(window.label().to_string(), route);

    window.navigate(launcher)?;
    reveal_window(app, window);
    Ok(true)
}

fn launcher_url<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<Url> {
    if tauri::is_dev() {
        if let Some(url) = app.config().build.dev_url.as_ref() {
            return Ok(url.clone());
        }
    }
    Url::parse("tauri://localhost").map_err(tauri::Error::InvalidUrl)
}

fn is_loopback_runtime_url(url: &Url) -> bool {
    loopback_socket_addr(url).is_some()
}

fn workspace_recovery_route(url: &Url) -> String {
    // `url.path()` is never empty for an `http://host:port` URL (always "/...").
    let mut route = url.path().to_string();
    if let Some(query) = url.query() {
        route.push('?');
        route.push_str(query);
    }
    if let Some(fragment) = url.fragment().filter(|value| !value.trim().is_empty()) {
        route.push('#');
        route.push_str(fragment);
    }
    route
}

fn runtime_health_probe(url: &Url, timeout: Duration) -> bool {
    let Some(addr) = loopback_socket_addr(url) else {
        return false;
    };
    let Ok(mut stream) = TcpStream::connect_timeout(&addr, timeout) else {
        return false;
    };
    let _ = stream.set_read_timeout(Some(timeout));
    let _ = stream.set_write_timeout(Some(timeout));

    let request = format!(
        "GET /api/health HTTP/1.1\r\nHost: {}\r\nConnection: close\r\n\r\n",
        url.authority()
    );
    if stream.write_all(request.as_bytes()).is_err() {
        return false;
    }

    let mut buf = [0_u8; 128];
    let Ok(n) = stream.read(&mut buf) else {
        return false;
    };
    if n == 0 {
        return false;
    }
    let response = String::from_utf8_lossy(&buf[..n]);
    response
        .lines()
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        == Some("200")
}

/// Single source of truth for "is this a local runtime URL?": an `http` URL with
/// an explicit port whose host resolves to a loopback address. Returns the
/// resolved address so callers don't re-parse the host.
fn loopback_socket_addr(url: &Url) -> Option<SocketAddr> {
    if url.scheme() != "http" || url.port().is_none() {
        return None;
    }
    url.socket_addrs(|| None)
        .ok()?
        .into_iter()
        .find(|addr| addr.ip().is_loopback())
}

fn perform_window_reveal<R: Runtime>(app: &AppHandle<R>, window: &WebviewWindow<R>) {
    show_app(app);
    let _ = window.set_content_protected(false);
    let _ = window.set_position(LogicalPosition::new(80.0, 80.0));
    let _ = window.unminimize();
    let _ = window.show();
    let _ = window.set_focus();
}

fn schedule_window_reveal<R: Runtime>(
    app: &AppHandle<R>,
    window: &WebviewWindow<R>,
    delay_ms: u64,
) {
    let app = app.clone();
    let window = window.clone();
    std::thread::spawn(move || {
        std::thread::sleep(std::time::Duration::from_millis(delay_ms));
        perform_window_reveal(&app, &window);
    });
}

#[cfg(target_os = "macos")]
fn activate_app_ignoring_other_apps() {
    // SAFETY: show_app schedules this function through Tauri's main-thread
    // dispatcher before touching AppKit.
    let mtm = unsafe { objc2::MainThreadMarker::new_unchecked() };
    let ns_app = objc2_app_kit::NSApplication::sharedApplication(mtm);
    let _ = ns_app.setActivationPolicy(objc2_app_kit::NSApplicationActivationPolicy::Regular);
    ns_app.unhide(None);
    #[allow(deprecated)]
    {
        ns_app.activateIgnoringOtherApps(true);
        let running_app = objc2_app_kit::NSRunningApplication::currentApplication();
        let _ = running_app.unhide();
        let _ = running_app.activateWithOptions(
            objc2_app_kit::NSApplicationActivationOptions::ActivateAllWindows
                | objc2_app_kit::NSApplicationActivationOptions::ActivateIgnoringOtherApps,
        );
    }
    ns_app.arrangeInFront(None);
}

fn open_workspace_window_native<R: Runtime>(
    app: &AppHandle<R>,
    caller: &WebviewWindow<R>,
    runtime_url: &str,
    force_new: bool,
) -> tauri::Result<()> {
    let url = workspace_entry_url(runtime_url)?;
    if caller.url()? != launcher_url(app)? {
        return Err(invalid_workspace_url(
            "workspace navigation must originate from bundled launcher content",
        ));
    }
    if (!force_new && caller.label() != PRIMARY_WORKSPACE_WINDOW_LABEL)
        || (force_new
            && !caller
                .label()
                .starts_with(ADDITIONAL_WORKSPACE_WINDOW_PREFIX))
    {
        return Err(invalid_workspace_url(
            "workspace navigation caller does not match the target window",
        ));
    }
    // A user-created additional window starts as bundled launcher content.
    // Reuse that exact invoking launcher only after verifying it has not
    // already navigated to runtime content.
    if force_new {
        if !is_additional_workspace_launcher(app, caller)? {
            return Err(tauri::Error::InvalidWebviewUrl(
                "additional workspace windows must originate from bundled launcher content",
            ));
        }
        configure_workspace_window(caller)?;
        caller.navigate(url)?;
        reveal_window(app, caller);
        return Ok(());
    }

    if let Some(window) = app.get_webview_window(PRIMARY_WORKSPACE_WINDOW_LABEL) {
        configure_workspace_window(&window)?;
        window.navigate(url)?;
        reveal_window(app, &window);
        return Ok(());
    }

    let window = WebviewWindowBuilder::new(
        app,
        PRIMARY_WORKSPACE_WINDOW_LABEL,
        WebviewUrl::External(url),
    )
    .title("Loom")
    .inner_size(WORKSPACE_WINDOW_WIDTH, WORKSPACE_WINDOW_HEIGHT)
    .min_inner_size(WORKSPACE_MIN_WIDTH, WORKSPACE_MIN_HEIGHT)
    .content_protected(false)
    .focused(true)
    .build()?;
    reveal_window(app, &window);
    Ok(())
}

fn invalid_workspace_url(message: &'static str) -> tauri::Error {
    tauri::Error::InvalidWebviewUrl(message)
}

fn open_additional_workspace_window<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<()> {
    let label = format!(
        "{}{}-{}",
        ADDITIONAL_WORKSPACE_WINDOW_PREFIX,
        current_time_millis(),
        app.webview_windows().len()
    );
    let window = WebviewWindowBuilder::new(app, label, WebviewUrl::default())
        .title("Loom")
        .initialization_script(&additional_workspace_launcher_init_script())
        .inner_size(WORKSPACE_WINDOW_WIDTH, WORKSPACE_WINDOW_HEIGHT)
        .min_inner_size(WORKSPACE_MIN_WIDTH, WORKSPACE_MIN_HEIGHT)
        .content_protected(false)
        .focused(true)
        .build()?;
    reveal_window(app, &window);
    Ok(())
}

fn is_additional_workspace_launcher<R: Runtime>(
    app: &AppHandle<R>,
    caller: &WebviewWindow<R>,
) -> tauri::Result<bool> {
    Ok(is_additional_workspace_launcher_url(
        caller.label(),
        &caller.url()?,
        &launcher_url(app)?,
    ))
}

fn is_additional_workspace_launcher_url(label: &str, current: &Url, launcher: &Url) -> bool {
    label.starts_with(ADDITIONAL_WORKSPACE_WINDOW_PREFIX) && current == launcher
}

fn configure_workspace_window<R: Runtime>(window: &WebviewWindow<R>) -> tauri::Result<()> {
    window.set_min_size(Some(LogicalSize::new(
        WORKSPACE_MIN_WIDTH,
        WORKSPACE_MIN_HEIGHT,
    )))?;
    window.set_size(LogicalSize::new(
        WORKSPACE_WINDOW_WIDTH,
        WORKSPACE_WINDOW_HEIGHT,
    ))
}

fn workspace_entry_url(runtime_url: &str) -> tauri::Result<Url> {
    let url = Url::parse(runtime_url.trim()).map_err(tauri::Error::InvalidUrl)?;
    if !is_loopback_runtime_url(&url) {
        // Reuse the URL parser's typed error path so callers receive a normal
        // Tauri InvalidUrl without introducing a stringly native-command error.
        let parse_error = Url::parse("http://[").expect_err("invalid URL fixture");
        return Err(tauri::Error::InvalidUrl(parse_error));
    }
    Ok(url)
}

fn current_time_millis() -> u128 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis())
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::{
        additional_workspace_launcher_init_script, is_additional_workspace_launcher_url,
        is_loopback_runtime_url, path_needs_relocation, runtime_health_probe, workspace_entry_url,
        workspace_recovery_route,
    };
    use std::{
        io::{Read, Write},
        net::TcpListener,
        path::Path,
        sync::mpsc,
        thread,
        time::Duration,
    };
    use tauri::Url;

    #[test]
    fn detects_app_running_from_mounted_disk_image() {
        assert!(path_needs_relocation(Path::new(
            "/Volumes/Loom Agents/Loom Agents.app/Contents/MacOS/loom-desktop"
        )));
    }

    #[test]
    fn detects_app_translocation_path() {
        assert!(path_needs_relocation(Path::new(
            "/private/var/folders/xx/AppTranslocation/123/d/Loom Agents.app/Contents/MacOS/loom-desktop"
        )));
    }

    #[test]
    fn allows_applications_path() {
        assert!(!path_needs_relocation(Path::new(
            "/Applications/Loom Agents.app/Contents/MacOS/loom-desktop"
        )));
    }

    #[test]
    fn additional_workspace_launcher_stays_on_bundled_content() {
        let script = additional_workspace_launcher_init_script();
        assert!(script.contains("__LOOM_OPEN_ADDITIONAL_WORKSPACE_WINDOW__ = true"));
        assert!(!script.contains("http://"));
    }

    #[test]
    fn only_bundled_additional_launcher_can_reuse_its_window() {
        let launcher = Url::parse("tauri://localhost").unwrap();
        let runtime = Url::parse("http://127.0.0.1:4567/ws/DESKTOP").unwrap();

        assert!(is_additional_workspace_launcher_url(
            "workspace-1-1",
            &launcher,
            &launcher,
        ));
        assert!(!is_additional_workspace_launcher_url(
            "workspace-1-1",
            &runtime,
            &launcher,
        ));
        assert!(!is_additional_workspace_launcher_url(
            "main", &launcher, &launcher,
        ));
    }

    #[test]
    fn detects_loopback_runtime_urls() {
        for raw in [
            "http://127.0.0.1:1234/ws/LOCAL/kanban",
            "http://localhost:1234/ws/LOCAL/kanban",
            "http://[::1]:1234/ws/LOCAL/kanban",
        ] {
            let url = Url::parse(raw).unwrap();
            assert!(is_loopback_runtime_url(&url), "{raw}");
        }
    }

    #[test]
    fn rejects_non_runtime_urls() {
        for raw in [
            "https://127.0.0.1:1234/ws/LOCAL/kanban",
            "http://127.0.0.1/ws/LOCAL/kanban",
            "http://192.168.1.10:1234/ws/LOCAL/kanban",
            "tauri://localhost",
        ] {
            let url = Url::parse(raw).unwrap();
            assert!(!is_loopback_runtime_url(&url), "{raw}");
        }
    }

    #[test]
    fn preserves_workspace_route_for_recovery() {
        let url = Url::parse("http://127.0.0.1:4567/ws/DESKTOP/list?search=abc#section").unwrap();
        assert_eq!(
            workspace_recovery_route(&url),
            "/ws/DESKTOP/list?search=abc#section"
        );
    }

    #[test]
    fn workspace_entry_url_preserves_full_route() {
        let url = workspace_entry_url("http://127.0.0.1:4567/ws/DESKTOP/list?search=abc#section")
            .unwrap();
        assert_eq!(
            url.as_str(),
            "http://127.0.0.1:4567/ws/DESKTOP/list?search=abc#section"
        );
    }

    #[test]
    fn workspace_entry_url_rejects_non_loopback_and_missing_port() {
        for raw in [
            "https://127.0.0.1:4567/ws/DESKTOP",
            "http://127.0.0.1/ws/DESKTOP",
            "http://192.0.2.10:4567/ws/DESKTOP",
            "https://example.com/ws/DESKTOP",
        ] {
            assert!(workspace_entry_url(raw).is_err(), "{raw}");
        }
    }

    #[test]
    fn runtime_health_probe_accepts_http_200() {
        let (url, request_rx, handle) = one_shot_health_server("200 OK");

        assert!(runtime_health_probe(&url, Duration::from_secs(1)));
        assert!(request_rx
            .recv_timeout(Duration::from_secs(1))
            .unwrap()
            .starts_with("GET /api/health "));
        handle.join().unwrap();
    }

    #[test]
    fn runtime_health_probe_rejects_non_200() {
        let (url, _request_rx, handle) = one_shot_health_server("503 Service Unavailable");

        assert!(!runtime_health_probe(&url, Duration::from_secs(1)));
        handle.join().unwrap();
    }

    #[test]
    fn runtime_health_probe_rejects_refused_port() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        drop(listener);
        let url = Url::parse(&format!("http://127.0.0.1:{port}/ws/LOCAL/kanban")).unwrap();

        assert!(!runtime_health_probe(&url, Duration::from_millis(100)));
    }

    fn one_shot_health_server(
        status: &'static str,
    ) -> (Url, mpsc::Receiver<String>, thread::JoinHandle<()>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let url = Url::parse(&format!("http://127.0.0.1:{port}/ws/LOCAL/kanban")).unwrap();
        let (tx, rx) = mpsc::channel();
        let handle = thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut buf = [0_u8; 512];
            let n = stream.read(&mut buf).unwrap_or(0);
            let request = String::from_utf8_lossy(&buf[..n]).to_string();
            let _ = tx.send(request.lines().next().unwrap_or("").to_string());
            let body = if status.starts_with("200") {
                r#"{"status":"ok"}"#
            } else {
                ""
            };
            let response = format!(
                "HTTP/1.1 {status}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            );
            stream.write_all(response.as_bytes()).unwrap();
        });
        (url, rx, handle)
    }
}
