//! The tray icon: Tailscale's 3x3 dot grid with one dot lit per
//! connected tailnet, refreshed from /status every two seconds.

use crate::api;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tauri::image::Image;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, Theme};

/// Pre-rendered by desktop/scripts/tray-icons.py. Index 0..=9 lit,
/// then the dimmed "offline" grid.
const LIGHT: [&[u8]; 11] = [
    include_bytes!("../icons/tray/light-0.png"),
    include_bytes!("../icons/tray/light-1.png"),
    include_bytes!("../icons/tray/light-2.png"),
    include_bytes!("../icons/tray/light-3.png"),
    include_bytes!("../icons/tray/light-4.png"),
    include_bytes!("../icons/tray/light-5.png"),
    include_bytes!("../icons/tray/light-6.png"),
    include_bytes!("../icons/tray/light-7.png"),
    include_bytes!("../icons/tray/light-8.png"),
    include_bytes!("../icons/tray/light-9.png"),
    include_bytes!("../icons/tray/light-off.png"),
];
const DARK: [&[u8]; 11] = [
    include_bytes!("../icons/tray/dark-0.png"),
    include_bytes!("../icons/tray/dark-1.png"),
    include_bytes!("../icons/tray/dark-2.png"),
    include_bytes!("../icons/tray/dark-3.png"),
    include_bytes!("../icons/tray/dark-4.png"),
    include_bytes!("../icons/tray/dark-5.png"),
    include_bytes!("../icons/tray/dark-6.png"),
    include_bytes!("../icons/tray/dark-7.png"),
    include_bytes!("../icons/tray/dark-8.png"),
    include_bytes!("../icons/tray/dark-9.png"),
    include_bytes!("../icons/tray/dark-off.png"),
];

const TRAY_ID: &str = "tailmux";

fn icon(app: &AppHandle, lit: Option<usize>) -> Image<'static> {
    // White dots suit dark taskbars and Linux panels. Windows in light
    // mode has a light taskbar, so use the dark set there. macOS would
    // want a template image; it ships the SwiftUI app instead.
    let light_taskbar = cfg!(target_os = "windows")
        && app
            .get_webview_window("main")
            .and_then(|w| w.theme().ok())
            .map(|t| t == Theme::Light)
            .unwrap_or(false);
    let set = if light_taskbar { &DARK } else { &LIGHT };
    let idx = lit.map(|n| n.min(9)).unwrap_or(10);
    Image::from_bytes(set[idx]).expect("tray icon png")
}

pub fn setup(app: &AppHandle, base: String) -> tauri::Result<()> {
    let open = MenuItem::with_id(app, "open", "Open tailmux", true, None::<&str>)?;
    let panel = MenuItem::with_id(app, "panel", "Show panel", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
    let sep = PredefinedMenuItem::separator(app)?;
    // Linux tray icons only have a menu, no click events, so the panel
    // needs an entry there.
    let menu = if cfg!(target_os = "linux") {
        Menu::with_items(app, &[&panel, &open, &sep, &quit])?
    } else {
        Menu::with_items(app, &[&open, &sep, &quit])?
    };

    TrayIconBuilder::with_id(TRAY_ID)
        .icon(icon(app, None))
        .icon_as_template(true)
        .tooltip("tailmux: connecting")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, e| match e.id().as_ref() {
            "open" => crate::show_main(app.clone(), None),
            "panel" => crate::toggle_panel(app),
            "quit" => app.exit(0),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            let app = tray.app_handle();
            // The positioner needs the icon's rectangle before it can
            // place the panel under it.
            tauri_plugin_positioner::on_tray_event(app, &event);
            if let TrayIconEvent::Click { button: MouseButton::Left, button_state: MouseButtonState::Up, .. } = event {
                crate::toggle_panel(app);
            }
        })
        .build(app)?;

    let handle = app.clone();
    let stop = Arc::new(AtomicBool::new(false));
    std::thread::Builder::new()
        .name("tray-poll".into())
        .spawn(move || {
            let mut last: Option<(Option<usize>, String)> = None;
            while !stop.load(Ordering::Relaxed) {
                let (lit, tip) = match api::call(&base, "GET", "status", None) {
                    Ok(v) => {
                        let (running, total) = api::running_count(&v);
                        (Some(running), format!("tailmux: {running} of {total} tailnets connected"))
                    }
                    Err(_) => (None, "tailmux: daemon not running".to_string()),
                };
                let next = (lit, tip.clone());
                if last.as_ref() != Some(&next) {
                    if let Some(tray) = handle.tray_by_id(TRAY_ID) {
                        let _ = tray.set_icon(Some(icon(&handle, lit)));
                        let _ = tray.set_tooltip(Some(&tip));
                    }
                    last = Some(next);
                }
                std::thread::sleep(Duration::from_secs(2));
            }
        })?;
    Ok(())
}
