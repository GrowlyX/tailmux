//! tailmux desktop: a tray icon, a small panel next to it and a main
//! window, all talking to the daemon's loopback API from Rust.

pub mod api;
mod tray;

use serde::Serialize;
use serde_json::Value;
use std::path::PathBuf;
use std::sync::Mutex;
use tauri::{AppHandle, Emitter, LogicalSize, Manager, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_autostart::ManagerExt;
use tauri_plugin_clipboard_manager::ClipboardExt;
use tauri_plugin_opener::OpenerExt;

pub struct State {
    pub base: String,
    /// The page the main window opens on (from --page), read once.
    pub start_page: Mutex<Option<String>>,
}

const PANEL_WIDTH: f64 = 372.0;

#[derive(Serialize)]
struct Info {
    base: String,
    os: &'static str,
    hostname: String,
    sidecar: Option<String>,
    start_page: Option<String>,
}

#[tauri::command]
fn info(app: AppHandle, state: tauri::State<'_, State>) -> Info {
    let _ = app;
    Info {
        base: state.base.clone(),
        os: std::env::consts::OS,
        hostname: hostname(),
        sidecar: sidecar_path().map(|p| p.display().to_string()),
        start_page: state.start_page.lock().unwrap().take(),
    }
}

fn hostname() -> String {
    for var in ["COMPUTERNAME", "HOSTNAME", "HOST"] {
        if let Ok(v) = std::env::var(var) {
            if !v.is_empty() {
                return v;
            }
        }
    }
    std::fs::read_to_string("/etc/hostname")
        .map(|s| s.trim().to_string())
        .unwrap_or_else(|_| "pc".to_string())
}

#[tauri::command]
async fn api(
    state: tauri::State<'_, State>,
    method: String,
    path: String,
    body: Option<Value>,
) -> Result<Value, String> {
    let base = state.base.clone();
    tauri::async_runtime::spawn_blocking(move || api::call(&base, &method, &path, body))
        .await
        .map_err(|e| e.to_string())?
}

#[tauri::command]
fn open_url(app: AppHandle, url: String) -> Result<(), String> {
    app.opener().open_url(url, None::<&str>).map_err(|e| e.to_string())
}

#[tauri::command]
fn copy_text(app: AppHandle, text: String) -> Result<(), String> {
    app.clipboard().write_text(text).map_err(|e| e.to_string())
}

#[tauri::command]
fn autostart_enabled(app: AppHandle) -> bool {
    app.autolaunch().is_enabled().unwrap_or(false)
}

#[tauri::command]
fn set_autostart(app: AppHandle, on: bool) -> Result<bool, String> {
    let launcher = app.autolaunch();
    let r = if on { launcher.enable() } else { launcher.disable() };
    r.map_err(|e| e.to_string())?;
    Ok(launcher.is_enabled().unwrap_or(false))
}

#[tauri::command]
fn show_main(app: AppHandle, page: Option<String>) {
    if let Some(p) = page {
        let _ = app.emit_to("main", "page", p);
    }
    if let Some(panel) = app.get_webview_window("panel") {
        let _ = panel.hide();
    }
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
}

#[tauri::command]
fn hide_panel(app: AppHandle) {
    if let Some(panel) = app.get_webview_window("panel") {
        let _ = panel.hide();
    }
}

/// The panel is as tall as its content; the page measures itself and
/// asks for the height after every render.
#[tauri::command]
fn resize_panel(app: AppHandle, height: f64) {
    if let Some(panel) = app.get_webview_window("panel") {
        let h = height.clamp(120.0, 900.0);
        let _ = panel.set_size(LogicalSize::new(PANEL_WIDTH, h));
    }
}

#[tauri::command]
fn quit(app: AppHandle) {
    app.exit(0);
}

/// Where the bundled `tailmux` binary is: next to this executable, as
/// Tauri puts external binaries. Falls back to one on PATH.
fn sidecar_path() -> Option<PathBuf> {
    let name = if cfg!(windows) { "tailmux.exe" } else { "tailmux" };
    let exe = std::env::current_exe().ok()?;
    let dir = exe.parent()?;
    let candidates = [dir.join(name), dir.join("binaries").join(name)];
    for c in candidates {
        if c.is_file() && std::fs::metadata(&c).map(|m| m.len() > 4096).unwrap_or(false) {
            return Some(c);
        }
    }
    let path = std::env::var_os("PATH")?;
    std::env::split_paths(&path)
        .map(|p| p.join(name))
        .find(|p| p.is_file())
}

/// Runs `tailmux service install` with elevation, using the platform's
/// own prompt so the app never handles a password.
#[tauri::command]
async fn install_service() -> Result<String, String> {
    let bin = sidecar_path().ok_or("The tailmux binary isn't bundled with this build and isn't on PATH.")?;
    tauri::async_runtime::spawn_blocking(move || run_elevated(&bin, &["service", "install"]))
        .await
        .map_err(|e| e.to_string())?
}

fn run_elevated(bin: &std::path::Path, args: &[&str]) -> Result<String, String> {
    use std::process::Command;
    let bin_s = bin.display().to_string();
    #[cfg(target_os = "windows")]
    let output = {
        // Start-Process -Verb RunAs shows the UAC prompt; -Wait so we can
        // report when it finished.
        let list = args.iter().map(|a| format!("'{a}'")).collect::<Vec<_>>().join(",");
        let script = format!(
            "$p = Start-Process -FilePath '{}' -ArgumentList {} -Verb RunAs -Wait -PassThru; exit $p.ExitCode",
            bin_s.replace('\'', "''"),
            list
        );
        Command::new("powershell")
            .args(["-NoProfile", "-NonInteractive", "-Command", &script])
            .output()
    };
    #[cfg(target_os = "linux")]
    let output = Command::new("pkexec").arg(&bin_s).args(args).output();
    #[cfg(target_os = "macos")]
    let output = {
        // Only for local testing; macOS ships the SwiftUI app instead.
        let cmd = format!("{} {}", shell_quote(&bin_s), args.iter().map(|a| shell_quote(a)).collect::<Vec<_>>().join(" "));
        Command::new("osascript")
            .args(["-e", &format!("do shell script \"{}\" with administrator privileges", cmd.replace('"', "\\\""))])
            .output()
    };
    let output = output.map_err(|e| format!("could not start the installer: {e}"))?;
    let out = String::from_utf8_lossy(&output.stdout).trim().to_string();
    let err = String::from_utf8_lossy(&output.stderr).trim().to_string();
    if output.status.success() {
        Ok(if out.is_empty() { "Service installed.".to_string() } else { out })
    } else {
        Err(if err.is_empty() { format!("installer exited with {}", output.status) } else { err })
    }
}

#[cfg(target_os = "macos")]
fn shell_quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\\''"))
}

/// Toggles the panel from the tray: shows it next to the icon or hides it.
pub fn toggle_panel(app: &AppHandle) {
    let Some(panel) = app.get_webview_window("panel") else { return };
    if panel.is_visible().unwrap_or(false) {
        let _ = panel.hide();
        return;
    }
    position_panel(&panel);
    let _ = panel.show();
    let _ = panel.set_focus();
}

fn position_panel(panel: &tauri::WebviewWindow) {
    use tauri_plugin_positioner::{Position, WindowExt};
    // The positioner knows where the tray icon is on Windows and macOS.
    // Linux tray icons report no position, so fall back to the corner
    // where panels usually live.
    if panel.move_window(Position::TrayCenter).is_ok() && !cfg!(target_os = "linux") {
        return;
    }
    let _ = panel.move_window(Position::BottomRight);
}

fn parse_args() -> (Option<String>, Option<String>) {
    let args: Vec<String> = std::env::args().collect();
    let flag = |name: &str| args.iter().position(|a| a == name).and_then(|i| args.get(i + 1).cloned());
    (flag("--page"), flag("--snapshot-page"))
}

pub fn run() {
    let (page, snapshot) = parse_args();
    if snapshot.is_some() {
        // Capturing the webview to a file has no cross-platform API in
        // Tauri; CI uses desktop/scripts/screenshot.mjs instead.
        eprintln!("--snapshot-page is not supported by the app; run `pnpm screenshot` (desktop/scripts/screenshot.mjs).");
        std::process::exit(2);
    }
    let base = api::discover_base();
    let open_main = page.is_some();

    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| show_main(app.clone(), None)))
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_positioner::init())
        .plugin(tauri_plugin_autostart::init(tauri_plugin_autostart::MacosLauncher::LaunchAgent, None))
        .manage(State { base: base.clone(), start_page: Mutex::new(page) })
        .invoke_handler(tauri::generate_handler![
            info,
            api,
            open_url,
            copy_text,
            autostart_enabled,
            set_autostart,
            show_main,
            hide_panel,
            resize_panel,
            quit,
            install_service,
        ])
        .setup(move |app| {
            #[cfg(target_os = "macos")]
            app.set_activation_policy(tauri::ActivationPolicy::Accessory);

            let panel = WebviewWindowBuilder::new(app, "panel", WebviewUrl::App("index.html#panel".into()))
                .title("tailmux")
                .inner_size(PANEL_WIDTH, 420.0)
                .decorations(false)
                .resizable(false)
                .always_on_top(true)
                .skip_taskbar(true)
                .visible(false)
                .build()?;
            let handle = app.handle().clone();
            panel.on_window_event(move |e| {
                // A tray panel goes away when you click elsewhere.
                if let tauri::WindowEvent::Focused(false) = e {
                    if let Some(p) = handle.get_webview_window("panel") {
                        let _ = p.hide();
                    }
                }
            });

            let main = WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html#main".into()))
                .title("tailmux")
                .inner_size(980.0, 640.0)
                .min_inner_size(820.0, 520.0)
                .center()
                .visible(open_main)
                .build()?;
            let handle = app.handle().clone();
            main.on_window_event(move |e| {
                // Closing the window keeps the tray app running.
                if let tauri::WindowEvent::CloseRequested { api, .. } = e {
                    api.prevent_close();
                    if let Some(w) = handle.get_webview_window("main") {
                        let _ = w.hide();
                    }
                }
            });

            tray::setup(app.handle(), base)?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tailmux");
}
