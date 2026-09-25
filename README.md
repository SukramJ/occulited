# occulited

The system service of [openccu-lite](https://github.com/hobbyquaker/openccu-lite), a Homematic CCU firmware without ReGaHSS: one static Go binary with an embedded web UI (Svelte 5) that keeps the metadata (device and channel names, rooms, functions) and its API, the sessions and users, the system administration (services, interfaces, firmware, log, backup, updates) and the addon management of the system. The image builds it from this repository at a pinned commit or tag (`buildroot-external/package/occulited` there).

Layout: `cmd/occulited` (the binary: daemon, `helper` for the privileged half), `internal/` (the packages), `ui/` (the web UI; `internal/ui/dist` is the committed build, so the image needs no Node), `deploy/` (lighttpd, systemd, the `tclrega` shim), `fixtures/` (the conformance corpus of the metadata API). `scripts/build.sh` cross-builds the static binaries for x86_64, aarch64 and armv7l.

Build and test: Go 1.26+, `go build ./cmd/occulited`, `go test ./...`; the UI with Node 24: `cd ui && npm ci && npm run check && npm run build`, the browser suite `npm run test:e2e`.

License: GPL-3.0-only (`LICENSE`). occulited links Mathias Dzionsko's [go-hmccu](https://github.com/mdzio/go-hmccu) (v2), which is under the GPL-3.0.

Copyright (C) 2026 Sebastian Raff
