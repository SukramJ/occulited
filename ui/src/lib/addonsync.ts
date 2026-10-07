/*
 * openccu-lite B-297: the shell reads the nav and addon lists at sign-in, so an addon uninstalled
 * or installed stayed (or stayed missing) in the Addons menu and the pinned tabs until a reload.
 * Two things tell it to read them again:
 *
 * - addonsChanged(): a page that changed the addons says so the moment its work is done - an
 *   uninstall, an install's end, enable and disable on the Addons page;
 * - watchAddons(): the system's addon revision (the topic addons of the shell's stream, occulited
 *   B-53; on its own GET /api/system/v1/addons/stream before), for what other tabs, other devices
 *   and the console change. A revision other than the one seen before means "read again". The
 *   shell's stream is one for every window of the browser and is held only while a window is shown
 *   (a browser gives plain HTTP six connections per host across all its windows); on a window's
 *   return the first event says whether anything moved while it was hidden.
 */

import {subscribeShell, type Topic} from './shellstream/client';

type Listener = () => void;

const listeners = new Set<Listener>();

/** the addons changed here: the shell reads its menu again */
export function addonsChanged(): void {
    for (const f of [...listeners]) f();
}

/** what watchAddons subscribes with; a test passes its own */
export type Subscribe = (topic: Topic, f: (data: string) => void) => () => void;

/**
 * The shell's: `f` runs on addonsChanged() and whenever the system's addon revision moves.
 * Returns the function that stops it.
 */
export function watchAddons(f: Listener, subscribe: Subscribe = subscribeShell): () => void {
    listeners.add(f);
    let seen: number | null = null;
    const stop = subscribe('addons', (data) => {
        let rev: unknown;
        try {
            rev = JSON.parse(data)?.revision;
        } catch {
            return;
        }
        if (typeof rev !== 'number') return;
        const changed = seen !== null && rev !== seen;
        seen = rev;
        if (changed) f();
    });
    return () => {
        listeners.delete(f);
        stop();
    };
}
