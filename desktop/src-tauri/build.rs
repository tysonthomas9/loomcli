// Every app command must be listed here. Declaring an app manifest turns on
// Tauri's ACL for *all* app commands (tauri::webview::Webview::on_message only
// checks app commands when `has_app_acl_manifest`), so a command missing from
// this list and from a capability becomes uncallable from every window.
const APP_COMMANDS: &[&str] = &[
    "open_workspace_window",
    "pick_folder",
    "needs_relocation",
    "take_workspace_recovery_path",
    "browser_operator_session",
];

fn main() {
    tauri_build::try_build(
        tauri_build::Attributes::new()
            .app_manifest(tauri_build::AppManifest::new().commands(APP_COMMANDS)),
    )
    .expect("failed to run tauri-build");
}
