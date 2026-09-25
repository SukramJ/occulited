import {expect, test, type Page} from '@playwright/test';

// Task 94: the reboot countdown in occulited's page. The power menu's reboot writes the countdown
// entry before it sends the request and shows a bar that empties; the health route's answers land
// in the entry as checkpoints. After the reload, a thinner bar waits for the radio interfaces and
// the page reports what it saw.

const ENTRY = 'occulite.reboot';

async function german(page: Page) {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
}

async function rebootFlow(page: Page) {
    let rebooted = false;
    let stored: string | null = null;
    await page.route(
        (url) => url.pathname === '/api/system/v1/boot-expect',
        // a slow occulited start, so the bar still runs after lighttpd's checkpoint
        (r) => r.fulfill({json: {kind: 'reboot', product: 'ova', expect: {down: 5, http: 31, ui: 30, ready: 19}, default: {down: 5, http: 31, ui: 1, ready: 19}, measured: {down: 0, http: 0, ui: 0, ready: 0}}}),
    );
    await page.route('**/api/system/v1/reboot', async (r) => {
        // what the page had written by the time the request left it
        stored = await page.evaluate((key) => localStorage.getItem(key), ENTRY);
        rebooted = true;
        await r.fulfill({json: {ok: true, message: 'rebooting'}});
    });
    // after the request: the box is gone for one poll, then lighttpd answers alone, occulited is not back
    let polls = 0;
    await page.route('**/api/system/v1/health', (r) => {
        if (!rebooted) return r.continue();
        return ++polls === 1 ? r.abort('connectionrefused') : r.fulfill({status: 503, json: {error: 'starting', message: 'occulited is not answering yet'}});
    });
    return {stored: () => stored};
}

for (const lang of [
    {name: 'English', setup: async () => {}, back: /^Back in about \d+ s$/},
    {name: 'German', setup: german, back: /^In etwa \d+ s wieder da$/},
]) {
    test(`a reboot writes the countdown before the request and shows the bar (${lang.name})`, async ({page}) => {
        await lang.setup(page);
        const flow = await rebootFlow(page);
        await page.goto('/');
        await page.locator('.ol-powerbtn').click();
        await page.locator('.ol-powerpop [data-action="reboot"]').click();
        await expect(page.getByRole('dialog')).toBeVisible();
        await page.keyboard.press('Enter');
        const state = page.locator('.ol-powerstate');
        const bar = state.getByRole('progressbar');
        await expect(bar).toBeVisible();
        const entry = JSON.parse(flow.stored() ?? 'null');
        expect(entry).toMatchObject({v: 1, kind: 'reboot', expect: {down: 5, http: 31, ui: 30, ready: 19}, seen: {}});
        expect(Math.abs(Date.now() - entry.started)).toBeLessThan(30_000);
        await expect(bar).toHaveAttribute('aria-valuenow', /^\d+$/);
        await expect(bar).toHaveAttribute('aria-valuetext', lang.back);
        await expect(state.locator('.ol-bootbar-text')).toHaveText(lang.back);
        // the box gone, then lighttpd's 503: the checkpoints the waiting page would go on from
        await expect.poll(() => page.evaluate((key) => JSON.parse(localStorage.getItem(key) ?? '{}').seen?.http ?? 0, ENTRY), {timeout: 10_000}).toBeGreaterThan(entry.started);
        const seen = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) ?? '{}').seen, ENTRY);
        expect(seen.down).toBeLessThan(seen.http);
        // the fill empties: a narrower bar a moment later
        const width = () => state.locator('.ol-bootbar-fill').evaluate((e) => e.getBoundingClientRect().width);
        const first = await width();
        await page.waitForTimeout(1200);
        expect(await width()).toBeLessThan(first);
    });
}

test('a refused reboot leaves no countdown behind', async ({page}) => {
    await page.route('**/api/system/v1/reboot', (r) => r.fulfill({status: 500, json: {error: 'internal', message: 'no reboot command'}}));
    await page.goto('/');
    await page.locator('.ol-powerbtn').click();
    await page.locator('.ol-powerpop [data-action="reboot"]').click();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('dialog', {name: 'Reboot', exact: true})).toContainText('no reboot command');
    expect(await page.evaluate((key) => localStorage.getItem(key), ENTRY)).toBeNull();
    await expect(page.locator('.ol-powerstate')).toHaveCount(0);
});

test('under reduced motion the bar does not animate', async ({page}) => {
    await page.emulateMedia({reducedMotion: 'reduce'});
    await rebootFlow(page);
    await page.goto('/');
    await page.locator('.ol-powerbtn').click();
    await page.locator('.ol-powerpop [data-action="reboot"]').click();
    await page.keyboard.press('Enter');
    const fill = page.locator('.ol-powerstate .ol-bootbar-fill');
    await expect(fill).toBeVisible();
    expect(await fill.evaluate((e) => getComputedStyle(e).animationName)).toBe('none');
});

test('after the reboot: the interfaces bar until rfd and hmipserver run, then the report and no entry', async ({page}) => {
    const started = Date.now() - 45_000;
    await page.addInitScript(
        ([key, s]) => {
            if (sessionStorage.getItem('planted')) return;
            sessionStorage.setItem('planted', '1');
            localStorage.setItem(key as string, JSON.stringify({v: 1, kind: 'reboot', started: s, expect: {down: 5, http: 31, ui: 1, ready: 19}, seen: {down: (s as number) + 5000, http: (s as number) + 38000, ui: (s as number) + 40000}}));
        },
        [ENTRY, started] as const,
    );
    const hmipAt = Date.now() + 4000;
    await page.route('**/api/system/v1/services', async (r) => {
        const response = await r.fetch();
        const body = await response.json();
        if (Date.now() < hmipAt) body.services = body.services.map((s: {id: string}) => (s.id === 'hmipserver' ? {...s, running: false} : s));
        await r.fulfill({response, json: body});
    });
    let report: Record<string, number | string> | null = null;
    await page.route('**/api/system/v1/boot-timing', async (r) => {
        report = r.request().postDataJSON();
        await r.fulfill({json: {ok: true, attached: 'record'}});
    });
    await page.goto('/');
    const ready = page.locator('.ol-bootready');
    await expect(ready.getByRole('progressbar')).toBeVisible();
    // B-104: a heading and a sentence naming what still starts (boot-ready.spec.ts has the details)
    await expect(ready.getByRole('heading', {name: 'The system has restarted'})).toBeVisible();
    await expect(ready).toContainText('until HmIP-RF is ready');
    await expect(ready).toHaveCount(0, {timeout: 15_000});
    await expect.poll(() => report).not.toBeNull();
    expect(report).toMatchObject({kind: 'reboot', started, down: started + 5000, http: started + 38000, ui: started + 40000});
    expect(Number(report!['ready'])).toBeGreaterThanOrEqual(hmipAt - 1000);
    expect(await page.evaluate((key) => localStorage.getItem(key), ENTRY)).toBeNull();
    await expect(page.locator('main h1')).toHaveText('Status');
});

test('an entry of a recovery boot is dropped when the box runs normally again', async ({page}) => {
    await page.addInitScript((key) => {
        if (sessionStorage.getItem('planted')) return;
        sessionStorage.setItem('planted', '1');
        localStorage.setItem(key, JSON.stringify({v: 1, kind: 'recovery', started: Date.now() - 300_000, expect: {down: 5, http: 31, ui: 1, ready: 19}, seen: {}}));
    }, ENTRY);
    await page.goto('/');
    await expect(page.locator('main h1')).toHaveText('Status');
    await expect.poll(() => page.evaluate((key) => localStorage.getItem(key), ENTRY)).toBeNull();
    await expect(page.locator('.ol-bootready')).toHaveCount(0);
});
