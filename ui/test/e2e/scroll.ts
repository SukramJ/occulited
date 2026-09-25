import type {Page} from '@playwright/test';

/*
 * B-129: the shell is a fixed-height layout. The top bar is a row of it that does not scroll and
 * `.ol-scrollport` - the box around `main` - is what scrolls, so the page's scroll bar begins at the
 * bar's lower edge instead of running the whole window height beside it.
 *
 * Every spec that used to drive or measure the *document's* scrolling asks the port through these
 * helpers. Two of them matter beyond convenience:
 *
 * - `pageWidth` is what the width guards (phone-width, services-layout and the per-page checks) have
 *   to use. A scroll container does not pass its horizontal overflow on to the document, so
 *   `documentElement.scrollWidth` alone would answer "the window's width" on every page and those
 *   guards would quietly stop guarding. The wider of the two is the page's real width.
 * - `scrollbarWidth` is the width the port's own scroll bar takes, which is what B-93's lock
 *   compensates; `window.innerWidth - documentElement.clientWidth` is 0 now, on a long page too.
 *
 * The Log page is the exception the helpers cover on their own: its port does not scroll (the
 * journal box inside it does), so its overflow still reaches the document.
 */
export const PORT = '.ol-scrollport';

const port = (page: Page) => page.locator(PORT);

/** how far the port is scrolled down; the old `window.scrollY` */
export const scrollY = (page: Page): Promise<number> => port(page).evaluate((el) => el.scrollTop);

/** scrolls the port to an offset; the old `window.scrollTo(0, top)` */
export async function scrollTo(page: Page, top: number): Promise<void> {
    await port(page).evaluate((el, y) => el.scrollTo({top: y}), top);
}

/** scrolls the port by an offset; the old `window.scrollBy(0, by)` */
export async function scrollBy(page: Page, by: number): Promise<void> {
    await port(page).evaluate((el, y) => el.scrollBy({top: y}), by);
}

/** whether the page is longer than the room it has */
export const pageScrolls = (page: Page): Promise<boolean> =>
    page.evaluate((s) => {
        const el = document.querySelector(s);
        const root = document.documentElement;
        return (!!el && el.scrollHeight > el.clientHeight + 1) || root.scrollHeight > root.clientHeight + 1;
    }, PORT);

/** how wide the page really is: the document's width, or the port's where it carries the overflow */
export const pageWidth = (page: Page): Promise<number> =>
    page.evaluate((s) => Math.max(document.documentElement.scrollWidth, document.querySelector(s)?.scrollWidth ?? 0), PORT);

/** the width the port's scroll bar takes; 0 with an overlay scroll bar or on a page that fits */
export const scrollbarWidth = (page: Page): Promise<number> =>
    page.evaluate((s) => {
        const el = document.querySelector<HTMLElement>(s);
        if (!el) return window.innerWidth - document.documentElement.clientWidth;
        return el.offsetWidth - el.clientWidth;
    }, PORT);

/** whether nothing on the page reaches past the window's right edge */
export async function fitsWindow(page: Page): Promise<boolean> {
    const [width, window_] = [await pageWidth(page), await page.evaluate(() => window.innerWidth)];
    return width <= window_;
}
