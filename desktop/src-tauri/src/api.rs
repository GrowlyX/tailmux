//! The daemon's loopback HTTP API. All requests go through here, from
//! Rust: the daemon sends no CORS headers on purpose, and every changing
//! request must carry `X-Tailmux: 1`, which a browser fetch could not
//! add across origins.

use serde_json::Value;
use std::path::PathBuf;
use std::time::Duration;

/// Finds the daemon's API address the way the CLI does: `TAILMUX_API`
/// wins, else the `http` key of the first config file that exists.
pub fn discover_base() -> String {
    if let Ok(url) = std::env::var("TAILMUX_API") {
        if !url.is_empty() {
            return url.trim_end_matches('/').to_string();
        }
    }
    for path in config_paths() {
        let Ok(text) = std::fs::read_to_string(&path) else { continue };
        let Ok(obj) = serde_json::from_str::<Value>(&text) else { continue };
        let mut addr = obj
            .get("http")
            .and_then(Value::as_str)
            .unwrap_or("127.0.0.1:1056")
            .to_string();
        if addr.starts_with(':') || addr.starts_with("0.0.0.0:") {
            let port = addr.rsplit(':').next().unwrap_or("1056");
            addr = format!("127.0.0.1:{port}");
        }
        return format!("http://{addr}");
    }
    "http://127.0.0.1:1056".to_string()
}

fn config_paths() -> Vec<PathBuf> {
    let mut paths = Vec::new();
    if let Ok(p) = std::env::var("TAILMUX_CONFIG") {
        if !p.is_empty() {
            paths.push(PathBuf::from(p));
        }
    }
    #[cfg(target_os = "windows")]
    {
        for var in ["ProgramData", "APPDATA"] {
            if let Ok(dir) = std::env::var(var) {
                paths.push(PathBuf::from(dir).join("tailmux").join("config.json"));
            }
        }
    }
    #[cfg(not(target_os = "windows"))]
    {
        if let Ok(home) = std::env::var("HOME") {
            paths.push(PathBuf::from(home).join(".config/tailmux/config.json"));
        }
        paths.push(PathBuf::from("/etc/tailmux/config.json"));
        #[cfg(target_os = "linux")]
        paths.push(PathBuf::from("/home/linuxbrew/.linuxbrew/etc/tailmux/config.json"));
        #[cfg(target_os = "macos")]
        {
            paths.push(PathBuf::from("/opt/homebrew/etc/tailmux/config.json"));
            paths.push(PathBuf::from("/usr/local/etc/tailmux/config.json"));
        }
    }
    paths
}

fn agent(timeout: Duration) -> ureq::Agent {
    ureq::Agent::config_builder()
        .timeout_global(Some(timeout))
        .proxy(None)
        .http_status_as_error(false)
        .build()
        .into()
}

/// One request. Returns the parsed JSON body (null for empty bodies) or
/// an error string with the daemon's own message when it sent one.
pub fn call(base: &str, method: &str, path: &str, body: Option<Value>) -> Result<Value, String> {
    let url = format!("{}/{}", base.trim_end_matches('/'), path.trim_start_matches('/'));
    let method = method.to_ascii_uppercase();
    // Polls must fail fast so the UI shows "offline" promptly; changing
    // requests can take a while (joining a tailnet waits for its login
    // URL, an update downloads a release).
    let agent = agent(Duration::from_secs(if method == "GET" { 3 } else { 90 }));
    let response = if method == "GET" {
        agent.get(&url).call()
    } else {
        // Anything that changes state must carry X-Tailmux; adding it to
        // every non-GET is simpler than listing the endpoints.
        let req = match method.as_str() {
            "POST" => agent.post(&url),
            "PUT" => agent.put(&url),
            "DELETE" => agent.delete(&url).force_send_body(),
            other => return Err(format!("unsupported method {other}")),
        }
        .header("X-Tailmux", "1");
        match body {
            Some(json) => req.header("Content-Type", "application/json").send_json(json),
            None => req.send_empty(),
        }
    };
    let mut response = response.map_err(|e| describe(&e))?;
    let status = response.status().as_u16();
    let text = response
        .body_mut()
        .read_to_string()
        .map_err(|e| e.to_string())?;
    if !(200..300).contains(&status) {
        let msg = text.trim();
        return Err(if msg.is_empty() { format!("HTTP {status}") } else { msg.to_string() });
    }
    if text.trim().is_empty() {
        return Ok(Value::Null);
    }
    serde_json::from_str(&text).map_err(|e| format!("bad JSON from {path}: {e}"))
}

fn describe(e: &ureq::Error) -> String {
    match e {
        ureq::Error::Io(io) => match io.kind() {
            std::io::ErrorKind::ConnectionRefused => "connection refused".to_string(),
            std::io::ErrorKind::TimedOut => "timed out".to_string(),
            _ => io.to_string(),
        },
        ureq::Error::Timeout(_) => "timed out".to_string(),
        other => other.to_string(),
    }
}

/// Number of connected tailnets in a /status body, for the tray icon.
pub fn running_count(status: &Value) -> (usize, usize) {
    let tailnets = status
        .get("tailnets")
        .and_then(Value::as_array)
        .map(Vec::as_slice)
        .unwrap_or(&[]);
    let running = tailnets
        .iter()
        .filter(|t| {
            t.get("enabled").and_then(Value::as_bool).unwrap_or(false)
                && t.get("state").and_then(Value::as_str) == Some("Running")
        })
        .count();
    (running, tailnets.len())
}
