// Task 59: the shell's per-account preferences - the addons pinned to the tab bar and the order
// of the addons with a web frontend. They are stored with the account on the box (D-54:
// GET/PUT /api/auth/v1/me/preferences), so a phone and a desktop show the same tab bar. With the
// login switched off there is no account to keep them with - the box answers 409 no-account -
// and the browser keeps them then.
import {api, ApiError} from './api';
import {auth} from './auth.svelte';
import {same, type AddonPref} from './addonorder';

/** task 193: where the UI opens for this account, and whether the App hides the shell's top bar;
 * openccu-lite task 306: whether Control's tab is out of the top bar (default: in it) */
export type StartPage = '' | 'app' | 'status';
export const prefs = $state({addons: [] as AddonPref[], startPage: '' as StartPage, appFullscreen: false, appHidden: false, loaded: false});

/** The start page in force: Control's, unless its tab is hidden - then Status (task 306). */
export function effectiveStartPage(p: {startPage: StartPage; appHidden: boolean}): 'app' | 'status' {
    return p.startPage === 'app' && !p.appHidden ? 'app' : 'status';
}

const KEY = 'ol.prefs';
const PATH = '/api/auth/v1/me/preferences';

function clean(list: unknown): AddonPref[] {
    if (!Array.isArray(list)) return [];
    return list.filter((p): p is {id: string; pinned?: boolean} => typeof p === 'object' && p !== null && typeof (p as {id?: unknown}).id === 'string').map((p) => ({id: p.id, pinned: p.pinned === true}));
}

type Stored = {addons?: unknown; start_page?: unknown; app_fullscreen?: unknown; app_hidden?: unknown};

function take(r: Stored | null): void {
    prefs.addons = clean(r?.addons);
    prefs.startPage = r?.start_page === 'app' || r?.start_page === 'status' ? r.start_page : '';
    prefs.appFullscreen = r?.app_fullscreen === true;
    prefs.appHidden = r?.app_hidden === true;
}

function fromBrowser(): Stored | null {
    try {
        return JSON.parse(localStorage.getItem(KEY) ?? 'null') as Stored | null;
    } catch {
        return null;
    }
}

/** the whole object, as a PUT replaces it (task 193: the App's choices travel with the pins) */
type Body = {addons: {id: string; pinned?: true}[]; start_page?: StartPage; app_fullscreen?: true; app_hidden?: true};
function body(): Body {
    const out: Body = {addons: prefs.addons.map((p) => (p.pinned ? {id: p.id, pinned: true} : {id: p.id}))};
    if (prefs.startPage) out.start_page = prefs.startPage;
    if (prefs.appFullscreen) out.app_fullscreen = true;
    if (prefs.appHidden) out.app_hidden = true;
    return out;
}

function toBrowser(): void {
    try {
        localStorage.setItem(KEY, JSON.stringify(body()));
    } catch {
        /* ignore */
    }
}

/** Reads the caller's set: from the box, or from the browser when the login is off. */
export async function loadPreferences(): Promise<void> {
    prefs.loaded = false;
    if (auth.authOff) {
        take(fromBrowser());
        prefs.loaded = true;
        return;
    }
    try {
        take(await api.get<Stored>(PATH));
    } catch {
        take(null);
    } finally {
        prefs.loaded = true;
    }
}

// the writes go out one after the other; a failure is logged and the shell keeps what it shows
let chain: Promise<unknown> = Promise.resolve();

/** Replaces the set of addon pins and order, in the shell at once and on the box after it. */
export function setAddonPreferences(list: readonly AddonPref[]): void {
    const next = list.map((p) => ({id: p.id, pinned: p.pinned}));
    if (same(prefs.addons, next)) return;
    prefs.addons = next;
    save();
}

/** task 193: the App's choices - the start page, the top bar hidden on the App; task 306: its tab hidden. */
export function setAppPreferences(p: {startPage?: StartPage; appFullscreen?: boolean; appHidden?: boolean}): void {
    if (p.startPage !== undefined) prefs.startPage = p.startPage;
    if (p.appFullscreen !== undefined) prefs.appFullscreen = p.appFullscreen;
    if (p.appHidden !== undefined) prefs.appHidden = p.appHidden;
    save();
}

function save(): void {
    if (auth.authOff) {
        toBrowser();
        return;
    }
    const b = body();
    chain = chain
        .then(() => api.put(PATH, b))
        .catch((e: unknown) => {
            // a session without an account: the browser keeps the choice
            if (e instanceof ApiError && e.code === 'no-account') toBrowser();
            else console.warn('preferences not saved', e);
        });
}

/** After a logout: nothing is remembered for the next login. */
export function resetPreferences(): void {
    prefs.addons = [];
    prefs.startPage = '';
    prefs.appFullscreen = false;
    prefs.appHidden = false;
    prefs.loaded = false;
}
