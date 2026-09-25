// Light and dark, following the system by default with a persisted manual switch (D-12, D-22).
// The chosen theme is also broadcast to embedded addon frames (postMessage, same origin) so an
// embedded homematic-manager can follow it (D-21).
export type Theme = 'system' | 'light' | 'dark';

function initial(): Theme {
    try {
        const s = localStorage.getItem('ol.theme');
        if (s === 'light' || s === 'dark' || s === 'system') return s;
    } catch {
        /* ignore */
    }
    return 'system';
}

export const theme = $state({value: initial()});

// 28.5: besides the frame URL and postMessage, the choice is a cookie on the origin, for addon
// pages that are rendered on the server and never see either (the embedding contract in
// docs/system-api.md). 'system' is resolved for them: a CGI cannot ask prefers-color-scheme.
export function resolvedTheme(): 'light' | 'dark' {
    if (theme.value !== 'system') return theme.value;
    try {
        return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    } catch {
        return 'light';
    }
}
export function setCookie(name: string, value: string): void {
    try {
        document.cookie = `${name}=${encodeURIComponent(value)}; path=/; max-age=31536000; SameSite=Lax`;
    } catch {
        /* ignore */
    }
}

// 28.5, the Node-RED half: since 5.0.6 an embedded editor asks its parent for the look with
// {type: "request-theme"} and takes {type: "set-theme", payload: {theme: "auto"|"light"|"dark"}}
// (its own "view-dark-theme" setting). The shell's "system" is its "auto".
export function nodeRedTheme(): 'auto' | 'light' | 'dark' {
    return theme.value === 'system' ? 'auto' : theme.value;
}

// Everything a framed page may want to hear: the shell's own message (the embedding contract in
// docs/system-api.md) and Node-RED's.
export function postLook(win: Window | null | undefined): void {
    if (!win) return;
    win.postMessage({type: 'openccu-lite:theme', theme: theme.value, lang: document.documentElement.lang}, location.origin);
    win.postMessage({type: 'set-theme', payload: {theme: nodeRedTheme()}}, location.origin);
}

// Answers a framed page's request-theme; installed once by the shell. Same origin only, and
// only to a window that is one of our frames.
let answering = false;
export function answerThemeRequests(): void {
    if (answering) return;
    answering = true;
    window.addEventListener('message', (e: MessageEvent) => {
        if (e.origin !== location.origin || !e.source || (e.data as {type?: string} | null)?.type !== 'request-theme') return;
        const frames = Array.from(document.querySelectorAll('iframe')).map((f) => f.contentWindow);
        if (!frames.includes(e.source as Window)) return;
        postLook(e.source as Window);
    });
}

export function applyTheme(): void {
    const root = document.documentElement;
    if (theme.value === 'system') root.removeAttribute('data-theme');
    else root.setAttribute('data-theme', theme.value);
    setCookie('ol-theme', resolvedTheme());
    for (const frame of Array.from(document.querySelectorAll('iframe'))) postLook(frame.contentWindow);
}

export function setTheme(v: Theme): void {
    theme.value = v;
    try {
        localStorage.setItem('ol.theme', v);
    } catch {
        /* ignore */
    }
    applyTheme();
}

export function cycleTheme(): void {
    setTheme(theme.value === 'system' ? 'light' : theme.value === 'light' ? 'dark' : 'system');
}
