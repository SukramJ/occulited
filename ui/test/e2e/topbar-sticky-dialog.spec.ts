import {expect, test, type Page} from '@playwright/test';
import {scrollBy, scrollY, scrollbarWidth} from './scroll';

// Task 99 with B-93/B-100: a dialog over a scrolled page. The scroll lock (lib/ModalDialog.svelte) pads
// the scrolling box by its scrollbar's width while a dialog is open; the top bar stays at the window's
// top, keeps its buttons where they were, and the page stays where it was scrolled - open and after
// Escape. B-129: the box is `.ol-scrollport`, and the bar is outside it, so the bar ends at the window's
// right edge either way - open and closed - instead of at the scrollbar.
//
// Like dialog-shift.spec.ts and topbar-edge.spec.ts this launches Chromium without --hide-scrollbars (for
// the whole file: Playwright does not allow it per group); where the engine draws overlay scrollbars
// there is no strip and the test skips.
test.use({launchOptions: {ignoreDefaultArgs: ['--hide-scrollbars']}});

function bar(page: Page) {
    return page.evaluate(() => {
        const r = document.querySelector('.ol-header')!.getBoundingClientRect();
        return {top: r.top, right: r.right};
    });
}

function rfdRow(page: Page) {
    return page.locator('table.ol-table').first().locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: /^rfd$/})});
}

test('a dialog over a scrolled page: the bar stays at the top and at the window edge, and nothing scrolls', async ({page, isMobile, browserName}) => {
    await page.addInitScript(() => {
        localStorage.setItem('ol.services.hideSystem', '0');
        localStorage.setItem('ol.services.hideOccu', '0');
    });
    await page.setViewportSize({width: 1440, height: 400});
    await page.goto('/system/services');
    await expect(rfdRow(page)).toHaveCount(1);
    const stop = rfdRow(page).locator('.ol-rowactions button:has(.ol-acttext:text-is("Stop"))');
    await stop.scrollIntoViewIfNeeded();
    await scrollBy(page, 60);
    const y = await scrollY(page);
    expect(y, 'the page is scrolled').toBeGreaterThan(0);
    const scrollbar = await scrollbarWidth(page);
    test.skip(scrollbar === 0 && (isMobile || browserName !== 'chromium'), `${isMobile ? 'the phone emulation' : browserName} draws overlay scrollbars here: nothing takes width`);
    expect(scrollbar, 'a classic scrollbar takes width').toBeGreaterThan(0);
    const power = await page.locator('.ol-powerbtn').evaluate((el) => el.getBoundingClientRect().x);

    await stop.click({force: true});
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    const open = await bar(page);
    expect(open.top).toBe(0);
    expect(open.right, 'the bar ends at the window edge').toBe(await page.evaluate(() => window.innerWidth));
    expect(await scrollY(page), 'the page stays where it was scrolled').toBe(y);
    expect(await page.locator('.ol-powerbtn').evaluate((el) => el.getBoundingClientRect().x), "the bar's buttons stay put").toBe(power);

    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    const closed = await bar(page);
    expect(closed.top).toBe(0);
    expect(closed.right, 'closed: the bar still ends at the window edge - the scrollbar is inside the port, below it').toBe(await page.evaluate(() => window.innerWidth));
    expect(await scrollY(page)).toBe(y);
});
