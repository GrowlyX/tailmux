// Tauri refuses to bundle when an externalBin is missing. The release
// pipeline provides binaries/tailmux-<triple>; for local builds, write a
// stub so `pnpm tauri build` works. The app treats tiny files as "not
// bundled" and looks for tailmux on PATH instead.
import { execSync } from "node:child_process";
import { existsSync, writeFileSync, chmodSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const host = execSync("rustc -vV").toString().match(/^host: (.+)$/m)[1];
const ext = host.includes("windows") ? ".exe" : "";
const path = join(here, "..", "src-tauri", "binaries", `tailmux-${host}${ext}`);
if (!existsSync(path)) {
  writeFileSync(path, ext ? "" : "#!/bin/sh\necho 'tailmux is not bundled with this build' >&2\nexit 1\n");
  if (!ext) chmodSync(path, 0o755);
  console.log(`wrote placeholder sidecar ${path}`);
}
