import {expect, test, type Page} from '@playwright/test';
import {scrollbarWidth} from './scroll';

// B-100 and B-129, the two halves of the same complaint about the top bar's right end.
//
// B-100: the bar stopped a few pixels short of the window's right edge, because B-93's fix had
// reserved the scrollbar's gutter on the root and every page kept a strip of the page ground beside
// the bar. The lock pads instead of reserving, so nothing is kept.
//
// B-129: while the *document* scrolled, the page's scrollbar still ran the full window height beside
// the bar, and the bar had to end where it began ("when page gets a scroller it spans over top bar.
// but top bar should be fixed position and scroller should begin underneath it"). The shell is one
// viewport tall now and `.ol-scrollport` - the box around `main` - is what scrolls. So the bar spans
// the whole window on every page, scrolling or not, with a dialog or without, and the scrollbar
// begins at the bar's lower edge.
//
// Like dialog-shift.spec.ts this launches Chromium without --hide-scrollbars; where the engine draws
// overlay scrollbars there is no strip at all, and the tests that need one skip.
test.use({launchOptions: {ignoreDefaultArgs: ['--hide-scrollbars']}});

function measure(page: Page) {
    return page.evaluate(() => {
        const root = document.documentElement;
        const header = document.querySelector('.ol-header')!.getBoundingClientRect();
        const port = document.querySelector('.ol-scrollport')!;
        const portBox = port.getBoundingClientRect();
        const backdrop = document.querySelector('.ol-backdrop')?.getBoundingClientRect();
        return {
            inner: window.innerWidth,
            client: root.clientWidth,
            scrolls: port.scrollHeight > port.clientHeight + 1,
            header: header.right,
            headerBottom: header.bottom,
            portTop: portBox.top,
            portRight: portBox.right,
            backdrop: backdrop ? {left: backdrop.left, right: backdrop.right} : null,
        };
    });
}

async function classicScrollbar(page: Page, isMobile: boolean, browserName: string) {
    const bar = await scrollbarWidth(page);
    test.skip(bar === 0 && (isMobile || browserName !== 'chromium'), `${isMobile ? 'the phone emulation' : browserName} draws overlay scrollbars here: nothing takes width`);
    expect(bar, 'a classic scrollbar takes width').toBeGreaterThan(0);
    return bar;
}

function rfdRow(page: Page) {
    return page.locator('table.ol-table').first().locator('tbody tr').filter({has: page.locator('.sv-id', {hasText: /^rfd$/})});
}

async function openServices(page: Page, width: number, height: number) {
    await page.addInitScript(() => {
        localStorage.setItem('ol.services.hideSystem', '1');
        localStorage.setItem('ol.services.hideOccu', '1');
    });
    await page.setViewportSize({width, height});
    await page.goto('/system/services');
    await expect(rfdRow(page)).toHaveCount(1);
}

const stop = (page: Page) => rfdRow(page).locator('.ol-rowactions button:has(.ol-acttext:text-is("Stop"))');

// two strips of the same size are the same PNG when they are the same colour: the strip where the
// scrollbar was, and one just left of it that is the same surface under the backdrop
async function sameAsBeside(page: Page, bar: number, y: number, height: number) {
    const inner = await page.evaluate(() => window.innerWidth);
    const width = Math.min(6, bar);
    const strip = await page.screenshot({clip: {x: inner - bar + (bar - width) / 2, y, width, height}});
    const beside = await page.screenshot({clip: {x: inner - bar - 9, y, width, height}});
    return strip.equals(beside);
}

test.describe('the top bar reaches the window edge', () => {
    test('on a page that does not scroll, with and without a dialog', async ({page}) => {
        await openServices(page, 1440, 1600);
        let m = await measure(page);
        expect(m.scrolls, 'the page fits the window').toBe(false);
        expect(m.client, 'nothing is reserved for a scrollbar').toBe(m.inner);
        expect(m.header, 'the top bar ends at the window edge').toBe(m.inner);

        await stop(page).click();
        await expect(page.getByRole('dialog')).toBeVisible();
        m = await measure(page);
        expect(m.header).toBe(m.inner);
        expect(m.backdrop, 'the backdrop spans the window').toEqual({left: 0, right: m.inner});
        await page.keyboard.press('Escape');
        await expect(page.getByRole('dialog')).toBeHidden();
        expect((await measure(page)).header).toBe(m.inner);
    });

    test('on a long page: the bar spans the window and the scrollbar begins under it', async ({page, isMobile, browserName}) => {
        await openServices(page, 1440, 400);
        let m = await measure(page);
        expect(m.scrolls, 'the page is longer than the room it has').toBe(true);
        const bar = await classicScrollbar(page, isMobile, browserName);
        // B-129: the bar is not cut short by the scrollbar any more, and the scrollbar's own box
        // starts where the bar ends downwards
        expect(m.header, 'the top bar ends at the window edge').toBe(m.inner);
        expect(m.portRight, "the scrollbar's box ends at the window edge").toBe(m.inner);
        expect(Math.abs(m.portTop - m.headerBottom), 'the scrolling box begins at the bar').toBeLessThanOrEqual(0.5);
        // in the bar's own band nothing is a scrollbar: the bar's colour reaches the edge there
        expect(await sameAsBeside(page, bar, 8, 14), "the strip in the top bar's band").toBe(true);
        const power = await page.locator('.ol-powerbtn').evaluate((el) => el.getBoundingClientRect().x);

        await stop(page).click({force: true});
        await expect(page.getByRole('dialog')).toBeVisible();
        m = await measure(page);
        expect(m.header, 'the bar stays at the window edge with a dialog open').toBe(m.inner);
        expect(m.backdrop, 'the backdrop covers the strip the scrollbar left').toEqual({left: 0, right: m.inner});
        expect(await page.locator('.ol-powerbtn').evaluate((el) => el.getBoundingClientRect().x), "the bar's buttons stay put").toBe(power);
        // pixels: below the bar the strip is the page ground under the backdrop, where the scrollbar was
        expect(await sameAsBeside(page, bar, 220, 40), 'the strip beside the page').toBe(true);

        await page.keyboard.press('Escape');
        await expect(page.getByRole('dialog')).toBeHidden();
        m = await measure(page);
        expect(m.header, 'closed: still at the window edge').toBe(m.inner);
        expect(m.backdrop).toBeNull();
    });

    // below 700 px the top bar has its own padding (app.css)
    test('in a narrow window, whose top bar wraps', async ({page, isMobile, browserName}) => {
        await page.setViewportSize({width: 680, height: 400});
        await page.goto('/');
        await expect(page.locator('main h1')).toBeVisible();
        test.skip(!(await measure(page)).scrolls, 'the Status page fits this window');
        await classicScrollbar(page, isMobile, browserName);
        let m = await measure(page);
        expect(m.header).toBe(m.inner);
        expect(Math.abs(m.portTop - m.headerBottom)).toBeLessThanOrEqual(0.5);

        await page.locator('.ol-powerbtn').click();
        await page.locator('.ol-powerpop [data-action="reboot"]').click();
        await expect(page.getByRole('dialog')).toBeVisible();
        m = await measure(page);
        expect(m.header).toBe(m.inner);
        expect(m.backdrop).toEqual({left: 0, right: m.inner});
        await page.keyboard.press('Escape');
        await expect(page.getByRole('dialog')).toBeHidden();
    });
});
