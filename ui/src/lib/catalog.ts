/*
 * The catalogue's pure helpers (task 56): the order of the cards, the star count as text, the
 * repository link of an entry, and the addons with an update waiting. `catalog.test.ts` covers
 * them.
 */

/** the fields of a catalogue item these helpers read (D-119: the manifest's id may be unknown before the first check) */
export interface CatalogLike {
    id?: string;
    name: string;
    stars?: number;
    git?: string;
    release?: {github?: string};
}

/**
 * The order of the cards: by GitHub stars, most first (maintainer, 2026-09-11). Equal counts by
 * name; entries whose count is not known come last, by name. The API drops a count of 0
 * (`omitempty`), so an entry nobody starred reads as unknown, which is where it belongs anyway.
 * The sort is in the page so the box's daily star refresh reorders the cards on the next load
 * without any index change.
 */
export function compareEntries(a: CatalogLike, b: CatalogLike): number {
    const ka = a.stars === undefined ? -1 : a.stars;
    const kb = b.stars === undefined ? -1 : b.stars;
    if (ka !== kb) return kb - ka;
    return a.name.localeCompare(b.name, undefined, {sensitivity: 'base'}) || (a.id ?? '').localeCompare(b.id ?? '');
}

/** `1.2k` from 1000 on, the plain number below; one decimal under ten thousand, no trailing `.0` */
export function formatStars(n: number): string {
    if (n < 1000) return String(n);
    const k = n / 1000;
    const s = k < 10 ? (Math.round(k * 10) / 10).toFixed(1).replace(/\.0$/, '') : String(Math.round(k));
    return `${s}k`;
}

/** the repository page: the entry's `git` URL, or GitHub by the release's owner/repo */
export function repoURL(e: CatalogLike): string {
    if (e.git && httpURL(e.git)) return e.git;
    if (e.release?.github) return `https://github.com/${e.release.github}`;
    return '';
}

/**
 * True for an absolute http(s) URL and nothing else. The catalogue's `git` and an addon's
 * own update check both hand the page a URL that ends up in an `href`; a `javascript:` there
 * would be a script that runs on a click.
 */
export function httpURL(u: string | undefined): boolean {
    return !!u && /^https?:\/\/[^\s]+$/i.test(u);
}

/**
 * The addons with an update waiting, each once (task 56): the catalogue's `update_available`
 * (set by the box only for an installed addon, under the addon's own id) and the addons' own
 * `Update:` checks (the daily run of /addons/updates). RedMatic is in both on a box where it
 * is installed from the catalogue and brings its own check; it is one update, not two.
 */
export function pendingUpdates(
    catalogue: {id: string; update_available?: boolean}[],
    checks: {id: string; info?: {update_available?: boolean}}[],
): string[] {
    const ids = new Set<string>();
    for (const e of catalogue) if (e.update_available) ids.add(e.id);
    for (const c of checks) if (c.info?.update_available) ids.add(c.id);
    return [...ids].sort();
}

/**
 * B-21 (maintainer, 2026-09-26): a catalogue install that cannot read the addon's release list
 * refuses and installs nothing, and the page says why. The box names it with a code - on a failed
 * run's progress (`error`, `retry_minutes`) and on the last check (`releases_error`) - and this is
 * which sentence `ReleasesProblem.svelte` shows for it: the rate limit with GitHub's wait, without
 * one, or no answer; '' for a code this page does not know (the box's English message stands then).
 */
export type ReleasesProblemKind = 'rate-limit-wait' | 'rate-limit' | 'unreachable' | '';

export function releasesProblemKind(code: string | undefined, minutes: number | undefined): ReleasesProblemKind {
    if (code === 'github-rate-limit') return minutes && minutes > 0 ? 'rate-limit-wait' : 'rate-limit';
    if (code === 'releases-unreachable') return 'unreachable';
    return '';
}
