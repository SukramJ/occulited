import {expect, test, type Page, type Route} from '@playwright/test';

// openccu-lite B-241 (D-90): the welcome page asks once about the daily checks, one checkbox per
// destination, each named with where it connects, both unticked on a fresh system. GitHub is one
// box for two settings (the release check, the catalogue's daily check with the addons' update
// checks); eQ-3 is the device firmware check and download. The old text that named eQ-3 as "the
// only thing it calls out for" is gone.

/** the stub's answer with the setting patched, and the PUT bodies of a settings path */
async function setting(page: Page, get: string, patch: (j: Record<string, unknown>) => void, put: string, reply: (b: Record<string, unknown>) => object) {
    const bodies: unknown[] = [];
    await page.route(get, async (route: Route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const j = await response.json();
        patch(j);
        await route.fulfill({response, json: j});
    });
    await page.route(put, async (route: Route) => {
        const b = route.request().postDataJSON() as Record<string, unknown>;
        bodies.push(b);
        await route.fulfill({json: {ok: true, ...reply(b)}});
    });
    return bodies;
}

async function fresh(page: Page) {
    const fw = await setting(page, '**/api/system/v1/firmware', (j) => (j.enabled = false), '**/api/system/v1/firmware/settings', (b) => ({enabled: b.enabled}));
    const rel = await setting(page, '**/api/system/v1/system-update', (j) => ((j.feed as Record<string, unknown>).enabled = false), '**/api/system/v1/system-update/settings', (b) => ({enabled: b.enabled}));
    const cat = await setting(page, '**/api/system/v1/catalog', (j) => (j.daily = false), '**/api/system/v1/catalog/settings', (b) => ({daily: b.daily}));
    return {fw, rel, cat};
}

test('a fresh system: both destinations named, both unticked, each box writes its settings', async ({page}) => {
    const puts = await fresh(page);
    const calls: string[] = [];
    page.on('request', (r) => calls.push(`${r.method()} ${new URL(r.url()).pathname}${new URL(r.url()).search}`));
    await page.goto('/welcome');
    await expect(page.getByRole('heading', {name: '1 · Automatic checks'})).toBeVisible();
    await expect(page.getByText('This system connects to the internet only when you ask it to.', {exact: false})).toBeVisible();
    const step = page.locator('[data-welcome-outbound]');
    const gh = step.locator('[data-outbound="github"]');
    const eq3 = step.locator('[data-outbound="eq3"]');
    await expect(gh).not.toBeChecked();
    await expect(eq3).not.toBeChecked();
    await expect(step).toContainText('GitHub (api.github.com, raw.githubusercontent.com)');
    await expect(step).toContainText('eQ-3 (ccu3-update.homematic.com)');
    await expect(step).toContainText('new system release');
    await expect(step).toContainText('addon catalogue');
    await expect(step).toContainText('firmware of the paired device types');
    await expect(page.getByText('the only thing it calls out for')).toHaveCount(0);
    await expect(page.getByText('Download automatically')).toHaveCount(0);
    // the page reads the catalogue as it is held, never the check
    expect(calls.filter((c) => c.includes('/catalog?refresh'))).toEqual([]);

    await gh.check();
    await expect.poll(() => puts.rel).toEqual([{enabled: true}]);
    await expect.poll(() => puts.cat).toEqual([{daily: true}]);
    await expect(gh).toBeChecked();
    expect(puts.fw).toEqual([]);

    await eq3.check();
    await expect.poll(() => puts.fw).toEqual([{enabled: true}]);
    await expect(eq3).toBeChecked();

    await gh.uncheck();
    await expect.poll(() => puts.rel).toEqual([{enabled: true}, {enabled: false}]);
    await expect.poll(() => puts.cat).toEqual([{daily: true}, {daily: false}]);
    await expect(gh).not.toBeChecked();

    // the other steps are still there, and Done leaves the page
    await expect(page.getByRole('heading', {name: '2 · Names from an old CCU'})).toBeVisible();
    await page.getByRole('button', {name: 'Done'}).click();
    await expect(page).toHaveURL(/\/$/);
});

test('a system in use shows what it runs with: GitHub ticked only when both its settings are on', async ({page}) => {
    await setting(page, '**/api/system/v1/firmware', (j) => (j.enabled = true), '**/api/system/v1/firmware/settings', (b) => ({enabled: b.enabled}));
    await setting(page, '**/api/system/v1/system-update', (j) => ((j.feed as Record<string, unknown>).enabled = true), '**/api/system/v1/system-update/settings', (b) => ({enabled: b.enabled}));
    await setting(page, '**/api/system/v1/catalog', (j) => (j.daily = false), '**/api/system/v1/catalog/settings', (b) => ({daily: b.daily}));
    await page.goto('/welcome');
    const step = page.locator('[data-welcome-outbound]');
    await expect(step.locator('[data-outbound="eq3"]')).toBeChecked();
    await expect(step.locator('[data-outbound="github"]')).not.toBeChecked();
});

test('German: the step and both destinations', async ({page}) => {
    await fresh(page);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/welcome');
    await expect(page.getByRole('heading', {name: '1 · Automatische Prüfungen'})).toBeVisible();
    const step = page.locator('[data-welcome-outbound]');
    await expect(step).toContainText('GitHub (api.github.com, raw.githubusercontent.com): täglich nach einer neuen Systemversion suchen');
    await expect(step).toContainText('eQ-3 (ccu3-update.homematic.com): täglich nach neuer Firmware');
    await expect(step).toContainText('Installieren bleibt deine Entscheidung.');
});
