// The shell's route tests, kept apart from router.svelte.ts so they are pure and vitest runs them
// (the router itself reads `location` when it is imported).

/**
 * Whether `path` is the page at `base`: that path, or one below it. A bare `startsWith` was the
 * mistake of B-63 - by it `/login` *is* `/log`, and a signed-in `/login` rendered the Log page -
 * so every page test of the shell goes through here. The query string is not part of `path`
 * (router.svelte.ts keeps it in `search`), so `/log?unit=x` is `/log` as well.
 */
export function onPage(path: string, base: string): boolean {
    return path === base || path.startsWith(base.endsWith('/') ? base : `${base}/`);
}

/** The decoded segment of `<base>/<segment>`; '' for any other path, a deeper one or a broken escape. */
export function segmentOf(path: string, base: string): string {
    const prefix = `${base}/`;
    if (!path.startsWith(prefix)) return '';
    const rest = path.slice(prefix.length);
    if (rest === '' || rest.includes('/')) return '';
    try {
        return decodeURIComponent(rest);
    } catch {
        return '';
    }
}

/**
 * The shell's path of an addon's settings page (its Config-Url in the shell's frame). It was
 * `/addons/<id>`, the prefix of the addons' www directories, which lighttpd answers itself: typed,
 * bookmarked or reloaded, that path got lighttpd's 403 (or the addon's own page) and never reached
 * the shell (B-81). `/addon-settings/` overlaps with no directory and no other page of the shell.
 */
export const SETTINGS_BASE = '/addon-settings';

export function settingsPath(id: string): string {
    return `${SETTINGS_BASE}/${encodeURIComponent(id)}`;
}

/**
 * The path an old in-app path of the shell stands for: `/addons/<id>` is the settings page at
 * `/addon-settings/<id>`. In the app only - a link or a history entry that still carries it; loaded
 * from the server, lighttpd answers that path. '' for every other path, `/addons/<id>/…` included.
 */
export function settingsAlias(path: string): string {
    const id = segmentOf(path, '/addons');
    return id ? settingsPath(id) : '';
}

/**
 * The target of `/login?return=<target>`, or '' when there is none the shell may follow: only a
 * path of this origin. A string starting with one slash is not enough - `//host`, `/\host`, a tab
 * the URL parser drops (`/<tab>/host`) and a path that normalises to two slashes (`/.//host`) all
 * lead a browser to another host - so the target is resolved the way the browser will resolve it,
 * and must come out on this origin and not as a path starting with two slashes. `/login` itself is
 * no target: that is the loop.
 */
export function safeReturn(raw: string | null | undefined, origin: string): string {
    if (!raw || raw[0] !== '/') return '';
    let u: URL;
    try {
        u = new URL(raw, origin);
    } catch {
        return '';
    }
    if (u.origin !== origin) return '';
    const target = u.pathname + u.search + u.hash;
    if (target.startsWith('//') || onPage(u.pathname, '/login')) return '';
    return target;
}

/** A signed-in session sent on from `/login` to `target` at `at` (ms), remembered for the tab. */
export interface Bounce {
    target: string;
    at: number;
}

/** How long a second bounce to the same target counts as a loop. A Pi takes seconds per round. */
export const BOUNCE_WINDOW_MS = 30_000;

export function parseBounce(raw: string | null | undefined): Bounce | null {
    if (!raw) return null;
    try {
        const b = JSON.parse(raw) as Partial<Bounce>;
        return typeof b.target === 'string' && typeof b.at === 'number' ? {target: b.target, at: b.at} : null;
    } catch {
        return null;
    }
}

/**
 * Whether a signed-in `/login?return=<target>` may send the browser on again. Not when it did so
 * for the same target a moment ago and was sent back: the page's gate did not take the session
 * (the token session of B-63, a session file the gate cannot see), and another round would only
 * repeat - forever, in an addon's frame.
 */
export function mayBounce(last: Bounce | null, target: string, now: number): boolean {
    if (!last || last.target !== target) return true;
    const age = now - last.at;
    return age < 0 || age > BOUNCE_WINDOW_MS;
}
