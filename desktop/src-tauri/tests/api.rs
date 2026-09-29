//! Runs against a live daemon when TAILMUX_API is set; skipped otherwise.
//! Adds and removes a throwaway tailnet and round-trips the settings.
use serde_json::json;
use tailmux_desktop_lib::api;

fn base() -> Option<String> {
    std::env::var("TAILMUX_API").ok().filter(|s| !s.is_empty())
}

#[test]
fn status_and_stats() {
    let Some(base) = base() else { return };
    let st = api::call(&base, "GET", "status", None).unwrap();
    let (running, total) = api::running_count(&st);
    assert!(total >= running);
    let stats = api::call(&base, "GET", "stats", None).unwrap();
    assert!(stats.get("tailnets").is_some());
    let logs = api::call(&base, "GET", "logs?n=3", None).unwrap();
    assert!(logs.as_array().map(|a| a.len() <= 3).unwrap_or(true));
}

#[test]
fn add_and_remove_tailnet() {
    let Some(base) = base() else { return };
    let body = json!({"name": "test", "control_url": "http://127.0.0.1:9"});
    let added = api::call(&base, "POST", "tailnets", Some(body)).unwrap();
    assert_eq!(added["name"], "test");
    let st = api::call(&base, "GET", "status", None).unwrap();
    assert!(st["tailnets"].as_array().unwrap().iter().any(|t| t["name"] == "test"));
    api::call(&base, "POST", "tailnets/test/disable", None).unwrap();
    api::call(&base, "POST", "tailnets/test/enable", None).unwrap();
    assert_eq!(api::call(&base, "DELETE", "tailnets/test", None).unwrap(), serde_json::Value::Null);
    let st = api::call(&base, "GET", "status", None).unwrap();
    assert!(!st["tailnets"].as_array().unwrap().iter().any(|t| t["name"] == "test"));
    // A missing header or bad body must surface the daemon's message.
    let err = api::call(&base, "POST", "tailnets", Some(json!({"name": ""}))).unwrap_err();
    assert!(!err.is_empty());
}

#[test]
fn settings_round_trip() {
    let Some(base) = base() else { return };
    let cfg = api::call(&base, "GET", "config", None).unwrap();
    let c = &cfg["config"];
    let hostname = c["hostname"].as_str().unwrap_or("").to_string();
    let tun = c["tun"]["enabled"].as_bool().unwrap_or(false);
    let auto = c["updates"]["auto"].as_bool().unwrap_or(true);
    let r = api::call(&base, "PUT", "settings", Some(json!({"hostname": "tailmux-test", "tun": tun, "auto_update": auto}))).unwrap();
    assert_eq!(r["restart_required"], true);
    let cfg2 = api::call(&base, "GET", "config", None).unwrap();
    assert_eq!(cfg2["config"]["hostname"], "tailmux-test");
    // Put the original values back.
    api::call(&base, "PUT", "settings", Some(json!({"hostname": hostname, "tun": tun, "auto_update": auto}))).unwrap();
    let cfg3 = api::call(&base, "GET", "config", None).unwrap();
    assert_eq!(cfg3["config"]["hostname"].as_str().unwrap_or(""), hostname);
}
