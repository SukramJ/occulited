/**
 * The icon an addon shows in the CCU WebUI, taken from the `Info:` line of its `info` operation.
 *
 * That line is part of the addon ABI (ROADMAP task 10) and occulited already parses it into
 * `Addon.info`. It is **HTML written by the addon**, so this extracts the image's `src` rather than
 * rendering the markup, and accepts only a path into that addon's own `/addons/<id>/` directory —
 * never an absolute URL, a `data:` or a `javascript:` one.
 *
 * Both spellings occur in the wild. Measured on the lab box, 2026-09-07:
 *
 *   mosquitto           <img src="/addons/mosquitto/mosquitto-text-side-28.png" width="240"/>
 *   redmatic            <img src="/addons/redmatic/redmatic5-wide.png" height="48"/>
 *   jp-hb-devices-addon <img src='../addons/jp-hb-devices-addon/jp-hb-devices-addon.png'></img>
 *   hm2mqtt             (an Info: line, but no image at all)
 *
 * The third is relative to the WebUI page it was written for and is normalised to the first form.
 * The fourth is why every caller needs a fallback. Note the addon-authored `width="240"` and
 * `height="48"`: these are sized for a full-width WebUI panel, so the caller must box them.
 */
export function addonIconSrc(id: string, info: string | undefined): string {
    if (!info) return '';
    const m = /<img\b[^>]*\bsrc\s*=\s*["']([^"']+)["']/i.exec(info);
    let src = m?.[1]?.trim() ?? '';
    if (src === '') return '';
    if (/^\.\.\/addons\//.test(src)) src = src.slice(2); // "../addons/x/y" -> "/addons/x/y"
    const want = `/addons/${id}/`;
    if (!src.startsWith(want)) return '';
    return src.slice(want.length).includes('..') ? '' : src;
}
