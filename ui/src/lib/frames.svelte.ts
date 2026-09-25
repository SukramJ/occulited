// Task 39: addon pages kept loaded for the session. Every switch to another page used to unmount
// the addon's iframe, so Node-RED's editor loaded from scratch on the way back - many seconds on a
// Pi. The shell now keeps a page it has shown (an addon's frontend, an addon's settings page, a
// nav.d page whose drop-in asks) in lib/FrameHost.svelte, shown or hidden by the route, and
// NavFrame/AddonFrame only hand their page over.
//
// The rules: at most MAX_KEPT frontends, the least recently shown one goes (framekeep.ts); an
// addon's settings page does not count towards them - it is shown in a frame here as well, and
// dropped as soon as the user leaves it (leaveFrames), so opening settings pages never pushes
// Node-RED out. A page goes when its addon changed (stopped, restarted, switched off, updated,
// uninstalled - the page would be stale anyway), on the ✕ in the addon dropdown, and all of them
// on logout.
//
// An addon's change is seen in the addon list, which the shell re-reads when the user leaves a page
// that operates addons (Services, Installed addons, the catalogue) and at most every 15 s when a kept
// page is shown again. The list runs every addon's rc.d `info`, so it is not polled.
import {api, type Addon} from './api';
import {compare, leaving, MAX_KEPT, overflow, addonState} from './framekeep';

export interface KeptFrame {
    key: string;
    src: string;
    title: string;
    /** the addon whose state the page follows; '' for a nav.d page */
    addon: string;
    /** a settings page that refuses embedding (X-Frame-Options) is a notice instead */
    detectBlocked: boolean;
    blocked: boolean;
    used: number;
    state: string;
}

export const frames = $state({list: [] as KeptFrame[]});

let uses = 0;
/** How fresh an addon list must be to give a newly kept page its state without asking again. */
const FRESH_MS = 30_000;
/** How often showing a kept page again may re-read the addon list. */
const THROTTLE_MS = 15_000;
let known: {at: number; addons: Addon[]} | null = null;

/** Keeps a page, or marks a kept one as shown now. */
export function keepFrame(f: Pick<KeptFrame, 'key' | 'src' | 'title' | 'addon' | 'detectBlocked'>): void {
    const have = frames.list.find((x) => x.key === f.key);
    if (have) {
        have.used = ++uses;
        return;
    }
    const fresh = known && Date.now() - known.at < FRESH_MS ? known.addons : null;
    const state = f.addon && fresh ? addonState(fresh.find((a) => a.id === f.addon)) : '';
    frames.list.push({...f, blocked: false, used: ++uses, state});
    for (const k of overflow(frames.list, MAX_KEPT, f.key)) closeFrame(k);
    if (f.addon && state === '') void recheck(true);
}

/** Marks a kept page as shown now. */
export function useFrame(key: string): void {
    const have = frames.list.find((x) => x.key === key);
    if (have) have.used = ++uses;
}

export function markBlocked(key: string): void {
    const have = frames.list.find((x) => x.key === key);
    if (have) have.blocked = true;
}

/** Drops one kept page. The others stay where they are in the list: an iframe that moves reloads. */
export function closeFrame(key: string): void {
    const i = frames.list.findIndex((x) => x.key === key);
    if (i >= 0) frames.list.splice(i, 1);
}

/** The route shows `active` ('' = a shell page): every settings page but that one goes. */
export function leaveFrames(active: string): void {
    for (const k of leaving(frames.list, active)) closeFrame(k);
}

/** Drops every kept page of an addon, its frontend and its settings page. */
export function closeAddon(id: string): void {
    for (const f of frames.list.filter((x) => x.addon === id)) closeFrame(f.key);
}

/** Drops everything: the logout. */
export function dropAll(): void {
    frames.list.splice(0);
    known = null;
}

/** An addon list the shell fetched anyway: kept pages of an addon that changed go. */
export function noteAddons(addons: Addon[]): void {
    known = {at: Date.now(), addons};
    const {drop, learn} = compare(frames.list, addons);
    for (const f of frames.list) {
        const s = learn[f.key];
        if (s) f.state = s;
    }
    for (const k of drop) closeFrame(k);
}

let running: Promise<void> | null = null;
let again = false;
let lastAt = 0;

/**
 * Re-reads the addon list for the kept pages of addons. `force` for a moment when an addon may just
 * have changed; otherwise at most every THROTTLE_MS. Nothing is asked while no addon page is kept.
 */
export function recheck(force: boolean): Promise<void> {
    if (!frames.list.some((f) => f.addon)) return Promise.resolve();
    if (running) {
        // a check already on its way may have left before the change: one more after it
        if (force) again = true;
        return running;
    }
    if (!force && Date.now() - lastAt < THROTTLE_MS) return Promise.resolve();
    running = (async () => {
        do {
            again = false;
            lastAt = Date.now();
            try {
                noteAddons((await api.get<{addons: Addon[]}>('/api/system/v1/addons')).addons);
            } catch {
                /* the next check */
            }
        } while (again);
        running = null;
    })();
    return running;
}
