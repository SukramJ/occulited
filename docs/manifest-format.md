# The addon manifest: `openccu-lite.json`

An addon tells openccu-lite what it is and what it needs in **one file, `openccu-lite.json`**, at the **root of its
package tarball** (beside `update_script`) and at a path of its repository. The CCU3 and OpenCCU ignore the file.
This document is normative; [`manifest.schema.json`](manifest.schema.json) is the same in JSON Schema for an editor
and a pull-request check. The catalogue ([catalog-format.md](catalog-format.md)) only says where the manifests are.

**Two rules decide everything else** (D-119):

1. **The package's manifest is authoritative.** At install and update the system reads `openccu-lite.json` out of
   the archive in Go, before the addon's `update_script` runs, and applies it as declared — the runtime block becomes
   the addon's policy, the UI facts drive the shell. The accepted copy is kept beside the policy, root-owned; the
   copy in the addon's directory is never read again (a confined addon owns that directory). The repository's copy
   at the latest release tag is only what the Addons page shows before an install (from the default branch while
   the latest release does not carry the file yet, and for a repository without releases).
2. **The addon declares, the system trusts.** There is no grant, no request/grant split and no consent dialog:
   `root: true` runs the addon as root, `capabilities`, `groups`, `paths`, `api_scopes` are applied. The guard rails
   that protect the system itself stay: a declared port is a switch in the firewall and **closed** until the user
   opens it (D-29), `data_dirs` are fenced to the addon's own directories under `/usr/local/` (D-52), no root addon
   has `CAP_SYS_ADMIN` unless it declares it (D-66), and what an addon writes into `/firmware/rftypes` lands in the
   writable extension directory (task 97). `untested` in the catalogue is a label and grants nothing.

## The file

```json
{
  "format": 1,
  "id": "mosquitto",
  "version": "2.1.2+3",
  "name": "Mosquitto",
  "description": {
    "de": "Der MQTT-Broker — die Brücke zu Home Assistant, ioBroker und allem anderen.",
    "en": "The MQTT broker — the bridge to Home Assistant, ioBroker and everything else."
  },
  "homepage": "https://github.com/homematic-community/ccu-addon-mosquitto",
  "licence": "EPL-2.0",
  "release": {
    "github": "homematic-community/ccu-addon-mosquitto",
    "asset": "mosquitto-{arch}-{version}.tar.gz",
    "fallback_asset": "mosquitto-{version}.tar.gz"
  },
  "requires": { "architectures": ["armv7l", "aarch64", "x86_64"] },
  "ui": { "icon": "mosquitto/www/icon.svg", "session_header": true },
  "runtime": {
    "needs": [],
    "ports": [1883, 8883],
    "port_info": {
      "1883": { "proto": "tcp", "label": { "de": "MQTT, unverschlüsselt", "en": "MQTT, plain" } },
      "8883": { "proto": "tcp", "tls": true, "label": { "de": "MQTT über TLS", "en": "MQTT over TLS" } }
    },
    "note": {
      "de": "Beide Ports sind in der Firewall geschlossen, bis sie auf der Seite Zusatzsoftware (Ports der Zusatzsoftware) geöffnet werden.",
      "en": "Both ports stay closed in the firewall until opened on the Addons page (Addon ports)."
    }
  }
}
```

Texts (`name`, `description`, `note`, a port's `label`) are `{"de": …, "en": …}` or one plain string for both; the UI
shows the user's language and falls back to English, then German. A key this version of occulited does not know is
ignored (the schema is the strict reader), so a manifest written for a newer system still installs on an older one.
The file is at most 256 KiB.

### Identity

| Key | Required | Rules |
| --- | --- | --- |
| `format` | yes | `1`. |
| `id` | yes | `^[a-z0-9][a-z0-9_.-]{0,31}$` — **the addon's rc.d id**, the name `update_script` links into `/usr/local/etc/config/rc.d/`. The system checks it after the install against the rc.d entries the installer created or changed; a manifest whose `id` names none of them is not applied and the install log says so. |
| `version` | no | The package's version, `1.2.3`, `3.0.0-beta.22`, `2.1.2+3`. Informational: the installed version comes from the rc.d script's `info` as it always did. |
| `name` | yes | Text. What the Addons page and the menu show. |
| `description` | no | Text, one or two sentences. |
| `homepage` | no | An `http(s)` URL. |
| `licence` | no | An SPDX identifier. |

### `release` — where the packages are

Read by the update check and by the catalogue install. An addon that is only ever installed by upload leaves it out.

| Key | Rules |
| --- | --- |
| `github` | `owner/repo`. Releases are listed through the GitHub API; drafts are skipped, prereleases too unless `prerelease` is true. |
| `asset` | The asset name with the placeholders `{arch}` (`uname -m`: `armv7l`, `aarch64`, `x86_64`) and `{version}` (the tag without a leading `v`; matches anything, so `3.5.2-beta` resolves). |
| `assets` | A map architecture → pattern, for projects that do not name packages by `uname -m`. Tried before `asset`. |
| `fallback_asset` | Tried when nothing else matches: a package for every architecture (scripts, device descriptions). It must be genuinely architecture-independent. |
| `prerelease` | `true` for a project without a stable release yet. |

A `<asset>.sha256` beside the asset is checked when it exists; without one the download is trusted on HTTPS alone.

### `requires` — compatibility

| Key | Rules |
| --- | --- |
| `lite` | The oldest openccu-lite version the addon runs on (`1.0.0`). The page says so when the system is older; the install is not refused. |
| `rega` | `true` when the addon needs the ReGa, which openccu-lite does not have — the catalogue lists such an addon only as *untested*; the system disables one it finds after an update from OpenCCU, and the Addons page marks it incompatible. **A manifest without it says the addon runs without the ReGa**: it replaces the `openccu-lite.ok` marker of the porting kit, which stays accepted for addons without a manifest. |
| `forms` | The product forms: `sd` (the SD-card images), `ova` (the virtual machine). Empty = all. |
| `architectures` | The `uname -m` names the package exists for; empty = any. Both ARM products of openccu-lite are `aarch64`; `armv7l` matches only eQ-3's own 32-bit CCU3 firmware. |

### `ui` — what the shell shows and how it opens the addon

| Key | Rules |
| --- | --- |
| `icon`, `icon_dark` | A square icon, SVG preferred (else PNG, at least 64×64), as a **path relative to the package root** (`mosquitto/www/icon.svg`); `_dark` for dark backgrounds. Used in the addon menu, the tab bar, the Services rows and the Addons page. |
| `logo`, `logo_dark` | A wide logo (height about 48–96 px), the same way. Used on the addon's card. If only one of icon and logo is given, the other falls back to it. |
| `settings_url` | The addon's settings page **where its `Config-Url` is not it**: a path under `/addons/`, with a query. Homematic Manager's `Config-Url` is the CCU's button into the app, its settings page is `/addons/hmm/settings.cgi?cmd=config`. Without it the `Config-Url` is the settings page. |
| `session_header` | `true`: **this version** reads the gate's `X-Occulite-Session` everywhere the shell opens it — its frontend and its settings page — so the shell leaves `?sid=@…@` off those URLs. The manifest is per version, so this is a boolean, not a "since" version. Keep accepting `?sid=` for the CCU3 and OpenCCU. |
| `own_updater` | `true`: the addon still carries an update mechanism of its own, which the system's updates bypass; the page notes it. Addons should not ship one, or hide it on openccu-lite (`grep -qx 'VARIANT=lite' /VERSION`). |

### `runtime` — what the addon needs to run

Under systemd every addon runs in a generated unit, as its own user `addon-<id>` unless it declares `root`. The block
says what that unit gets; **applied as declared** (rule 2). An addon without a `runtime` block runs confined with
nothing but its own three directories and is shown as *undeclared* on the Addons and Services pages — that marking is
what a missing block looks like, and the block is what removes it. **One exception:** a block that says nothing but
the start order (`needs`, `start`, with or without a `note`) keeps the marking, because the start order is no
statement of what the addon needs to run. Any other key removes it, `daemon: true` and `api_scopes` included — so an
addon that needs nothing beyond its own directories declares `{"daemon": true, "needs": [...], "start": "early"}`, or
an empty block `{}` when it keeps no process running. The busybox products ignore the block.

| Key | Rules |
| --- | --- |
| `root` | `true`: the unit runs as root (talks to hardware, patches the system). Shown as *root (unsafe)*. A root addon still has no `CAP_SYS_ADMIN` (D-66): `mount -o remount,rw /` answers *permission denied* and what it writes into `/firmware/rftypes` lands in the writable extension directory; declare `capabilities: ["CAP_SYS_ADMIN"]` beside `root` only when it truly has to mount, and the page marks it *may mount file systems*. |
| `capabilities` | `CAP_*` names for `AmbientCapabilities=` (`CAP_NET_BIND_SERVICE`, `CAP_NET_RAW`, …). Empty: an empty bounding set. |
| `groups` | Supplementary groups (`dialout`, `video`). Every confined addon is in `certs` and may read the system's TLS certificate (D-46) without declaring it. |
| `paths` | Extra writable paths for `ReadWritePaths=` beyond the addon's own directories, `/usr/local/etc/config/rc.d`, `/run`, `/var/log`, `/tmp` and `/var/tmp`. May name a shared directory; never chowned. |
| `data_dirs` | The addon's **own state directories outside its three standard ones** (`/usr/local/addons/<id>`, `/usr/local/etc/config/addons/<id>`, `/usr/local/etc/config/addons/www/<id>`): taken over — chowned to `addon-<id>` and put on `ReadWritePaths=` — when the addon is confined (D-52), created when missing. Absolute paths under `/usr/local/` only. The system adds the convention `/usr/local/<id>` on its own. Guard rails, whatever the manifest says: never `/usr/local` itself or one of its shared trees (`addons`, `etc`, `tmp`, `backup`, `crontabs`, `lost+found`, `var`, `sdcard`, `usb`, dotfiles), never another addon's directory or a path inside one, never a symlink. |
| `ports`, `port_info` | The ports the addon listens on. Each is a switch on the Addons page (*Addon ports*), **closed by default** (D-29, D-47); an opened one is a rule on the Firewall page with the addon as its owner. This holds for any mode — the declaration is about reachability, not confinement. `port_info` is keyed by the port as a string: `proto` (`tcp`/`udp`, informational — the firewall opens both), `tls` (a badge, so a user can open the TLS listener and leave the plain one closed), `label` (Text). A key that names no port in `ports` is refused. |
| `needs` | The interface processes the addon talks to, out of `rfd`, `hmipserver`, `hs485d`: its unit starts after them. **Absent = undeclared**, the safe default (after rfd and hmipserver). **`[]` = none**: it starts right after the network (a broker, a web page). An id the system does not know makes the declaration unusable (logged; the default order). |
| `start` | `"early"`: the addon **copes with interface processes that do not answer yet** — it retries within seconds and logs no error lines while it waits — so its unit starts before them (D-75). The user can switch the early start off, globally and per addon. Any other value is refused. |
| `daemon` | `true`: the addon **keeps a process running** after its rc.d `start` (a broker, a server, Node-RED). Its unit is a oneshot that stays *active* either way, so without this an empty unit reads *Completed* — right for an addon that only prepares things, wrong for a daemon that died. With it, a unit whose processes are all gone shows as **Exited** (red) on the Services and Addons pages, with *Start* offered and a Status warning. The system also learns it: once the addon's unit has held a process after a start, it counts as a daemon until the addon is removed; the field covers the very first start. |
| `api_scopes` | The scopes of the addon's **own API token** (D-85), out of `meta:read`, `meta:write`, `system:read`, `logs:read`, `system:write`, `addons:write`, `led`, `rpc:read`, `rpc:operate`, `rpc:configure`, `rpc:admin`. The system mints a token with exactly these at every start and writes it to `/run/occulite/addon-tokens/<id>.api` (`0600`, the addon's user). **Never granted:** `*`, `auth:admin`, `power`, `backup` — such a name, or one the system does not know, is logged at the mint and left out. Declare only what the addon uses: every addon reads names and rooms with the local token without any declaration. |
| `note` | Text: why the addon needs what it declares, and what it contacts outside the system (D-90). Shown on the Addons page. No internal ids in it. |

## Where the system reads the manifest, and when

| Moment | Source | What is used |
| --- | --- | --- |
| **Install or update** (upload or catalogue) | `openccu-lite.json` at the root of the archive, read in Go before `update_script` runs | everything: the policy from `runtime`, the stored copy for `ui`, `requires`, `release` |
| **A package without a manifest** | the catalogue's adapter manifest for the id (`catalog/manifests/<id>.json`), else the manifest fetched for the page | the same |
| **Neither** (an arbitrary CCU addon) | — | the system's default mode, no extras: today's behaviour |
| **The Addons page before an install** | `<git>/<manifest>` at the latest release tag, fetched on the user's check and cached | name, description, icons, homepage, `requires`, `release` (the latest version), `untested` from the catalogue |
| **The update check** | the installed addon's stored manifest, `release` | the newest matching release |

The stored copy lives in `/usr/local/etc/config/addon-policy/<id>.manifest.json`, root-owned, written by the install
and removed with the addon's policy. It is rewritten by every install and update, so a release that declares less than
its predecessor loses what it dropped at once — a closed port, a group. A user's own choice on the Services page (the
switch to root and back) stands across updates; the runtime block's facts follow the package all the same.

## For addon authors

- Put `openccu-lite.json` where your packaging copies it to the **root of the tarball**, next to `update_script`, and
  name that path in the catalogue entry. The CCU3 and OpenCCU ignore the file.
- Bump nothing for the manifest alone: the system reads it with every install, and `version` is informational.
- Declare only what the addon uses. `needs: []` and `start: "early"` shorten every boot; `ports` give the user a
  switch instead of a closed door; `data_dirs` is what keeps a confined addon writing where it always did.
- Validate with the schema: `npx ajv validate -s manifest.schema.json -d openccu-lite.json`, or any JSON Schema
  2020-12 validator. The system's own reader is `internal/manifest` in this repository.
