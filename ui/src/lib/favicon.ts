/**
 * The favicon of an addon's web frontend, for the addon dropdown (task 55).
 *
 * The dropdown used to show the logo from the addon's `Info:` line (lib/addonicon.ts): a wordmark
 * drawn for the CCU WebUI's full-width panel, squeezed into a small box. The maintainer's call
 * (2026-09-11): the icon is what the frontend itself offers as its favicon, found the way a browser
 * tab finds it - the frontend's page is fetched once, same origin, in the user's own session, and
 * its `<link rel~="icon">` elements are read.
 *
 * The parser is pure string work, no DOM: vitest runs these in node, and a `<link>` in a `<head>`
 * is simple enough that a regular expression over the tags is honest (the HTML is the addon's own,
 * and this never renders it - it extracts one attribute and checks it). The fetching, probing and
 * caching in `resolveFavicon` take the browser as parameters for the same reason.
 *
 * What is accepted is deliberately narrow, the same shape `addonicon.ts` has for the logos: a
 * same-origin path below the frontend's own directory or below the addon's `/addons/<id>/`, or a
 * `data:image/…` of the four raster and vector types a favicon comes as. An absolute URL to another
 * host, `javascript:`, a path that climbs out of the addon and everything else is refused - the
 * `<img>` this ends up in must not become a way for one addon's page to fetch from elsewhere.
 */

export interface IconLink {
    href: string;
    rel: string;
    type: string;
    sizes: string;
}

/** The attributes of one tag, lower-cased names, quotes stripped; a bare attribute is ''. */
function attributes(tag: string): Record<string, string> {
    const out: Record<string, string> = {};
    const re = /([^\s"'<>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'<>`]+)))?/g;
    // skip the tag name
    const body = tag.replace(/^<\s*[a-zA-Z][^\s/>]*/, '');
    for (const m of body.matchAll(re)) out[m[1]!.toLowerCase()] = (m[2] ?? m[3] ?? m[4] ?? '').trim();
    return out;
}

/**
 * Every `<link>` whose `rel` carries the token `icon` (`rel~="icon"`: "icon", "shortcut icon",
 * "icon shortcut"; NOT "apple-touch-icon", which is another token), in document order. Only the
 * head is read when there is one - a favicon declared in the body is nobody's convention.
 */
export function iconLinks(html: string): IconLink[] {
    const headEnd = html.search(/<\/head\s*>/i);
    const head = headEnd >= 0 ? html.slice(0, headEnd) : html;
    const out: IconLink[] = [];
    for (const m of head.matchAll(/<link\b[^>]*>/gi)) {
        const a = attributes(m[0]);
        const rel = (a['rel'] ?? '').toLowerCase().split(/\s+/).filter(Boolean);
        if (!rel.includes('icon')) continue;
        if (!a['href']) continue;
        out.push({href: a['href'], rel: rel.join(' '), type: (a['type'] ?? '').toLowerCase(), sizes: (a['sizes'] ?? '').toLowerCase()});
    }
    return out;
}

/** The largest edge a `sizes` attribute names ("16x16 32x32" → 32); 0 when it names none. */
function largestSize(sizes: string): number {
    let best = 0;
    for (const m of sizes.matchAll(/(\d+)x(\d+)/g)) best = Math.max(best, Number(m[1]), Number(m[2]));
    return best;
}

function isSvg(l: IconLink): boolean {
    return l.type === 'image/svg+xml' || /\.svg(?:[?#]|$)/i.test(l.href) || /^data:image\/svg\+xml[;,]/i.test(l.href);
}

/**
 * The one link to use, as a browser would rank them for a tab: SVG first (it scales to any size),
 * then the largest declared size of at least 32 px, then the first declared. Empty when none.
 */
export function pickIconLink(links: IconLink[]): IconLink | undefined {
    if (links.length === 0) return undefined;
    const svg = links.find(isSvg);
    if (svg) return svg;
    let best: IconLink | undefined;
    let bestSize = 0;
    for (const l of links) {
        const s = largestSize(l.sizes);
        if (s >= 32 && s > bestSize) {
            best = l;
            bestSize = s;
        }
    }
    return best ?? links[0];
}

/** The directory the page lives in: `/addons/red/flows.html` → `/addons/red/`. */
function directoryOf(pathname: string): string {
    return pathname.slice(0, pathname.lastIndexOf('/') + 1);
}

/**
 * Resolves a favicon `href` against the page it was found on and returns what the `<img>` may
 * show: the same-origin path (with its query, without the origin) when it lies below the page's
 * directory or below `/addons/<id>/`, the `data:` URL itself when it is an image of an accepted
 * type, and '' for everything else. `page` is the absolute URL of the frontend's page.
 *
 * A page directory of `/` or `/addons/` does not count as the addon's own: the first is the
 * shell's, the second every addon's, and "below" either would accept any path of the box.
 */
export function acceptIconHref(href: string, page: string, addonId: string): string {
    const h = href.trim();
    if (h === '') return '';
    if (/^data:/i.test(h)) return /^data:image\/(png|svg\+xml|x-icon|gif)[;,]/i.test(h) ? h : '';
    let base: URL;
    let url: URL;
    try {
        base = new URL(page);
        url = new URL(h, base);
    } catch {
        return '';
    }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return ''; // javascript:, blob:, …
    if (url.origin !== base.origin) return '';
    const below = [directoryOf(base.pathname), `/addons/${encodeURIComponent(addonId)}/`].filter((d) => d !== '/' && d !== '/addons/');
    // the URL parser has resolved every `..` already; a path that climbed out simply is not below
    if (!below.some((d) => url.pathname.startsWith(d))) return '';
    return url.pathname + url.search;
}

/**
 * The favicon an addon's frontend page declares, ready for an `<img src>`, or '' when it declares
 * none the shell may show. A declared icon that is refused is not replaced by the next one down
 * the ranking, nor by `favicon.ico`: the page said which icon it wants, and it is not this one.
 */
export function faviconFromHtml(html: string, page: string, addonId: string): string {
    const link = pickIconLink(iconLinks(html));
    return link ? acceptIconHref(link.href, page, addonId) : '';
}

/** `favicon.ico` beside the page, where a browser looks when nothing is declared. */
export function faviconFallback(page: string): string {
    try {
        return directoryOf(new URL(page).pathname) + 'favicon.ico';
    } catch {
        return '';
    }
}

/** The localStorage key the result is cached under: per addon and version, so an update refetches. */
export function faviconCacheKey(addonId: string, version: string): string {
    return `ol.favicon.${addonId}@${version}`;
}

// ---- finding it in the browser ----------------------------------------------------------------

/** What `resolveFavicon` needs of a fetched frontend page. `html` is '' when it was not HTML. */
export interface FaviconPage {
    ok: boolean;
    /** the URL the answer came from, after redirects */
    url: string;
    html: string;
}

/** The browser, as parameters: the unit tests hand in fakes, the shell `browserFaviconDeps()`. */
export interface FaviconDeps {
    fetchPage(url: string): Promise<FaviconPage>;
    /** whether an image at this src loads */
    probe(src: string): Promise<boolean>;
    storage: Pick<Storage, 'getItem' | 'setItem'> | null;
}

/**
 * The favicon of one addon's frontend, for an `<img src>`, or '' for the letter.
 *
 * - A cached answer for this addon *and version* is returned as it is, '' included: a frontend
 *   that has no favicon is not asked again on every shell load, and an update asks again.
 * - The page is fetched same origin only (a frontend on another origin cannot be read, and the
 *   shell does not try). A declared icon is taken when it is accepted; with none declared,
 *   `favicon.ico` beside the page. Either one must actually load before it is used.
 * - A failed fetch, an error status, or a redirect that led out of the addon - the login page of
 *   a session that just expired - is not cached: that is not the addon's answer, and it would
 *   stick until the next version.
 */
export async function resolveFavicon(addon: {id: string; version: string; href: string}, origin: string, deps: FaviconDeps): Promise<string> {
    const key = faviconCacheKey(addon.id, addon.version);
    try {
        const hit = deps.storage?.getItem(key);
        if (hit !== null && hit !== undefined) return hit;
    } catch {
        /* no storage: ask every time */
    }
    let page: URL;
    try {
        const base = new URL(origin + '/');
        page = new URL(addon.href, base);
        if (page.origin !== base.origin) return '';
    } catch {
        return '';
    }
    let res: FaviconPage;
    try {
        res = await deps.fetchPage(page.href);
    } catch {
        return '';
    }
    if (!res.ok) return '';
    const at = res.url || page.href;
    if (acceptIconHref(at, page.href, addon.id) === '') return '';
    const link = pickIconLink(iconLinks(res.html));
    const candidate = acceptIconHref(link ? link.href : faviconFallback(at), at, addon.id);
    let src = '';
    if (candidate) {
        try {
            src = (await deps.probe(candidate)) ? candidate : '';
        } catch {
            src = '';
        }
    }
    try {
        deps.storage?.setItem(key, src);
    } catch {
        /* full or blocked: the next load asks again, which is all it costs */
    }
    return src;
}

/** The real browser for `resolveFavicon`: fetch with a timeout, an `Image` probe, localStorage. */
export function browserFaviconDeps(): FaviconDeps {
    let storage: Storage | null = null;
    try {
        storage = window.localStorage;
    } catch {
        storage = null; // a browser that blocks storage throws on the property access itself
    }
    return {
        storage,
        async fetchPage(url: string): Promise<FaviconPage> {
            // a frontend that hangs (Node-RED still starting) must not hold the icon forever
            const ctl = new AbortController();
            const timer = setTimeout(() => ctl.abort(), 8000);
            try {
                const r = await fetch(url, {credentials: 'same-origin', signal: ctl.signal, headers: {Accept: 'text/html'}});
                const html = r.ok && /html/i.test(r.headers.get('content-type') ?? '') ? (await r.text()).slice(0, 256 * 1024) : '';
                return {ok: r.ok, url: r.url, html};
            } finally {
                clearTimeout(timer);
            }
        },
        probe(src: string): Promise<boolean> {
            return new Promise((resolve) => {
                const img = new Image();
                const done = (ok: boolean) => {
                    clearTimeout(timer);
                    img.onload = img.onerror = null;
                    resolve(ok);
                };
                const timer = setTimeout(() => done(false), 8000);
                // an HTML error page served as favicon.ico "loads" as nothing: no width, no icon
                img.onload = () => done(img.naturalWidth > 0);
                img.onerror = () => done(false);
                img.src = src;
            });
        },
    };
}
