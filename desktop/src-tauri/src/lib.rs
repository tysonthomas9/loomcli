use tauri::{
    menu::{Menu, MenuItem, MenuItemKind, PredefinedMenuItem, Submenu, WINDOW_SUBMENU_ID},
    AppHandle, Emitter, Manager, RunEvent, Runtime, Url, WebviewUrl, WebviewWindow,
    WebviewWindowBuilder,
};

const MENU_NEW_WORKSPACE_WINDOW: &str = "new-workspace-window";
const MENU_NEW_WORKSPACE_WINDOW_ALT: &str = "new-workspace-window-window-menu";
const EVENT_NEW_WORKSPACE_WINDOW: &str = "loom:new-workspace-window";
const PRIMARY_WORKSPACE_WINDOW_LABEL: &str = "main";

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            open_workspace_window,
            pick_folder
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
    runtime_url: String,
    force_new: bool,
) -> Result<(), String> {
    open_workspace_window_native(&app, &runtime_url, force_new).map_err(|err| err.to_string())
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

    if let Ok(window) = WebviewWindowBuilder::new(app, "main", WebviewUrl::default())
        .title("Loom")
        .inner_size(520.0, 300.0)
        .min_inner_size(420.0, 260.0)
        .content_protected(false)
        .focused(true)
        .build()
    {
        reveal_window(app, &window);
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

fn perform_window_reveal<R: Runtime>(app: &AppHandle<R>, window: &WebviewWindow<R>) {
    show_app(app);
    let _ = window.set_content_protected(false);
    let _ = window.unminimize();
    let _ = window.show();
    let _ = window.set_focus();

    #[cfg(target_os = "macos")]
    raise_macos_window(window);
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

#[cfg(target_os = "macos")]
fn raise_macos_window<R: Runtime>(window: &WebviewWindow<R>) {
    let dispatch_window = window.clone();
    let target_window = window.clone();
    let _ = dispatch_window.run_on_main_thread(move || {
        activate_app_ignoring_other_apps();
        if let Ok(ns_window) = target_window.ns_window() {
            if !ns_window.is_null() {
                // SAFETY: ns_window is the AppKit NSWindow pointer returned by Tauri
                // for this webview window, and this block runs on the main thread.
                let ns_window = unsafe { &*(ns_window.cast::<objc2_app_kit::NSWindow>()) };
                ns_window.makeKeyAndOrderFront(None);
                ns_window.orderFrontRegardless();
            }
        }
        activate_app_ignoring_other_apps();
    });
}

fn open_workspace_window_native<R: Runtime>(
    app: &AppHandle<R>,
    runtime_url: &str,
    force_new: bool,
) -> tauri::Result<()> {
    let url = workspace_entry_url(runtime_url)?;

    if !force_new {
        if let Some(window) = app.get_webview_window(PRIMARY_WORKSPACE_WINDOW_LABEL) {
            window.navigate(url)?;
            reveal_window(app, &window);
            return Ok(());
        }
    }

    let label = if force_new {
        format!(
            "workspace-{}-{}",
            current_time_millis(),
            app.webview_windows().len()
        )
    } else {
        PRIMARY_WORKSPACE_WINDOW_LABEL.to_string()
    };

    let window = WebviewWindowBuilder::new(app, label, WebviewUrl::External(url))
        .title("Loom")
        .inner_size(1280.0, 800.0)
        .min_inner_size(720.0, 520.0)
        .content_protected(false)
        .focused(true)
        .build()?;
    reveal_window(app, &window);
    Ok(())
}

fn open_additional_workspace_window<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<()> {
    let Some(url) = current_workspace_url(app)? else {
        show_primary_window(app);
        app.emit(EVENT_NEW_WORKSPACE_WINDOW, ())?;
        return Ok(());
    };
    let label = format!(
        "workspace-{}-{}",
        current_time_millis(),
        app.webview_windows().len()
    );
    let window = WebviewWindowBuilder::new(app, label, WebviewUrl::External(url))
        .title("Loom")
        .inner_size(1280.0, 800.0)
        .min_inner_size(720.0, 520.0)
        .content_protected(false)
        .focused(true)
        .build()?;
    reveal_window(app, &window);
    Ok(())
}

fn workspace_entry_url(runtime_url: &str) -> tauri::Result<Url> {
    let base = runtime_url.trim_end_matches('/');
    Url::parse(&format!("{base}/")).map_err(tauri::Error::InvalidUrl)
}

fn current_workspace_url<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<Option<Url>> {
    if let Some(window) = app.get_webview_window(PRIMARY_WORKSPACE_WINDOW_LABEL) {
        let url = window.url()?;
        if url.scheme() == "http" || url.scheme() == "https" {
            return Ok(Some(url));
        }
    }
    for (label, window) in app.webview_windows() {
        if label.starts_with("workspace-") {
            return window.url().map(Some);
        }
    }
    Ok(None)
}

fn current_time_millis() -> u128 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis())
        .unwrap_or_default()
}
