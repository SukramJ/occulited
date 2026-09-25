import {expect, test, type Page} from '@playwright/test';

// Task 177, as the maintainer settled it: the pages are kept once visited (as the addon frames), so a
// return is instant and uncovered; a page's first visit is covered while it builds up and fades in
// once nothing in it is loading any more (the Status page: its warnings too), not before 120 ms and
// not after 2 s. The menu stays uncovered and shows the addons of the last visit at once.

// the times (ms since the page's start) at which <main> was covered and uncovered
async function watchCover(page: Page) {
    await page.addInitScript(() => {
        const w = window as unknown as {__cover: {on: number[]; off: number[]}};
        w.__cover = {on: [], off: []};
        let was = false;
        new MutationObserver(() => {
            const main = document.querySelector('main.ol-main');
            const now = !!main?.classList.contains('ol-entering');
            if (main && now !== was) {
                (now ? w.__cover.on : w.__cover.off).push(performance.now());
                was = now;
            }
        }).observe(document, {subtree: true, childList: true, attributes: true, attributeFilter: ['class']});
    });
}
const cover = (page: Page) => page.evaluate(() => (window as unknown as {__cover: {on: number[]; off: number[]}}).__cover);

test('the first page is covered until it is ready, then fades in', async ({page}) => {
    await watchCover(page);
    await page.route('**/api/system/v1/services', async (route) => {
        await new Promise((r) => setTimeout(r, 700));
        await route.continue();
    });
    await page.goto('/system/services');
    await expect(page.locator('main.ol-main')).not.toHaveClass(/ol-entering/);
    await expect(page.locator('table.sv-table').first()).toBeVisible();
    const c = await cover(page);
    const shown = c.off[0]! - c.on[0]!;
    // the services answer after 700 ms: the cover waited for them, and no longer than the cap
    expect(shown).toBeGreaterThanOrEqual(650);
    expect(shown).toBeLessThan(2100);
    expect(await page.locator('main.ol-main').evaluate((m) => getComputedStyle(m).transitionProperty)).toContain('opacity');
});

test('the Status page fades in only once its warnings are there', async ({page}) => {
    await watchCover(page);
    await page.route('**/api/system/v1/warnings', async (route) => {
        await new Promise((r) => setTimeout(r, 900));
        await route.continue();
    });
    await page.goto('/');
    await expect(page.locator('main.ol-main')).not.toHaveClass(/ol-entering/);
    const c = await cover(page);
    expect(c.off[0]! - c.on[0]!).toBeGreaterThanOrEqual(850);
});

test('a page visited before comes back at once, as it was left, and refreshes quietly', async ({page}) => {
    await page.goto('/system/services');
    await expect(page.locator('table.sv-table').first()).toBeVisible();
    await page.getByPlaceholder('Filter').fill('rfd');
    await page.getByRole('link', {name: 'Status', exact: true}).first().click();
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    // back: no cover, the filter still typed, a fresh load of the list behind it
    const refreshed = page.waitForRequest((r) => new URL(r.url()).pathname === '/api/system/v1/services');
    const covered = page.evaluate(() => new Promise<boolean>((resolve) => {
        const main = document.querySelector('main.ol-main')!;
        new MutationObserver(() => {
            if (main.classList.contains('ol-entering')) resolve(true);
        }).observe(main, {attributes: true, attributeFilter: ['class']});
        setTimeout(() => resolve(false), 1000);
    }));
    await page.goBack();
    await refreshed;
    expect(await covered).toBe(false);
    await expect(page.getByPlaceholder('Filter')).toHaveValue('rfd');
    // the Status page is still there, hidden: one of each page in the DOM
    await expect(page.locator('.ol-keptpage[data-page="status"]')).toBeHidden();
    await expect(page.locator('.ol-keptpage[data-page="services"]')).toBeVisible();
});

test('a first visit through the menu is covered; the menu itself is not', async ({page}) => {
    await page.goto('/system/services');
    await expect(page.locator('main.ol-main')).not.toHaveClass(/ol-entering/);
    const seen = page.evaluate(() => new Promise<boolean>((resolve) => {
        const main = document.querySelector('main.ol-main')!;
        new MutationObserver(() => {
            if (main.classList.contains('ol-entering')) resolve(true);
        }).observe(main, {attributes: true, attributeFilter: ['class']});
        setTimeout(() => resolve(false), 3000);
    }));
    await page.getByRole('link', {name: 'Status', exact: true}).first().click();
    expect(await seen).toBe(true);
    await expect(page.locator('header').first()).not.toHaveClass(/ol-entering/);
    await expect(page.locator('main.ol-main')).not.toHaveClass(/ol-entering/);
});

test('reduced motion: covered, then shown without a fade', async ({page}) => {
    await page.emulateMedia({reducedMotion: 'reduce'});
    await page.goto('/system/services');
    await expect(page.locator('main.ol-main')).not.toHaveClass(/ol-entering/);
    expect(await page.locator('main.ol-main').evaluate((m) => getComputedStyle(m).transitionDuration)).toBe('0s');
});

test('a panel that loads on its own does not claim an empty state meanwhile', async ({page}) => {
    await page.route('**/api/system/v1/firewall/listeners', async (route) => {
        await new Promise((r) => setTimeout(r, 900));
        await route.continue();
    });
    await page.goto('/system/firewall');
    await expect(page.locator('table.fw-rules')).toBeVisible();
    await expect(page.getByText('Nothing is listening, or /proc is not readable.')).toHaveCount(0);
    await expect(page.locator('table.fw-listeners')).toBeVisible();
});

test('the menu shows the addons of the last visit at once', async ({page}) => {
    await page.goto('/');
    const menu = page.locator('.ol-menu').first();
    await menu.locator('.ol-menubtn').click();
    await expect(menu.getByRole('menu').locator('.ol-addonname', {hasText: /^RedMatic$/})).toBeVisible();
    await expect.poll(() => page.evaluate(() => localStorage.getItem('ol.menuCache') ?? '')).toContain('redmatic');
    // the next visit: the lists answer late, the menu has them already
    for (const p of ['**/api/system/v1/addons', '**/api/system/v1/nav']) {
        await page.route(p, async (route) => {
            await new Promise((r) => setTimeout(r, 3000));
            await route.continue();
        });
    }
    await page.reload();
    await menu.locator('.ol-menubtn').click();
    await expect(menu.getByRole('menu').locator('.ol-addonname', {hasText: /^RedMatic$/})).toBeVisible({timeout: 1500});
});

test('a hidden page does not poll', async ({page}) => {
    await page.goto('/system/firewall');
    await expect(page.locator('table.fw-rules')).toBeVisible();
    await page.getByRole('link', {name: 'Status', exact: true}).first().click();
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    let counters = 0;
    page.on('request', (r) => {
        if (new URL(r.url()).pathname === '/api/system/v1/firewall/counters') counters++;
    });
    // the counters poll every 5 s while the Firewall page shows
    await page.waitForTimeout(6000);
    expect(counters).toBe(0);
});

// the wait made visible: a light along the top bar's bottom edge, once, in the cover's 2 s
test('the top bar shows the wait of a first visit, and not a moment longer', async ({page}) => {
    await page.route('**/api/system/v1/services', async (route) => {
        await new Promise((r) => setTimeout(r, 900));
        await route.continue();
    });
    await page.goto('/system/services');
    const bar = page.locator('.ol-header .ol-loadbar');
    await expect(bar).toBeAttached();
    expect(await bar.evaluate((e) => getComputedStyle(e, '::before').animationDuration)).toBe('2s');
    await expect(page.locator('table.sv-table').first()).toBeVisible();
    await expect(bar).toHaveCount(0);
    // Status (a first visit), then back to Services, visited before: no bar on the way back
    await page.getByRole('link', {name: 'Status', exact: true}).first().click();
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    await expect(bar).toHaveCount(0);
    const appeared = page.evaluate(() => new Promise<boolean>((resolve) => {
        new MutationObserver(() => {
            if (document.querySelector('.ol-header .ol-loadbar')) resolve(true);
        }).observe(document.querySelector('.ol-header')!, {childList: true, subtree: true});
        setTimeout(() => resolve(false), 1000);
    }));
    await page.goBack();
    await expect(page.locator('table.sv-table').first()).toBeVisible();
    expect(await appeared).toBe(false);
});

test('reduced motion: the wait is a still line', async ({page}) => {
    await page.emulateMedia({reducedMotion: 'reduce'});
    await page.route('**/api/system/v1/services', async (route) => {
        await new Promise((r) => setTimeout(r, 900));
        await route.continue();
    });
    await page.goto('/system/services');
    const bar = page.locator('.ol-header .ol-loadbar');
    await expect(bar).toBeAttached();
    expect(await bar.evaluate((e) => getComputedStyle(e, '::before').animationName)).toBe('none');
});

// found by the maintainer: Homematic Manager's menu bar was under the top bar - the page area kept
// the scroll of the page before, and the addon frame, which fills the window, started scrolled
test('an addon page opens from the top, whatever the page before was scrolled to', async ({page}) => {
    await page.setViewportSize({width: 1000, height: 500});
    await page.goto('/');
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    await expect(page.locator('main.ol-main')).not.toHaveClass(/ol-entering/);
    await page.locator('.ol-scrollport').evaluate((e) => (e.scrollTop = 400));
    expect(await page.locator('.ol-scrollport').evaluate((e) => e.scrollTop)).toBeGreaterThan(0);
    const menu = page.locator('.ol-menu').first();
    await menu.locator('.ol-menubtn').click();
    await menu.getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: /^RedMatic$/})}).getByRole('menuitem').click();
    const frame = page.locator('iframe.ol-kept-frame[src^="/addons/red"]');
    await expect(frame).toBeVisible();
    await expect.poll(() => page.locator('.ol-scrollport').evaluate((e) => e.scrollTop)).toBe(0);
    const header = (await page.locator('.ol-header').boundingBox())!;
    const f = (await frame.boundingBox())!;
    expect(f.y).toBeGreaterThanOrEqual(header.y + header.height - 1);
});
