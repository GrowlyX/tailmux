# binaries/

Tauri bundles `tailmux-<target-triple>[.exe]` from here next to the app
(`bundle.externalBin`). The release pipeline drops the real daemon build in
before `pnpm tauri build`; for local builds `scripts/ensure-sidecar.mjs`
(run by `beforeBuildCommand`) writes a placeholder so bundling works.

To bundle the real thing locally:

    go build -o desktop/src-tauri/binaries/tailmux-$(rustc -vV | sed -n 's/host: //p') ./cmd/tailmux
