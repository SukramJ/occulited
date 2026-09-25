import {expect, test, type Page} from '@playwright/test';

/*
 * B-159 (the maintainer, 2026-09-19): "the system menu opening animation (on desktop browser)
 * flashes a scrollbar inside for a short moment while it's growing". The panel's box grows out of
 * the tab; its item list is the flex child that scrolls, and a still-short panel squeezed it into a
 * scroller of its own. Now the content keeps its size while the box morphs. Stepped through the
 * open and the close: the list is never shorter than it is when the menu stands open, and never
 * scrolls when it does not scroll open either.
 */

/** the animations still running on a menu panel or its content (the page's own transitions do not count) */
function menuAnimations(): number {
    return document.getAnimations().filter((a) => {
        const t = (a.effect as KeyframeEffect | null)?.target;
        return t instanceof Element && t.closest('.ol-sysmenu') !== null;
    }).length;
}

interface Sample {
    at: number;
    client: number;
    scroll: number;
}

/*
 * Every animation the page starts from now on is held paused, so that the frames can be looked at
 * one by one: sampling with requestAnimationFrame depends on the engine's frame rate (WebKit in the
 * Playwright container gave two frames in the whole 200 ms).
 */
async function holdAnimations(page: Page): Promise<void> {
    await page.evaluate(() => {
        const w = window as unknown as {__b159held: Animation[]};
        w.__b159held = [];
        const own = Element.prototype.animate;
        Element.prototype.animate = function (this: Element, ...args: Parameters<Element['animate']>) {
            const a = own.apply(this, args);
            a.pause();
            w.__b159held.push(a);
            return a;
        };
    });
}

/** the held animations stepped through their length; the list measured at every step; then finished */
async function stepThrough(page: Page): Promise<Sample[]> {
    await expect.poll(() => page.evaluate(() => (window as unknown as {__b159held: Animation[]}).__b159held.length)).toBeGreaterThan(0);
    return page.evaluate(() => {
        const w = window as unknown as {__b159held: Animation[]};
        const held = w.__b159held;
        w.__b159held = [];
        const length = Math.max(...held.map((a) => Number(a.effect?.getComputedTiming().duration ?? 0)));
        const out: Sample[] = [];
        for (let at = 0; at < length; at += length / 10) {
            for (const a of held) a.currentTime = at;
            const list = document.querySelector('.ol-syslist') as HTMLElement;
            out.push({at: Math.round(at), client: list.clientHeight, scroll: list.scrollHeight});
        }
        for (const a of held) a.finish();
        return out;
    });
}

test('the System menu shows no scrollbar while it grows, and closes at once', async ({page, isMobile}) => {
    test.skip(isMobile, 'the phone sheet slides and does not change its size');
    await page.goto('/');
    const tab = page.locator('.ol-systab');
    await expect(tab).toBeVisible();
    await holdAnimations(page);
    const panel = page.locator('.ol-sysmenu');

    await tab.click();
    await expect.poll(() => panel.getAttribute('data-phase')).toBe('opening');
    const opening = await stepThrough(page);
    await expect.poll(() => panel.getAttribute('data-phase')).toBe('open');
    const open = await page.locator('.ol-syslist').evaluate((l) => ({client: l.clientHeight, scroll: l.scrollHeight}));
    expect(opening.length).toBeGreaterThan(5);
    for (const s of opening) {
        expect(s.client, `the list squeezed while opening: ${JSON.stringify(s)}`).toBeGreaterThanOrEqual(open.client - 1);
        if (open.scroll <= open.client) expect(s.scroll, `a scrollbar while opening: ${JSON.stringify(s)}`).toBeLessThanOrEqual(s.client);
    }

    // task 210: the close does not move - the panel is gone at once, nothing left running
    await page.keyboard.press('Escape');
    await expect(panel).toHaveCount(0);
    expect(await page.evaluate(menuAnimations)).toBe(0);

    // the list still scrolls where the window is too short for it
    await page.setViewportSize({width: 1000, height: 320});
    await tab.click();
    await stepThrough(page);
    await expect.poll(() => panel.getAttribute('data-phase')).toBe('open');
    const short = await page.locator('.ol-syslist').evaluate((l) => {
        l.scrollTop = 1000;
        return {client: l.clientHeight, scroll: l.scrollHeight, top: l.scrollTop};
    });
    expect(short.scroll).toBeGreaterThan(short.client);
    expect(short.top).toBeGreaterThan(0);
});

interface Frame {
    width: number;
    height: number;
    border: string;
}

/** the held animations stepped through; the panel's box and border colour at every step; then finished */
async function panelFrames(page: Page): Promise<Frame[]> {
    await expect.poll(() => page.evaluate(() => (window as unknown as {__b159held: Animation[]}).__b159held.length)).toBeGreaterThan(0);
    return page.evaluate(() => {
        const w = window as unknown as {__b159held: Animation[]};
        const held = w.__b159held;
        w.__b159held = [];
        const length = Math.max(...held.map((a) => Number(a.effect?.getComputedTiming().duration ?? 0)));
        const out: Frame[] = [];
        for (let at = 0; at < length; at += length / 10) {
            for (const a of held) a.currentTime = at;
            const p = document.querySelector('nav.ol-sysmenu') as HTMLElement;
            const r = p.getBoundingClientRect();
            out.push({width: r.width, height: r.height, border: getComputedStyle(p).borderTopColor});
        }
        for (const a of held) a.finish();
        return out;
    });
}

/*
 * Task 210 (the maintainer, 2026-09-23): "it seems that only the vertical grow is animated, not the
 * horizontal ... let the addon menu use the same animation! both animation show a bright border while
 * running, prevent that. addon also animates menu closing, remove that". Both menus grow out of their
 * tab in both axes, in the panel's own border colour throughout, and close without a motion.
 */
for (const menu of [{name: 'System', tab: '.ol-systab'}, {name: 'Addons', tab: '.ol-addonsbtn'}]) {
    test(`the ${menu.name} menu grows in both axes, keeps its border colour, and closes at once`, async ({page, isMobile}) => {
        test.skip(isMobile, 'the phone sheet slides and does not change its size');
        await page.goto('/');
        const tab = page.locator(menu.tab).first();
        await expect(tab).toBeVisible();
        await holdAnimations(page);
        const panel = page.locator('nav.ol-sysmenu');
        await tab.click();
        const frames = await panelFrames(page);
        await expect.poll(() => panel.getAttribute('data-phase')).toBe('open');
        const open = await panel.evaluate((p) => ({width: p.getBoundingClientRect().width, height: p.getBoundingClientRect().height, border: getComputedStyle(p).borderTopColor}));
        expect(frames.length).toBeGreaterThan(5);
        // the first frame is the tab's size, clearly narrower and lower than the open panel
        expect(frames[0].width, JSON.stringify(frames)).toBeLessThan(open.width * 0.6);
        expect(frames[0].height, JSON.stringify(frames)).toBeLessThan(open.height * 0.6);
        for (let i = 1; i < frames.length; i++) {
            expect(frames[i].width, JSON.stringify(frames)).toBeGreaterThanOrEqual(frames[i - 1].width - 0.5);
            expect(frames[i].height, JSON.stringify(frames)).toBeGreaterThanOrEqual(frames[i - 1].height - 0.5);
        }
        for (const f of frames) expect(f.border, 'the border colour while the menu grows').toBe(open.border);

        await page.keyboard.press('Escape');
        await expect(panel).toHaveCount(0);
        expect(await page.evaluate(menuAnimations)).toBe(0);
    });
}
