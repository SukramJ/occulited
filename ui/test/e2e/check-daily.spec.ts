import {type Page, type Route} from '@playwright/test';
import {expect, test} from './fixtures';
import {fitsWindow} from './scroll';

// openccu-lite task 244 (the maintainer: "a checkbox behind the 'search now' button with label
// 'check daily'"): device firmware, the system's releases and the addon catalogue each have their
// check button with *Check daily* directly behind it on the same line, bound to that check's own
// setting; the host is the hint behind the ?.

/** answers a GET with the stub's JSON changed, and records the PUT bodies to a settings path */
async function setting(page: Page, get: string, patch: (j: Record<string, unknown>) => void, put: string) {
    const bodies: unknown[] = [];
    await page.route(get, async (route: Route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const j = await response.json();
        patch(j);
        await route.fulfill({response, json: j});
    });
    await page.route(put, async (route: Route) => {
        bodies.push(route.request().postDataJSON());
        await route.fulfill({json: {ok: true, ...(route.request().postDataJSON() as object)}});
    });
    return bodies;
}

async function sameLine(page: Page, scope: ReturnType<Page['locator']>) {
    const b = (await scope.locator('[data-check-now]').boundingBox())!;
    const c = (await scope.locator('[data-daily]').boundingBox())!;
    expect(Math.abs(b.y + b.height / 2 - (c.y + c.height / 2))).toBeLessThanOrEqual(4);
    expect(c.x).toBeGreaterThan(b.x + b.width);
}

test('device firmware: Check daily reflects and saves the setting; the long switch is gone', async ({page}) => {
    const bodies = await setting(page, '**/api/system/v1/firmware', (j) => (j.enabled = false), '**/api/system/v1/firmware/settings');
    await page.goto('/system/updates');
    const fw = page.locator('[data-check-daily="firmware"]');
    await expect(fw.locator('[data-daily]')).not.toBeChecked();
    await expect(page.getByText('Fetch automatically')).toHaveCount(0);
    await expect(fw).toContainText('Check daily');
    await sameLine(page, fw);
    await fw.locator('[data-daily]').check();
    await expect.poll(() => bodies).toEqual([{enabled: true}]);
    // the hint names the host
    await fw.getByRole('button', {name: 'Help'}).click();
    await expect(fw.locator('.ol-help-pop')).toContainText('A check calls ccu3-update.homematic.com. Nothing is sent unless you press the button or Check daily is on.');
    expect(await fitsWindow(page)).toBe(true);
});

test("the system's releases: Check daily beside Check now", async ({page}) => {
    const bodies = await setting(page, '**/api/system/v1/system-update', (j) => ((j.feed as Record<string, unknown>).enabled = true), '**/api/system/v1/system-update/settings');
    await page.goto('/system/updates');
    const rel = page.locator('[data-check-daily="system-update"]');
    await expect(rel.locator('[data-daily]')).toBeChecked();
    await expect(rel.getByRole('button', {name: 'Check now'})).toBeVisible();
    await sameLine(page, rel);
    await rel.locator('[data-daily]').uncheck();
    await expect.poll(() => bodies).toEqual([{enabled: false}]);
    await expect(rel.locator('[data-daily]')).not.toBeChecked();
    await rel.getByRole('button', {name: 'Help'}).click();
    await expect(rel.locator('.ol-help-pop')).toContainText('A check calls api.github.com.');
});

test('the addon catalogue: Check daily beside Check for updates', async ({page}) => {
    const bodies = await setting(page, '**/api/system/v1/catalog', (j) => (j.daily = true), '**/api/system/v1/catalog/settings');
    await page.goto('/addons');
    const pair = page.locator('[data-check-daily]');
    await expect(pair.getByRole('button', {name: 'Check for updates'})).toBeVisible();
    await expect(pair.locator('[data-daily]')).toBeChecked();
    await sameLine(page, pair);
    await pair.locator('[data-daily]').uncheck();
    await expect.poll(() => bodies).toEqual([{daily: false}]);
    expect(await fitsWindow(page)).toBe(true);
});

test('German: Täglich prüfen', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await setting(page, '**/api/system/v1/catalog', (j) => (j.daily = false), '**/api/system/v1/catalog/settings');
    await page.goto('/addons');
    await expect(page.locator('[data-check-daily]')).toContainText('Täglich prüfen');
    await page.goto('/system/updates');
    await expect(page.locator('[data-check-daily="firmware"]')).toContainText('Täglich prüfen');
    await expect(page.locator('[data-check-daily="firmware"]').getByRole('button', {name: 'Jetzt prüfen'})).toBeVisible();
});

test('a failed save puts the box back', async ({page}) => {
    await page.route('**/api/system/v1/catalog', async (route: Route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        await route.fulfill({response, json: {...(await response.json()), daily: true}});
    });
    await page.route('**/api/system/v1/catalog/settings', (route) => route.fulfill({status: 500, json: {error: 'x', message: 'disk full'}}));
    await page.goto('/addons');
    const box = page.locator('[data-check-daily] [data-daily]');
    await expect(box).toBeChecked();
    // a click, not uncheck(): the refused save may put the box back before Playwright looks
    const [req] = await Promise.all([page.waitForRequest('**/api/system/v1/catalog/settings'), box.click()]);
    expect(req.postDataJSON()).toEqual({daily: false});
    await expect(box).toBeChecked();
});
