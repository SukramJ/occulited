// The script of lighttpd's waiting page (template.html next to this file; built into
// deploy/lighttpd/occulite-starting.html by scripts/starting-page.mjs). The page is served under
// the URL the browser asked for while occulited does not answer: it polls occulited's health route
// and reloads that URL once occulited is back.
//
// When the web interface started a reboot, an update's install or a restore, it left its countdown
// in localStorage (lib/bootbar.ts); this page is on the same origin, reads it and goes on with the
// same bar: lighttpd answering is the checkpoint it stands on, occulited answering the one it waits
// for. Without such an entry it shows the spinner only. It never shows diagnostics.
//
// openccu-lite task 283: without a countdown, lighttpd's 503 on the health route carries
// occulited's unit state (systemd's hooks keep it in /run), and the page says which case it is:
// restarting after a crash, a crash loop, stopped on purpose, the system going down.

import {BAR_KINDS, barView, bootText, classifyHealth, defaultExpect, EASE_MS, estimatedEnd, HINT_AFTER_MS, markSeen, observe, pickLanguage, recoveryHint, unitStateOf, unitStateView, type BootEntry, type UnitState} from '../lib/bootbar';
import {probeHealth, readEntry, writeEntry} from '../lib/bootwatch';

const FAST_MS = 2000;
const SLOW_MS = 10_000;
const BACKOFF_AFTER_MS = 60_000;

const byId = (id: string) => document.getElementById(id) as HTMLElement;

// S50lighttpd writes the values in; one it did not write is still the bare placeholder
const PLACEHOLDER = /^@[A-Z]+@$/;
function served(name: 'hostname' | 'version' | 'platform' | 'ip'): string {
    const v = (document.body.dataset[name] ?? '').trim();
    return PLACEHOLDER.test(v) ? '' : v;
}

function stored(key: string): string | null {
    try {
        return localStorage.getItem(key);
    } catch {
        return null;
    }
}

const lang = pickLanguage(stored('ol.language'), navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language]);
const root = document.documentElement;
root.lang = lang;
// the shell's own light/dark choice, where it made one; the system's otherwise
const theme = stored('ol.theme');
if (theme === 'light' || theme === 'dark') root.dataset.theme = theme;

const title = bootText('starting', lang);
document.title = title;
byId('title').textContent = title;
const ident = byId('ident');
ident.textContent = [served('hostname'), served('version')].filter(Boolean).join(' · ');
ident.hidden = ident.textContent === '';

const ip = served('ip');
const url = ip ? `http://${ip.includes(':') ? `[${ip}]` : ip}/` : '';

const loadedAt = Date.now();
// Without a countdown the page cannot know where in a boot it was opened, so the recovery hint
// waits for a whole reboot of this product (its built-in figures) and three minutes more.
const product = defaultExpect(served('platform'), 'reboot');
const hintWithoutEntryMs = (product.down + product.http + product.ui) * 1000 + HINT_AFTER_MS;

let entry: BootEntry | null = readEntry(loadedAt);
// a countdown only for a boot still waited for: not a recovery or a halt, not one already back
if (entry && (!BAR_KINDS.has(entry.kind) || entry.seen.ui !== undefined)) entry = null;
if (entry && entry.seen.down !== undefined) {
    // lighttpd served this page after the box was gone: the boot's lighttpd answers. Before that,
    // it may still be the shutdown's (lib/bootbar.ts, observe); the polls tell.
    entry = markSeen(entry, 'http', loadedAt);
    writeEntry(entry);
}

const bar = byId('bar');
const fill = byId('fill');
const text = byId('text');
const hint = byId('hint');
const stateLine = byId('state');
let unit: UnitState | null = null;
byId('spinner').hidden = entry !== null;
bar.hidden = text.hidden = entry === null;
bar.setAttribute('aria-label', bootText('barLabel', lang));

function showHint(s: string) {
    if (!s) {
        hint.hidden = true;
        return;
    }
    if (hint.dataset.text === s) return;
    hint.dataset.text = s;
    hint.textContent = '';
    const i = url ? s.indexOf(url) : -1;
    if (i < 0) {
        hint.textContent = s;
    } else {
        const a = document.createElement('a');
        a.href = a.textContent = url;
        hint.append(s.slice(0, i), a, s.slice(i + url.length));
    }
    hint.hidden = false;
}

function render() {
    const now = Date.now();
    if (!entry) {
        const v = unitStateView(unit, now, lang);
        const heading = v ? v.title : title;
        if (byId('title').textContent !== heading) {
            byId('title').textContent = heading;
            document.title = heading;
        }
        stateLine.textContent = v ? v.line : '';
        stateLine.hidden = !v;
        showHint((v && v.hint) || now - loadedAt >= hintWithoutEntryMs ? recoveryHint(url, lang) : '');
        return;
    }
    stateLine.hidden = true;
    const v = barView(entry, now, lang, url);
    fill.style.width = v.indeterminate ? '100%' : `${(v.fill * 100).toFixed(2)}%`;
    bar.classList.toggle('indeterminate', v.indeterminate);
    if (v.percent === null) bar.removeAttribute('aria-valuenow');
    else bar.setAttribute('aria-valuenow', String(v.percent));
    bar.setAttribute('aria-valuetext', v.text);
    text.textContent = v.text;
    showHint(v.hint);
}

async function poll() {
    const {status, body} = await probeHealth();
    const cls = classifyHealth(status, body);
    unit = cls === 'http' ? unitStateOf(body) : null;
    if (cls === 'ui') {
        if (!entry) {
            location.reload();
            return;
        }
        // the bar runs out, then the page the user asked for
        entry = markSeen(readEntry() ?? entry, 'ui', Date.now());
        writeEntry(entry);
        render();
        setTimeout(() => location.reload(), EASE_MS);
        return;
    }
    const now = Date.now();
    if (entry) {
        // the box going down and lighttpd coming back, when this page was opened before either
        const base = readEntry(now) ?? entry;
        entry = observe(base, cls, null, -1, now).entry;
        if (entry !== base) writeEntry(entry);
    }
    const fast = now - loadedAt < BACKOFF_AFTER_MS || (entry !== null && now < estimatedEnd(entry) + BACKOFF_AFTER_MS);
    setTimeout(poll, fast ? FAST_MS : SLOW_MS);
}

// under prefers-reduced-motion the bar jumps every few seconds instead of gliding
const reduced = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
render();
setInterval(render, reduced ? 3000 : 200);
setTimeout(poll, FAST_MS);
