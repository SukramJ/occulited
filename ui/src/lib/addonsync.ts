/*
 * openccu-lite B-297: the shell reads the nav and addon lists at sign-in, so an addon uninstalled
 * or installed stayed (or stayed missing) in the Addons menu and the pinned tabs until a reload.
 * Two things tell it to read them again:
 *
 * - addonsChanged(): a page that changed the addons says so the moment its work is done - an
 *   uninstall, an install's end, enable and disable on the Addons page;
 * - watchAddons(): the system's addon revision (GET /api/system/v1/addons/stream), for what other
 *   tabs, other devices and the console change. A revision other than the one seen before means
 *   "read again". The stream is open only while the tab is visible - a browser gives plain HTTP
 *   six connections per host across all its tabs - and on its return the first event says
 *   whether anything moved while it was hidden.
 */

type Listener = () => void;

const listeners = new Set<Listener>();

/** the addons changed here: the shell reads its menu again */
export function addonsChanged(): void {
    for (const f of [...listeners]) f();
}

export const ADDONS_STREAM = '/api/system/v1/addons/stream';

/** what watchAddons needs of the page; a test passes its own */
export interface WatchEnv {
    doc: Pick<Document, 'visibilityState' | 'addEventListener' | 'removeEventListener'>;
    open: ((url: string) => EventSource) | null;
}

function browserEnv(): WatchEnv {
    return {doc: document, open: typeof EventSource === 'undefined' ? null : (url) => new EventSource(url)};
}

/**
 * The shell's: `f` runs on addonsChanged() and whenever the system's addon revision moves.
 * Returns the function that stops it.
 */
export function watchAddons(f: Listener, env: WatchEnv = browserEnv()): () => void {
    listeners.add(f);
    let es: EventSource | null = null;
    let seen: number | null = null;
    const close = () => {
        es?.close();
        es = null;
    };
    const open = () => {
        if (!env.open || env.doc.visibilityState === 'hidden') return;
        // a stream the browser gave up on (a 401 after the session ended, a 403) is opened anew
        if (es && es.readyState === 2) close();
        if (es) return;
        const s = env.open(ADDONS_STREAM);
        es = s;
        s.addEventListener('addons', (ev) => {
            let rev: unknown;
            try {
                rev = JSON.parse((ev as MessageEvent).data)?.revision;
            } catch {
                return;
            }
            if (typeof rev !== 'number') return;
            const changed = seen !== null && rev !== seen;
            seen = rev;
            if (changed) f();
        });
    };
    const onVisibility = () => (env.doc.visibilityState === 'hidden' ? close() : open());
    env.doc.addEventListener('visibilitychange', onVisibility);
    open();
    return () => {
        listeners.delete(f);
        env.doc.removeEventListener('visibilitychange', onVisibility);
        close();
    };
}
