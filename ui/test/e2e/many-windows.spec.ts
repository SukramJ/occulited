import {execFileSync} from 'node:child_process';
import {mkdtempSync, readFileSync, rmSync} from 'node:fs';
import http from 'node:http';
import http2 from 'node:http2';
import {tmpdir} from 'node:os';
import path from 'node:path';
import type {AddressInfo} from 'node:net';
import type {Browser, BrowserContext, Page} from '@playwright/test';
import {expect, test} from './fixtures';

// occulited B-53: each window of the shell held three long-lived streams (the addons' revision, the
// service messages, and the pairing requests on the Status page or lite-rpc's events in the Control
// app). A browser gives plain HTTP six connections per host across all its windows, so a third
// window did not load at all; over https lighttpd's HTTP/2 allows eight streams at once on the
// browser's one connection (a fixed SETTINGS_MAX_CONCURRENT_STREAMS), and a fourth window's page
// waited behind the nine streams. Now the browser holds one shell stream for all its windows (a
// SharedWorker), the App's lite-rpc stream is the shown App windows' alone, and a window over the
// account's stream limit says so.
//
// The windows are pages of one browser context - one browser profile, one connection pool. Every
// page of a headless browser counts as shown, the worst case.

const WINDOWS = 6;
const APP = '/app/e/rooms/og/bad';
const notLive = (page: Page) => page.locator('[data-app-title] [data-app-notlive]');
const lampState = (page: Page) => page.locator('[data-app-tile="HmIP-RF.000DD8:3"] [data-app-state]');

test.describe.configure({mode: 'serial'});
// the windows of one test open one after another; six of them and a TLS front take their time
test.setTimeout(120_000);
// the connections are the browser's, not the theme's or the viewport's: one project is enough
test.beforeEach(({}, info) => test.skip(info.project.name !== 'desktop-light', 'the connection pool is the same in every project'));

async function ownStub(context: BrowserContext, origin: string): Promise<string> {
    const id = `win-${Math.random().toString(36).slice(2)}`;
    // stub-lite-limit: the account's lite-rpc streams, three as on the system (rpc.streams_per_session)
    await context.addCookies(Object.entries({'stub-shellstream': id, 'stub-app': id, 'stub-lite-limit': `${id}.3`}).map(([name, value]) => ({name, value, url: origin})));
    return id;
}

async function shellStreams(base: string, id: string): Promise<{topics: string; open: boolean}[]> {
    const r = await fetch(`${base}/__stub/shellstream?id=${id}`);
    return (await r.json()) as {topics: string; open: boolean}[];
}

/** Opens the windows one after another; each must load within the page's own time. */
async function openWindows(context: BrowserContext, origin: string, path: string, ready: (p: Page) => Promise<void>): Promise<Page[]> {
    const pages: Page[] = [];
    for (let i = 0; i < WINDOWS; i++) {
        const p = await context.newPage();
        await p.goto(`${origin}${path}`, {timeout: 15_000});
        await ready(p);
        pages.push(p);
    }
    return pages;
}

async function appWindows(context: BrowserContext, origin: string, stubBase: string, id: string) {
    const pages = await openWindows(context, origin, APP, async (p) => {
        await expect(lampState(p)).toHaveText('On · 60 %', {timeout: 10_000});
    });
    // one shell stream for the browser: the addons for the shell, the service messages for the App
    await expect.poll(async () => (await shellStreams(stubBase, id)).filter((s) => s.open).map((s) => s.topics)).toEqual(['addons,service-messages']);
    // three windows live; the others say why they are not, and keep trying
    await expect.poll(async () => (await Promise.all(pages.map((p) => notLive(p).count()))).filter((n) => n === 0).length, {timeout: 15_000}).toBe(3);
    for (const p of pages) {
        if (await notLive(p).count()) await expect(notLive(p)).toHaveText('not live: too many windows');
    }
    // a window closes: a waiting one takes its stream
    const live = [];
    for (const p of pages) if ((await notLive(p).count()) === 0) live.push(p);
    await live[0]!.close();
    const rest = pages.filter((p) => p !== live[0]);
    await expect.poll(async () => (await Promise.all(rest.map((p) => notLive(p).count()))).filter((n) => n === 0).length, {timeout: 45_000}).toBe(3);
    // and a page of the shell elsewhere still loads at once
    const p = await context.newPage();
    await p.goto(`${origin}/`, {timeout: 15_000});
    await expect(p.locator('nav.ol-nav')).toBeVisible();
    return pages;
}

test('plain http: six windows of the Control app all load; three are live, the others say "too many windows"', async ({browser, baseURL}) => {
    const context = await browser.newContext();
    try {
        const id = await ownStub(context, baseURL!);
        await appWindows(context, baseURL!, baseURL!, id);
    } finally {
        await context.close();
    }
});

test('plain http: six windows of the Status page all load, with one stream for all of them', async ({browser, baseURL}) => {
    const context = await browser.newContext();
    try {
        const id = await ownStub(context, baseURL!);
        await openWindows(context, baseURL!, '/', async (p) => {
            await expect(p.locator('nav.ol-nav')).toBeVisible();
        });
        await expect.poll(async () => (await shellStreams(baseURL!, id)).filter((s) => s.open).map((s) => s.topics)).toEqual(['addons,pairing,service-messages']);
    } finally {
        await context.close();
    }
});

test('a page of the shell other than the App holds no lite-rpc stream', async ({browser, baseURL}) => {
    const context = await browser.newContext();
    try {
        await ownStub(context, baseURL!);
        const page = await context.newPage();
        const rpc: string[] = [];
        page.on('request', (r) => new URL(r.url()).pathname === '/api/rpc/v1/events' && rpc.push(r.url()));
        await page.goto('/');
        await expect(page.locator('nav.ol-nav')).toBeVisible();
        await page.waitForTimeout(1000);
        expect(rpc).toEqual([]);
    } finally {
        await context.close();
    }
});

// --- https: an HTTP/2 front like lighttpd's, eight streams at once per connection ---

/** A TLS HTTP/2 server with lighttpd's limit, passing every request on to the stub. */
async function h2Front(stub: string): Promise<{origin: string; close: () => Promise<void>} | null> {
    const dir = mkdtempSync(path.join(tmpdir(), 'b53-'));
    try {
        execFileSync('openssl', ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes', '-subj', '/CN=127.0.0.1', '-days', '1', '-keyout', path.join(dir, 'k.pem'), '-out', path.join(dir, 'c.pem')], {stdio: 'ignore'});
    } catch {
        rmSync(dir, {recursive: true, force: true});
        return null; // no openssl here: the test says so and is skipped
    }
    const key = readFileSync(path.join(dir, 'k.pem'));
    const cert = readFileSync(path.join(dir, 'c.pem'));
    rmSync(dir, {recursive: true, force: true});
    const target = new URL(stub);
    const server = http2.createSecureServer({key, cert, allowHTTP1: false, settings: {maxConcurrentStreams: 8}}, (req, res) => {
        const headers: http.OutgoingHttpHeaders = {};
        for (const [k, v] of Object.entries(req.headers)) if (!k.startsWith(':')) headers[k] = v;
        headers.host = target.host;
        const up = http.request({host: target.hostname, port: target.port, method: req.method, path: req.url, headers}, (r) => {
            const out: http.OutgoingHttpHeaders = {};
            for (const [k, v] of Object.entries(r.headers)) if (!['connection', 'keep-alive', 'transfer-encoding'].includes(k)) out[k] = v;
            res.writeHead(r.statusCode ?? 502, out);
            r.on('data', (c) => res.write(c));
            r.on('end', () => res.end());
        });
        up.on('error', () => res.headersSent || res.writeHead(502).end());
        res.on('close', () => up.destroy());
        req.pipe(up);
    });
    const sessions = new Set<http2.ServerHttp2Session>();
    server.on('session', (s) => {
        sessions.add(s);
        s.on('close', () => sessions.delete(s));
    });
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = (server.address() as AddressInfo).port;
    return {
        origin: `https://127.0.0.1:${port}`,
        close: () =>
            new Promise<void>((resolve) => {
                for (const s of sessions) s.destroy();
                server.close(() => resolve());
            }),
    };
}

async function httpsContext(browser: Browser) {
    return browser.newContext({ignoreHTTPSErrors: true});
}

test('https behind an HTTP/2 front with eight streams: six windows of the Control app all load', async ({browser, baseURL}) => {
    const front = await h2Front(baseURL!);
    test.skip(!front, 'no openssl for the TLS front');
    const context = await httpsContext(browser);
    try {
        const id = await ownStub(context, front!.origin);
        const pages = await appWindows(context, front!.origin, baseURL!, id);
        expect(await pages.at(-1)!.evaluate(() => performance.getEntriesByType('navigation').map((e) => (e as PerformanceNavigationTiming).nextHopProtocol))).toEqual(['h2']);
    } finally {
        await context.close();
        await front!.close();
    }
});

test('https behind an HTTP/2 front with eight streams: six windows of the Status page all load', async ({browser, baseURL}) => {
    const front = await h2Front(baseURL!);
    test.skip(!front, 'no openssl for the TLS front');
    const context = await httpsContext(browser);
    try {
        const id = await ownStub(context, front!.origin);
        await openWindows(context, front!.origin, '/', async (p) => {
            await expect(p.locator('nav.ol-nav')).toBeVisible();
        });
        await expect.poll(async () => (await shellStreams(baseURL!, id)).filter((s) => s.open).length).toBe(1);
    } finally {
        await context.close();
        await front!.close();
    }
});
