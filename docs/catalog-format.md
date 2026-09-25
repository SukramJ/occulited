# The addon catalogue

The catalogue is one JSON file in this repository, [`catalog/catalog.json`](../catalog/catalog.json). It says
**where the addons' manifests are** and nothing else: an addon describes itself in its own
[`openccu-lite.json`](manifest-format.md), and the system reads that — from the package at install time, from
the repository for the Addons page. To list an addon, open a pull request that adds one entry.

```json
{
  "format": 1,
  "addons": [
    {
      "git": "https://github.com/homematic-community/ccu-addon-mosquitto",
      "manifest": "addon_files/openccu-lite.json"
    },
    {
      "git": "https://github.com/jp112sdl/JP-HB-Devices-addon",
      "manifest": "catalog/manifests/jp-hb-devices-addon.json",
      "untested": true
    }
  ]
}
```

| Field | Rules |
| --- | --- |
| `format` | `1`. A reader refuses another number. |
| `git` | The addon's repository, an `https://` URL without `.git`. Each repository once. For a GitHub repository the system finds the **latest release tag** through the GitHub API and reads the manifest at that tag, so the page describes what an install would get; a repository without a release is read at its default branch — and so is one whose latest release does not carry the manifest yet (the file was added after that release; a 404 at the tag), until a release has it. Other hosts are read at their default branch (`<git>/raw/branch/<default>/<manifest>`, the Gitea shape). |
| `manifest` | The path of `openccu-lite.json` **inside that repository** — usually where the packaging puts it at the root of the tarball (`addon_files/openccu-lite.json`). **An adapter manifest** for an addon whose author ships none lives in *this* repository under `catalog/manifests/<id>.json`, written by the catalogue maintainers; a path that begins with `catalog/manifests/` is read from here (bundled in the image, fetched from this repository on a refresh), never from `git`, while the manifest's own `release` names the author's repository. The system applies an adapter manifest at install when the package carries no manifest of its own. |
| `untested` | Optional, `true` when **nobody has tried the addon on openccu-lite yet**; set by the catalogue maintainers. The Addons page shows it as the label *untested* ("install at your own risk"). It grants and refuses nothing — every addon's manifest is applied as declared, and the install is the same (D-119, revised 2026-09-25). An entry without it carries no label. Catalogues written before then have a `verified` flag instead; the system ignores it. |

That is the whole format. Not in the catalogue, because the manifest has it: names, descriptions, icons, the
homepage, the release source, compatibility, the `runtime` block. Not anywhere: status notes, tiers, star counts
(the system fetches those from GitHub when the user runs an update check, and caches them).

## What the system does with it

- **The image carries a copy** of this file and of `catalog/manifests/` as `/etc/occulite/catalog.json` and
  `/etc/occulite/manifests/`, installed by the occulited package from the same commit as the binary. Until the
  user's first check the Addons page shows the entries by their repository name, with the adapter manifests'
  details already there.
- **The user's update check** (*Check for updates* on the Addons page) fetches this file from the configured URLs
  (`catalog.urls` in `occulited.json`; the published copy first, the bundled one as the fallback; the first entry
  per repository wins), then every entry's manifest at its latest release tag, the star counts and the latest
  releases. Everything is cached in occulited's state directory and shown from the cache; no fetch happens on a
  page load or in the background (D-90).
- **An install from the page** resolves the release from the manifest's `release`, downloads the asset, checks a
  `<asset>.sha256` sidecar when the release has one, and hands the archive to the same installer a manual upload
  takes. The package's own manifest is what gets applied; the fetched one stands in only when the package carries
  none (an adapter, or a release older than the addon's first manifest).

## The check on a pull request

`go test ./internal/manifest/` parses every file under `catalog/manifests/` and `catalog/catalog.json`: the JSON
must parse, an adapter manifest's `id` must equal its file name and it must name a `release`. CI runs it. The
manifest at `<git>/<manifest>` of a new entry is read by the reviewer, not by CI: the catalogue makes no promise
about a repository it does not own.
