import {mount} from 'svelte';
import App from './App.svelte';
import FramedNotice from './lib/FramedNotice.svelte';
import {framedByShell} from './lib/framed';
import {applyTheme} from './lib/theme.svelte';
import {i18n} from './lib/i18n.svelte';

// A ?ticket= in the URL (task 125): the confirm link of a network change points at the new address,
// where no cookie exists yet, and carries a one-time session ticket instead of the session. It is
// exchanged here, before anything is requested, for a session on this host - the box sets the
// cookie and answers the id, which becomes the shell's Bearer (api.ts) - and removed from the URL.
async function handOver(): Promise<void> {
    const params = new URLSearchParams(location.search);
    const ticket = params.get('ticket');
    if (!ticket) return;
    params.delete('ticket');
    const q = params.toString();
    history.replaceState(null, '', location.pathname + (q ? '?' + q : ''));
    try {
        const res = await fetch('/api/auth/v1/ticket/redeem', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ticket})});
        if (!res.ok) return;
        const r = (await res.json()) as {sid?: string};
        if (r.sid) sessionStorage.setItem('ol.sid', r.sid);
    } catch {
        /* the login page follows */
    }
}

// task 193: the App is installable. The service worker caches the shell under the build's version
// and never an API answer (src/sw.js); when a new build's worker takes over, the page reloads once
// so nobody runs yesterday's UI against today's system.
function registerWorker(): void {
    if (!import.meta.env.PROD || !('serviceWorker' in navigator) || !window.isSecureContext) return;
    const had = !!navigator.serviceWorker.controller;
    let reloaded = false;
    navigator.serviceWorker.addEventListener('controllerchange', () => {
        if (!had || reloaded) return;
        reloaded = true;
        location.reload();
    });
    navigator.serviceWorker.register('/sw.js').catch(() => undefined);
}

void handOver().then(() => {
    applyTheme();
    registerWorker();
    document.documentElement.lang = i18n.language;
    // B-132: an addon that sent its frame back to the box's own page would otherwise draw a second shell
    // inside the first, and a third inside that. A framed shell shows a notice instead of the app.
    mount(framedByShell() ? FramedNotice : App, {target: document.getElementById('app')!});
});
