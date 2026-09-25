// A small path router: the shell owns the URL (D-18), the Go side serves index.html for any path.
// `path` is the pathname alone and `search` the query string, kept apart on purpose: every route
// test in the shell is an `onPage` (lib/routes.ts) or an `===` on the path, and a query string
// glued onto it would break them. A page that takes a parameter (the Log page's unit, 27.5) reads
// `search`.
import {settingsAlias} from './routes';
import {sectionAlias, systemAlias, systemPageAt} from './systemmenu';

/** the system page this browser showed last, for `/system` alone (task 57) */
const LAST_SYSTEM = 'ol.system.last';
function lastSystemPage(): string {
    try {
        return localStorage.getItem(LAST_SYSTEM) ?? '';
    } catch {
        return '';
    }
}
function noteSystemPage(path: string): void {
    if (!systemPageAt(path)) return;
    try {
        localStorage.setItem(LAST_SYSTEM, path);
    } catch {
        /* no storage: /system opens Services */
    }
}

/**
 * The path a route is shown under. An old in-app path stands for its new one and is rewritten
 * before any page sees it: `/addons/<id>` is `/addon-settings/<id>` (B-81), and without the rewrite
 * the Installed addons page would mount for a moment on its way there; `/services`, `/log` and the
 * other paths the system pages had before task 57 (and the Interfaces and Metadata tabs before
 * task 131: `/radio`) are their `/system/<page>` paths, the Metadata page's (`/metadata`, `/names`,
 * `/system/metadata`) are the App's since task 193, and `/system`
 * alone is the system page this browser showed last.
 */
// task 193: the Metadata page is gone - the App is the editor of names, rooms, functions and
// favorites - and its paths (the tab's /metadata and /names, the System page's /system/metadata)
// open the App
function metadataAlias(pathname: string): string {
    return /^\/(metadata|names|system\/metadata)(\/|$)/.test(pathname) ? '/app' : '';
}

// task 139: the Catalogue is part of the Addons page; /catalog and its ?q= (kept by the caller)
// open it
function catalogAlias(pathname: string): string {
    return /^\/catalog(\/|$)/.test(pathname) ? '/addons' : '';
}

function canonical(pathname: string, hash: string): {path: string; hash: string} {
    // task 132: a section that moved away from the page its old path stands for (the Security
    // page's #https is the Certificate page's) - decided by the anchor, before the path's alias
    const moved = sectionAlias(pathname, hash);
    const p = moved?.path ?? (metadataAlias(pathname) || catalogAlias(pathname) || settingsAlias(pathname) || systemAlias(pathname, lastSystemPage()) || pathname);
    noteSystemPage(p);
    return {path: p, hash: moved?.hash ?? hash};
}

// The anchor goes along with the query: `/radio#security-key` (task 81's warning, a bookmark) is
// `/system/interfaces#security-key`, and the page scrolls to it once it has mounted (task 131).
const first = canonical(location.pathname, location.hash);
if (first.path !== location.pathname || first.hash !== location.hash) history.replaceState(history.state, '', first.path + location.search + first.hash);
export const router = $state({path: first.path, search: location.search});

/**
 * Navigates to a path, which may carry a query string and a #fragment. The fragment stays in the
 * URL for the page that mounts to read (task 81: the Status page's security-key warning opens
 * `/system/interfaces#security-key`); the router itself does not act on it.
 */
export function navigate(path: string): void {
    const url = new URL(path, location.href);
    const c = canonical(url.pathname, url.hash);
    if (c.path !== router.path || url.search !== router.search) {
        history.pushState(null, '', c.path + url.search + c.hash);
        router.path = c.path;
        router.search = url.search;
    }
}

/**
 * Replaces the current history entry with a path: for a route that only stands for another one
 * (a signed-in `/login`, an old path), so Back does not return to it.
 */
export function replace(path: string): void {
    const url = new URL(path, location.href);
    const c = canonical(url.pathname, url.hash);
    history.replaceState(null, '', c.path + url.search + c.hash);
    router.path = c.path;
    router.search = url.search;
}

window.addEventListener('popstate', () => {
    const c = canonical(location.pathname, location.hash);
    if (c.path !== location.pathname || c.hash !== location.hash) history.replaceState(history.state, '', c.path + location.search + c.hash);
    router.path = c.path;
    router.search = location.search;
});

/** Intercepts same-origin link clicks so the app does not reload. */
export function link(node: HTMLAnchorElement): {destroy(): void} {
    const onClick = (e: MouseEvent) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        if (node.target && node.target !== '_self') return;
        const url = new URL(node.href, location.href);
        if (url.origin !== location.origin) return;
        // a place on the page that is showing (the Status page's storage warning points at its
        // own panel): the browser's jump to the anchor, which the router has nothing to add to
        if (url.hash && url.pathname === location.pathname && url.search === location.search) return;
        e.preventDefault();
        navigate(url.pathname + url.search + url.hash);
    };
    node.addEventListener('click', onClick);
    return {destroy: () => node.removeEventListener('click', onClick)};
}
