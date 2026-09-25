// The session on an addon's URL, the pure half (lib/auth.svelte.ts holds the session). The CCU
// convention appends ?sid=@..@ (task 6). Since task 125 (D-77) what goes there is never the session
// itself but its legacy alias - ten characters the addon CGIs parse, accepted by lighttpd's gate
// and the tclrega shim for /addons/ alone, never by the API - and only where the box says so:
// `legacy_session` on GET /nav and GET /addons is true for an addon that does not read the gate's
// X-Occulite-Session (task 88, D-67) while the legacy session is switched on for it. Absent means
// no ?sid=; the shell never appends one on its own.

/** `url` with `?sid=@<alias>@` appended when the box says the addon gets the legacy session and the alias is known. */
export function addonUrl(url: string, alias: string, legacySession = false): string {
    if (!alias || !legacySession) return url;
    return url + (url.includes('?') ? '&' : '?') + 'sid=@' + alias + '@';
}

/**
 * The look on an addon frame's URL (the embedding contract, docs/system-api.md): `theme=` and
 * `lang=` for the page's first paint, before the frame is inside the shell's message range.
 * The settings frame (AddonFrame) and a nav.d page's frame (NavFrame, B-200) both carry it; the
 * fragment stays at the end.
 */
export function withLook(url: string, theme: string, lang: string): string {
    const at = url.indexOf('#');
    const base = at < 0 ? url : url.slice(0, at);
    const frag = at < 0 ? '' : url.slice(at);
    return `${base}${base.includes('?') ? '&' : '?'}theme=${encodeURIComponent(theme)}&lang=${encodeURIComponent(lang)}${frag}`;
}
