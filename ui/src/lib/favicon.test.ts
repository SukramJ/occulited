import {describe, expect, it} from 'vitest';

import {acceptIconHref, faviconCacheKey, faviconFallback, faviconFromHtml, iconLinks, pickIconLink, resolveFavicon, type FaviconDeps, type FaviconPage} from './favicon';

// task 55: the favicon of an addon's frontend as the dropdown finds it
const PAGE = 'http://box.lan/addons/red/';

describe('iconLinks', () => {
    it('finds rel="icon" and rel="shortcut icon", in order, head only', () => {
        const html = `<!doctype html><html><head>
            <link rel="stylesheet" href="style.css">
            <link rel="shortcut icon" href="favicon.ico">
            <LINK REL="Icon" TYPE="image/png" SIZES="32x32" HREF='icons/32.png'>
            <link rel=icon href=bare.png sizes=16x16>
            <link rel="apple-touch-icon" href="touch.png">
            </head><body><link rel="icon" href="body.png"></body></html>`;
        expect(iconLinks(html)).toEqual([
            {href: 'favicon.ico', rel: 'shortcut icon', type: '', sizes: ''},
            {href: 'icons/32.png', rel: 'icon', type: 'image/png', sizes: '32x32'},
            {href: 'bare.png', rel: 'icon', type: '', sizes: '16x16'},
        ]);
    });

    it('reads Node-RED\'s template as it ships', () => {
        // @node-red/editor-client/templates/index.mst:25, with the default page.favicon filled in
        const html = '<html><head><title>Node-RED</title><link rel="icon" type="image/png" href="favicon.ico"></head><body></body></html>';
        expect(iconLinks(html)).toEqual([{href: 'favicon.ico', rel: 'icon', type: 'image/png', sizes: ''}]);
    });

    it('returns nothing for a page that declares none (homematic-manager today)', () => {
        expect(iconLinks('<html><head><title>hmm</title><link rel="stylesheet" href="a.css"></head></html>')).toEqual([]);
        expect(iconLinks('')).toEqual([]);
    });

    it('ignores a link without an href', () => {
        expect(iconLinks('<head><link rel="icon"></head>')).toEqual([]);
    });
});

describe('pickIconLink', () => {
    const ico = {href: 'favicon.ico', rel: 'shortcut icon', type: '', sizes: ''};
    const png16 = {href: '16.png', rel: 'icon', type: 'image/png', sizes: '16x16'};
    const png48 = {href: '48.png', rel: 'icon', type: 'image/png', sizes: '48x48'};
    const png64 = {href: '64.png', rel: 'icon', type: 'image/png', sizes: '64x64'};
    const multi = {href: 'multi.png', rel: 'icon', type: 'image/png', sizes: '16x16 32x32 96x96'};
    const svg = {href: 'icon.svg', rel: 'icon', type: 'image/svg+xml', sizes: 'any'};
    const svgByName = {href: 'icon.svg', rel: 'icon', type: '', sizes: ''};

    it('prefers SVG whatever its position', () => {
        expect(pickIconLink([ico, png64, svg])).toBe(svg);
        expect(pickIconLink([png64, svgByName])).toBe(svgByName);
    });
    it('then the largest declared size of at least 32', () => {
        expect(pickIconLink([ico, png16, png48, png64])).toBe(png64);
        expect(pickIconLink([png16, multi, png48])).toBe(multi);
    });
    it('then the first declared', () => {
        expect(pickIconLink([ico, png16])).toBe(ico);
        expect(pickIconLink([png16])).toBe(png16);
    });
    it('is undefined for no links', () => {
        expect(pickIconLink([])).toBeUndefined();
    });
});

describe('acceptIconHref', () => {
    it('resolves a relative path against the page', () => {
        expect(acceptIconHref('favicon.ico', PAGE, 'red')).toBe('/addons/red/favicon.ico');
        expect(acceptIconHref('./icons/32.png', PAGE, 'red')).toBe('/addons/red/icons/32.png');
        expect(acceptIconHref('icons/x.png?v=3', 'http://box.lan/addons/red/flows.html', 'red')).toBe('/addons/red/icons/x.png?v=3');
    });
    it('accepts an absolute path below the page\'s directory or the addon\'s own /addons/<id>/', () => {
        expect(acceptIconHref('/addons/red/favicon.png', PAGE, 'red')).toBe('/addons/red/favicon.png');
        // the frontend lives under another path than the addon's directory - both are its own
        expect(acceptIconHref('/addons/redmatic/favicon.png', 'http://box.lan/addons/red/', 'redmatic')).toBe('/addons/redmatic/favicon.png');
        expect(acceptIconHref('http://box.lan/addons/red/favicon.png', PAGE, 'red')).toBe('/addons/red/favicon.png');
    });
    it('refuses a path outside the addon, however it is spelled', () => {
        expect(acceptIconHref('/favicon.ico', PAGE, 'red')).toBe('');
        expect(acceptIconHref('/addons/mosquitto/x.png', PAGE, 'red')).toBe('');
        expect(acceptIconHref('../mosquitto/x.png', PAGE, 'red')).toBe('');
        expect(acceptIconHref('/addons/red/../../etc.png', PAGE, 'red')).toBe('');
        expect(acceptIconHref('/addons/redder/x.png', PAGE, 'red')).toBe('');
    });
    it('does not count the shell\'s root or the shared /addons/ as the page\'s own directory', () => {
        expect(acceptIconHref('/favicon.ico', 'http://box.lan/index.html', 'red')).toBe('');
        expect(acceptIconHref('/addons/red/x.png', 'http://box.lan/', 'red')).toBe('/addons/red/x.png');
        // a frontend path without its trailing slash lives "in" /addons/ - which is every addon's
        expect(acceptIconHref('/addons/mosquitto/x.png', 'http://box.lan/addons/red', 'red')).toBe('');
        expect(acceptIconHref('/addons/red/x.png', 'http://box.lan/addons/red', 'red')).toBe('/addons/red/x.png');
    });
    it('refuses a foreign host, javascript:, blob: and nonsense', () => {
        expect(acceptIconHref('https://example.org/addons/red/favicon.ico', PAGE, 'red')).toBe('');
        expect(acceptIconHref('//example.org/addons/red/favicon.ico', PAGE, 'red')).toBe('');
        expect(acceptIconHref('http://box.lan:8080/addons/red/favicon.ico', PAGE, 'red')).toBe('');
        expect(acceptIconHref('javascript:alert(1)', PAGE, 'red')).toBe('');
        expect(acceptIconHref('blob:http://box.lan/abc', PAGE, 'red')).toBe('');
        expect(acceptIconHref('', PAGE, 'red')).toBe('');
        expect(acceptIconHref('favicon.ico', 'not a url', 'red')).toBe('');
    });
    it('accepts data: images of the four types and nothing else', () => {
        const png = 'data:image/png;base64,iVBORw0KGgo=';
        expect(acceptIconHref(png, PAGE, 'red')).toBe(png);
        expect(acceptIconHref('data:image/svg+xml,<svg/>', PAGE, 'red')).toBe('data:image/svg+xml,<svg/>');
        expect(acceptIconHref('data:image/x-icon;base64,AAAB', PAGE, 'red')).toBe('data:image/x-icon;base64,AAAB');
        expect(acceptIconHref('data:image/gif;base64,R0lG', PAGE, 'red')).toBe('data:image/gif;base64,R0lG');
        expect(acceptIconHref('data:text/html,<script>', PAGE, 'red')).toBe('');
        expect(acceptIconHref('data:image/jpeg;base64,/9j/', PAGE, 'red')).toBe('');
        expect(acceptIconHref('data:image/svg+xmlx,<svg/>', PAGE, 'red')).toBe('');
    });
});

describe('faviconFromHtml', () => {
    it('is the chosen link, resolved and checked', () => {
        const html = '<head><link rel="icon" href="a.ico"><link rel="icon" type="image/svg+xml" href="icon.svg"></head>';
        expect(faviconFromHtml(html, PAGE, 'red')).toBe('/addons/red/icon.svg');
    });
    it('is empty when the chosen link is refused, rather than falling through to the next', () => {
        // a browser would take the SVG; the shell must not silently take the second best either
        const html = '<head><link rel="icon" type="image/svg+xml" href="https://cdn.example/icon.svg"><link rel="icon" href="a.ico"></head>';
        expect(faviconFromHtml(html, PAGE, 'red')).toBe('');
    });
    it('is empty when nothing is declared', () => {
        expect(faviconFromHtml('<head><title>x</title></head>', PAGE, 'red')).toBe('');
    });
});

describe('faviconFallback and the cache key', () => {
    it('is favicon.ico beside the page', () => {
        expect(faviconFallback(PAGE)).toBe('/addons/red/favicon.ico');
        expect(faviconFallback('http://box.lan/addons/red/flows.html?x=1')).toBe('/addons/red/favicon.ico');
        expect(faviconFallback('garbage')).toBe('');
    });
    it('names the addon and its version', () => {
        expect(faviconCacheKey('redmatic', '9.4.0')).toBe('ol.favicon.redmatic@9.4.0');
    });
});

// the browser half: fetch the frontend once, probe the icon, remember the answer per version
describe('resolveFavicon', () => {
    const ORIGIN = 'http://box.lan';
    const RED = {id: 'redmatic', version: '9.4.0', href: '/addons/red/'};
    const HMM = {id: 'hmm', version: '3.0.0', href: '/addons/hmm/'};
    const NODE_RED = '<html><head><title>Node-RED</title><link rel="icon" type="image/png" href="favicon.ico"></head><body></body></html>';

    function fakes(pages: Record<string, FaviconPage | Error>, loads: string[] = []) {
        const store = new Map<string, string>();
        const fetched: string[] = [];
        const probed: string[] = [];
        const deps: FaviconDeps = {
            storage: {getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v)},
            async fetchPage(url) {
                fetched.push(url);
                const p = pages[url];
                if (p === undefined) throw new Error(`nothing at ${url}`);
                if (p instanceof Error) throw p;
                return p;
            },
            async probe(src) {
                probed.push(src);
                return loads.includes(src);
            },
        };
        return {deps, store, fetched, probed};
    }
    const page = (path: string, html: string, extra: Partial<FaviconPage> = {}): FaviconPage => ({ok: true, url: ORIGIN + path, html, ...extra});

    it('takes the declared icon once it loads, caches it, and asks again for a new version', async () => {
        const f = fakes({'http://box.lan/addons/red/': page('/addons/red/', NODE_RED)}, ['/addons/red/favicon.ico']);
        expect(await resolveFavicon(RED, ORIGIN, f.deps)).toBe('/addons/red/favicon.ico');
        expect(f.store.get('ol.favicon.redmatic@9.4.0')).toBe('/addons/red/favicon.ico');
        expect(await resolveFavicon(RED, ORIGIN, f.deps)).toBe('/addons/red/favicon.ico');
        expect(f.fetched).toHaveLength(1);
        await resolveFavicon({...RED, version: '9.4.1'}, ORIGIN, f.deps);
        expect(f.fetched).toHaveLength(2);
    });

    it('tries favicon.ico beside the page when nothing is declared', async () => {
        const f = fakes({'http://box.lan/addons/hmm/': page('/addons/hmm/', '<html><head><title>hmm</title></head></html>')}, ['/addons/hmm/favicon.ico']);
        expect(await resolveFavicon(HMM, ORIGIN, f.deps)).toBe('/addons/hmm/favicon.ico');
    });

    it('remembers a frontend without an icon as the letter, and does not ask again', async () => {
        const f = fakes({'http://box.lan/addons/hmm/': page('/addons/hmm/', '<html><head><title>hmm</title></head></html>')});
        expect(await resolveFavicon(HMM, ORIGIN, f.deps)).toBe('');
        expect(f.probed).toEqual(['/addons/hmm/favicon.ico']);
        expect(f.store.get('ol.favicon.hmm@3.0.0')).toBe('');
        expect(await resolveFavicon(HMM, ORIGIN, f.deps)).toBe('');
        expect(f.fetched).toHaveLength(1);
    });

    it('is the letter when the declared icon does not load', async () => {
        const f = fakes({'http://box.lan/addons/red/': page('/addons/red/', NODE_RED)});
        expect(await resolveFavicon(RED, ORIGIN, f.deps)).toBe('');
        expect(f.probed).toEqual(['/addons/red/favicon.ico']);
    });

    it('does not fall back to favicon.ico when the declared icon is refused', async () => {
        const html = '<head><link rel="icon" href="https://cdn.example/icon.png"></head>';
        const f = fakes({'http://box.lan/addons/red/': page('/addons/red/', html)}, ['/addons/red/favicon.ico']);
        expect(await resolveFavicon(RED, ORIGIN, f.deps)).toBe('');
        expect(f.probed).toEqual([]);
    });

    it('resolves against the page it was redirected to, inside the addon', async () => {
        // `/addons/red` lives "in" /addons/, which is nobody's own: the addon's /addons/<id>/ is
        // what the redirected page and its icon are held to
        const f = fakes({'http://box.lan/addons/red': page('/addons/red/', '<head><link rel="icon" href="icons/red.svg"></head>')}, ['/addons/red/icons/red.svg']);
        expect(await resolveFavicon({id: 'red', version: '1', href: '/addons/red'}, ORIGIN, f.deps)).toBe('/addons/red/icons/red.svg');
        // the same redirect for an addon whose id is not the path: out of scope, nothing cached
        expect(await resolveFavicon({...RED, href: '/addons/red'}, ORIGIN, f.deps)).toBe('');
        expect(f.store.has('ol.favicon.redmatic@9.4.0')).toBe(false);
    });

    it('caches nothing that is not the addon\'s own answer', async () => {
        // a network error, an error status, and a redirect out of the addon (an expired session's login)
        for (const p of [new Error('offline'), page('/addons/red/', '', {ok: false}), page('/login', '<head><link rel="icon" href="/addons/red/favicon.ico"></head>')]) {
            const f = fakes({'http://box.lan/addons/red/': p}, ['/addons/red/favicon.ico']);
            expect(await resolveFavicon(RED, ORIGIN, f.deps)).toBe('');
            expect(f.store.size).toBe(0);
            expect(f.probed).toEqual([]);
        }
    });

    it('fetches nothing for a frontend on another origin', async () => {
        const f = fakes({});
        expect(await resolveFavicon({...RED, href: 'http://box.lan:1880/'}, ORIGIN, f.deps)).toBe('');
        expect(await resolveFavicon({...RED, href: 'https://elsewhere.example/red/'}, ORIGIN, f.deps)).toBe('');
        expect(f.fetched).toEqual([]);
    });

    it('works without storage and with a storage that throws', async () => {
        const f = fakes({'http://box.lan/addons/red/': page('/addons/red/', NODE_RED)}, ['/addons/red/favicon.ico']);
        expect(await resolveFavicon(RED, ORIGIN, {...f.deps, storage: null})).toBe('/addons/red/favicon.ico');
        const broken = {getItem: () => { throw new Error('denied'); }, setItem: () => { throw new Error('denied'); }};
        expect(await resolveFavicon(RED, ORIGIN, {...f.deps, storage: broken})).toBe('/addons/red/favicon.ico');
        expect(f.fetched).toHaveLength(2);
    });
});
