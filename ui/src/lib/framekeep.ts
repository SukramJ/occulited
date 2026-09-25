// Task 39: the shell keeps addon pages loaded while the user is elsewhere - the pure half, which
// vitest runs: which kept page goes when one too many is open, which pages the shell keeps at all,
// and when a kept page has gone stale. The state is lib/frames.svelte.ts, the frames lib/FrameHost.svelte.
import type {Addon, NavEntry} from './api';

/**
 * At most this many frontends are kept: a Node-RED editor or a manager tab costs tens of MB each.
 * A settings page does not count: it is kept only while it is shown (`leaving`), so opening one
 * never pushes Node-RED out.
 */
export const MAX_KEPT = 3;

/** The key of an addon frontend's page (`/nav/<id>`), or of a nav.d page whose drop-in asks to be kept. */
export const frontendKey = (id: string): string => `nav:${id}`;
/** The key of an addon's settings page (`/addon-settings/<id>`). */
export const settingsKey = (id: string): string => `settings:${id}`;
/** Whether a key names a settings page rather than a frontend. */
export const isSettingsKey = (key: string): boolean => key.startsWith('settings:');

export interface Slot {
    key: string;
    /** when the page was last shown, as an increasing count */
    used: number;
}

/**
 * The frontends to drop so that at most `max` remain: the least recently shown first, never `keep`
 * (the page being opened). Settings pages are neither counted nor dropped here.
 */
export function overflow(slots: readonly Slot[], max: number, keep: string): string[] {
    const frontends = slots.filter((s) => !isSettingsKey(s.key));
    const excess = frontends.length - max;
    if (excess <= 0) return [];
    return frontends
        .filter((s) => s.key !== keep)
        .sort((a, b) => a.used - b.used)
        .slice(0, excess)
        .map((s) => s.key);
}

/** The settings pages to drop when `active` is shown: every one but `active` itself ('' = a shell page). */
export function leaving(slots: readonly Pick<Slot, 'key'>[], active: string): string[] {
    return slots.filter((s) => isSettingsKey(s.key) && s.key !== active).map((s) => s.key);
}

/**
 * Whether the shell keeps a nav entry's page loaded: an addon's frontend always; a `nav.d` page
 * only when its drop-in asks for it with `keep_alive` - an arbitrary page may be heavy or hostile;
 * a page that opens in a new tab never.
 */
export function keepable(e: Pick<NavEntry, 'source' | 'target'> & {keep_alive?: boolean}): boolean {
    return e.target !== 'blank' && (e.source === 'addon' || e.keep_alive === true);
}

/**
 * What a kept page of an addon depends on. When any of it changes - the addon was stopped or
 * started, restarted (a new pid, which the restart after a policy change gives as well), switched
 * off or updated - the page shows a stale state or a dead connection and is dropped; 'gone' is an
 * addon that is no longer installed.
 */
export function addonState(a: Pick<Addon, 'version' | 'running' | 'pid' | 'enabled'> | undefined): string {
    return a ? `${a.version}|${a.running ? 'up' : 'down'}|${a.pid ?? 0}|${a.enabled ? 'on' : 'off'}` : 'gone';
}

export interface Watched {
    key: string;
    /** the addon the page belongs to; '' for a nav.d page, which follows no addon */
    addon: string;
    /** addonState when the page was opened; '' while not known yet */
    state: string;
}

/**
 * Compares the kept pages with a fresh addon list: `drop` are the pages whose addon changed or is
 * gone, `learn` the states of pages that had none yet.
 */
export function compare(kept: readonly Watched[], addons: readonly Pick<Addon, 'id' | 'version' | 'running' | 'pid' | 'enabled'>[]): {drop: string[]; learn: Record<string, string>} {
    const byId = new Map(addons.map((a) => [a.id, a]));
    const drop: string[] = [];
    const learn: Record<string, string> = {};
    for (const k of kept) {
        if (!k.addon) continue;
        const now = addonState(byId.get(k.addon));
        if (now === 'gone' || (k.state !== '' && k.state !== now)) drop.push(k.key);
        else if (k.state === '') learn[k.key] = now;
    }
    return {drop, learn};
}
