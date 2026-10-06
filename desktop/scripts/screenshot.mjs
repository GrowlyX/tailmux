// Captures the panel and every main window page, light and dark, as
// PNGs. Serves the built frontend (dist/) from a tiny HTTP server whose
// /api/ path proxies to the daemon and adds the X-Tailmux header, so the
// pages run unchanged in headless Chromium (src/lib/bridge.ts falls back
// to that proxy when Tauri isn't there). This is the snapshot path CI
// uses on ubuntu-latest and windows-latest: capturing a Tauri webview
// from Rust has no cross-platform API, and Chromium renders the same DOM.
//
//   pnpm build && pnpm screenshot [--out desktop/screenshots] [--only panel,settings] [--dark|--light]
//
// Reads TAILMUX_API (default http://127.0.0.1:1056). Exits non-zero when
// the daemon is unreachable so CI fails loudly instead of shipping
// offline screenshots.
import http from "node:http";
import { createReadStream, existsSync, mkdirSync, statSync } from "node:fs";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const here = dirname(fileURLToPath(import.meta.url));
const dist = resolve(here, "..", "dist");
const args = process.argv.slice(2);
const flag = (n) => { const i = args.indexOf(n); return i >= 0 ? args[i + 1] : undefined; };
const out = resolve(flag("--out") ?? join(here, "..", "screenshots"));
const only = flag("--only")?.split(",");
const schemes = args.includes("--dark") ? ["dark"] : args.includes("--light") ? ["light"] : ["light", "dark"];
const daemon = (process.env.TAILMUX_API ?? "http://127.0.0.1:1056").replace(/\/$/, "");

if (!existsSync(join(dist, "index.html"))) {
  console.error("dist/ is missing; run `pnpm build` first");
  process.exit(1);
}
try {
  const r = await fetch(`${daemon}/status`);
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
} catch (e) {
  console.error(`daemon at ${daemon} is unreachable: ${e.message ?? e}`);
  process.exit(1);
}

const types = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" };
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://x");
  if (url.pathname.startsWith("/api/")) {
    const chunks = [];
    for await (const c of req) chunks.push(c);
    const body = Buffer.concat(chunks);
    try {
      const up = await fetch(daemon + url.pathname.slice(4) + url.search, {
        method: req.method,
        headers: { "X-Tailmux": "1", ...(body.length ? { "Content-Type": "application/json" } : {}) },
        body: body.length ? body : undefined,
      });
      res.writeHead(up.status, { "Content-Type": up.headers.get("content-type") ?? "text/plain" });
      res.end(Buffer.from(await up.arrayBuffer()));
    } catch (e) {
      res.writeHead(502);
      res.end(String(e));
    }
    return;
  }
  let file = join(dist, url.pathname === "/" ? "index.html" : url.pathname);
  if (!existsSync(file) || statSync(file).isDirectory()) file = join(dist, "index.html");
  res.writeHead(200, { "Content-Type": types[extname(file)] ?? "application/octet-stream" });
  createReadStream(file).pipe(res);
});
await new Promise((ok) => server.listen(0, "127.0.0.1", ok));
const origin = `http://127.0.0.1:${server.address().port}`;

const shots = [
  { name: "panel", url: "/#panel", width: 372, height: 500, fit: true },
  ...["overview", "tailnets", "devices", "exit-node", "settings", "logs"].map((p) => ({ name: p, url: `/#main?page=${p}`, width: 980, height: 640 })),
].filter((s) => !only || only.includes(s.name));

mkdirSync(out, { recursive: true });
const browser = await chromium.launch();
let failed = 0;
for (const scheme of schemes) {
  for (const s of shots) {
    const page = await browser.newPage({ viewport: { width: s.width, height: s.height }, colorScheme: scheme, deviceScaleFactor: 2 });
    await page.goto(origin + s.url);
    // Two polls' worth, so the chart, peers and logs are all in.
    await page.waitForTimeout(2500);
    const offline = await page.evaluate(() => document.body.innerText.includes("daemon isn't reachable"));
    if (offline) failed++;
    const file = join(out, `${s.name}-${scheme}.png`);
    if (s.fit) {
      // The panel window is as tall as its content.
      const h = await page.evaluate(() => document.querySelector("#app > .panel")?.getBoundingClientRect().height ?? 400);
      await page.setViewportSize({ width: s.width, height: Math.ceil(h) });
    }
    await page.screenshot({ path: file });
    console.log(`wrote ${file}${offline ? " (offline!)" : ""}`);
    await page.close();
  }
}
await browser.close();
server.close();
process.exit(failed ? 1 : 0);
