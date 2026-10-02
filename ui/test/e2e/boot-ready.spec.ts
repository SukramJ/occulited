import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// B-104: after a reboot and a new login, task 94's thin bar stood without a label at the top of the
// Status page, with only "Radio interfaces are starting" under it. It keeps its place, and has a
// heading - the box has restarted - and one sentence naming the interfaces still starting and about
// how many seconds are left; it goes when they are up, as before. The countdown entry is in phase 4:
// the box went down, lighttpd and occulited answered, the interfaces are what is left.

const ENTRY = 'occulite.reboot';

/** the countdown entry with occulited seen a second ago and `readyS` seconds expected for the interfaces */
async function phaseFour(page: Page, readyS: number, uiAgoS = 1) {
    await page.addInitScript(
        ([key, ready, ago]) => {
            if (sessionStorage.getItem('b104-seeded')) return;
            sessionStorage.setItem('b104-seeded', '1');
            const now = Date.now();
            const ui = now - (ago as number) * 1000;
            localStorage.setItem(key as string, JSON.stringify({v: 1, kind: 'reboot', started: ui - 60_000, expect: {down: 5, http: 25, ui: 20, ready}, seen: {down: ui - 55_000, http: ui - 30_000, ui}}));
        },
        [ENTRY, readyS, uiAgoS] as const,
    );
}

/** the services list with rfd and hmipserver as `state` says; returns a switch that brings both up */
async function interfaces(page: Page, state: {rfd: boolean; hmipserver: boolean}) {
    const up = {...state};
    await page.route('**/api/system/v1/services', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const body = (await response.json()) as {services: Record<string, unknown>[]};
        for (const id of ['rfd', 'hmipserver'] as const) Object.assign(body.services.find((s) => s.id === id)!, {running: up[id], enabled: true, failed: false, skipped: false});
        await route.fulfill({response, json: body});
    });
    await page.route('**/api/system/v1/boot-timing', (r) => r.fulfill({json: {ok: true}}));
    return () => Object.assign(up, {rfd: true, hmipserver: true});
}

const section = (page: Page) => page.locator('section.ol-bootready');

test('a heading and a sentence with the seconds and the interfaces, gone when they are up', async ({page}) => {
    await phaseFour(page, 45);
    const bringUp = await interfaces(page, {rfd: false, hmipserver: false});
    await page.goto('/');
    await expect(section(page)).toBeVisible();
    await expect(section(page).getByRole('heading', {name: 'The system has restarted'})).toBeVisible();
    const text = section(page).locator('.ol-bootbar-text');
    await expect(text).toHaveText(/^The radio interfaces are still starting – about \d+ s until HmIP-RF and BidCos-RF are ready\.$/);
    const seconds = Number(/about (\d+) s/.exec((await text.textContent()) ?? '')![1]);
    expect(seconds).toBeGreaterThan(30);
    expect(seconds).toBeLessThanOrEqual(45);
    // the bar is named by the heading, its value is the sentence
    const bar = section(page).getByRole('progressbar');
    await expect(bar).toHaveAttribute('aria-label', 'The system has restarted');
    await expect(bar).toHaveAttribute('aria-valuetext', /^The radio interfaces are still starting – about \d+ s until HmIP-RF and BidCos-RF are ready\.$/);
    // it stays where it was: above the page's own heading
    const [bootY, h1Y] = await Promise.all([section(page).evaluate((e) => e.getBoundingClientRect().top), page.locator('main h1').evaluate((e) => e.getBoundingClientRect().top)]);
    expect(bootY).toBeLessThan(h1Y);

    bringUp();
    await expect(section(page)).toHaveCount(0, {timeout: 10_000});
    expect(await page.evaluate((key) => localStorage.getItem(key), ENTRY)).toBeNull();
});

test('in German, with one interface still starting', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await phaseFour(page, 45);
    await interfaces(page, {rfd: false, hmipserver: true});
    await page.goto('/');
    await expect(section(page).getByRole('heading', {name: 'Das System wurde neu gestartet'})).toBeVisible();
    await expect(section(page).locator('.ol-bootbar-text')).toHaveText(/^Die Funkschnittstellen starten noch – etwa \d+ s, bis BidCos-RF bereit ist\.$/);
    await expect(section(page).getByRole('progressbar')).toHaveAttribute('aria-label', 'Das System wurde neu gestartet');
});

test('past the estimate: the interfaces take longer than usual, the stripe runs', async ({page}) => {
    await phaseFour(page, 2, 10);
    await interfaces(page, {rfd: false, hmipserver: false});
    await page.goto('/');
    await expect(section(page).locator('.ol-bootbar-text')).toHaveText('The radio interfaces are still starting – HmIP-RF and BidCos-RF take longer than usual.');
    await expect(section(page).locator('.ol-bootbar-track')).toHaveClass(/indeterminate/);
});

test('with reduced motion nothing moves', async ({page}) => {
    await page.emulateMedia({reducedMotion: 'reduce'});
    await phaseFour(page, 2, 10);
    await interfaces(page, {rfd: false, hmipserver: false});
    await page.goto('/');
    await expect(section(page)).toBeVisible();
    await expect(section(page).locator('.ol-bootbar')).toHaveClass(/ol-bootbar-still/);
    expect(await section(page).locator('.ol-bootbar-fill').evaluate((e) => getComputedStyle(e).animationName)).toBe('none');
});

test('the heading and the sentence stay inside a narrow window', async ({page}) => {
    await page.setViewportSize({width: 360, height: 800});
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await phaseFour(page, 45);
    await interfaces(page, {rfd: false, hmipserver: false});
    await page.goto('/');
    await expect(section(page).locator('.ol-bootbar-text')).toContainText('HmIP-RF und BidCos-RF');
    const box = await section(page).evaluate((e) => ({right: e.getBoundingClientRect().right, scroll: e.scrollWidth, client: e.clientWidth}));
    expect(box.scroll).toBeLessThanOrEqual(box.client);
    expect(box.right).toBeLessThanOrEqual(360);
});
