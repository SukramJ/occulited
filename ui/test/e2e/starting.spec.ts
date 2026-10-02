import {type Locator} from '@playwright/test';
import {expect, test} from './fixtures';
import {fitsWindow} from './scroll';

// Task 94, section 7: during a boot the web UI answers long before hmipserver's JVM is ready (about
// 24 s and 65 s on the Charly). An interface process whose unit systemd is still starting is
// "starting" with its seconds, never down or stopped - on the Status page, the Interfaces page and the
// Services list. The stub plants the boot with the cookie stub-starting: `1` is hmipserver activating
// for 23 s while rfd answers; `boot` is the radio detection still running and the stack queued behind it.

const seconds = async (l: Locator) => Number(((await l.innerText()).match(/(\d+) s/) ?? [])[1] ?? NaN);

test('hmipserver starting: Status, Interfaces and Services say starting, with the seconds', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-starting', value: '1', url: baseURL!}]);
    await page.goto('/');
    const card = page.locator('[data-component="HmIP-RF"]');
    await expect(card).toHaveAttribute('data-state', 'starting');
    const body = card.locator('.ol-card-body');
    await expect(body).toHaveText(/^starting · 2\d s$/);
    await expect(card).not.toContainText('not answering');
    await expect(card).not.toHaveClass(/\berr\b/);
    await expect(card.locator('.ol-dot.starting')).toHaveCount(1);
    // the seconds count on between two polls of the box
    const first = await seconds(body);
    await expect.poll(() => seconds(body), {timeout: 5000}).toBeGreaterThan(first);
    // rfd answered: up, as before
    await expect(page.locator('[data-component="BidCos-RF"] .ol-card-body')).toHaveText('up');

    // task 183: the connection panel carries hmipserver's unit dot, the Services page's
    await page.goto('/radio');
    const dot = page.locator('[data-process="hmipserver"] .ol-card-sub .ol-dot');
    await expect(dot).toHaveAttribute('data-state', 'starting');
    await expect(dot).toHaveClass(/\bstarting\b/);
    await expect(dot).toHaveAttribute('title', 'Starting');
    await expect(page.locator('[data-process="rfd"] .ol-card-sub .ol-dot')).toHaveAttribute('data-state', 'running');

    await page.goto('/system/services');
    const row = page.locator('tr[data-service="hmipserver"]');
    await expect(row.locator('[data-cell="status"]')).toHaveText(/^Starting · 2\d s$/);
    await expect(row.locator('.ol-dot.starting')).toHaveCount(1);
    await expect(row).not.toContainText('Stopped');
    expect(await fitsWindow(page)).toBe(true);
});

test('at the start of a boot: the detection is still running, the radio stack is queued', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-starting', value: 'boot', url: baseURL!}]);
    await page.goto('/radio');
    await expect(page.locator('[data-notice="detecting"]')).toHaveText('The radio module detection is still running.');
    await expect(page.getByText('No radio module detected.')).toHaveCount(0);

    await page.goto('/');
    // the sampler has not seen either interface yet: a card each, starting, without seconds (queued)
    for (const name of ['BidCos-RF', 'HmIP-RF']) {
        const card = page.locator(`[data-component="${name}"]`);
        await expect(card, name).toHaveAttribute('data-state', 'starting');
        await expect(card.locator('.ol-card-body'), name).toHaveText('starting');
    }
});

// the Interfaces page opened while /var/hm_mode is still missing picks the modules up by itself
test('a late /var/hm_mode reaches an open Interfaces page', async ({page, context, baseURL}) => {
    test.setTimeout(45_000);
    await context.addCookies([{name: 'stub-starting', value: '1', url: baseURL!}]);
    let calls = 0;
    // the later reads wait until the empty page has been seen: the health poll asks again as soon as
    // it has answered, and under a loaded run that answer overtook the assertion
    let seen = () => {};
    const shown = new Promise<void>((resolve) => (seen = resolve));
    await page.route('**/api/system/v1/radio', async (route) => {
        calls += 1;
        const first = calls === 1;
        if (!first) await shown;
        const response = await route.fetch();
        const body = (await response.json()) as {mode: string; modules: unknown[]; interfaces: unknown[]};
        if (first) Object.assign(body, {mode: '', modules: [], interfaces: []});
        await route.fulfill({response, json: body});
    });
    await page.goto('/radio');
    await expect(page.getByText('No radio module detected.')).toBeVisible();
    seen();
    await expect(page.locator('#ol-module-HmIP-RF')).toBeVisible({timeout: 20_000});
    expect(calls).toBeGreaterThan(1);
});

test('the clock notice: after the clock gate timed out, until chrony has synchronised', async ({page, context, baseURL}) => {
    await page.goto('/');
    await expect(page.locator('.ol-hero')).toBeVisible();
    await expect(page.locator('[data-notice="clock"]')).toHaveCount(0);

    await context.addCookies([{name: 'stub-clock', value: 'timeout', url: baseURL!}]);
    await page.reload();
    const notice = page.locator('[data-notice="clock"]');
    await expect(notice).toContainText('The clock is not synchronised.');
    // a quiet notice, not a warning
    await expect(notice).not.toHaveClass(/\berror\b/);

    await context.addCookies([{name: 'stub-clock', value: 'synced', url: baseURL!}]);
    await page.reload();
    await expect(page.locator('.ol-hero')).toBeVisible();
    await expect(page.locator('[data-notice="clock"]')).toHaveCount(0);
});

test('in German: startet, and the clock notice', async ({page, context, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await context.addCookies([
        {name: 'stub-starting', value: '1', url: baseURL!},
        {name: 'stub-clock', value: 'timeout', url: baseURL!},
    ]);
    await page.goto('/');
    await expect(page.locator('[data-component="HmIP-RF"] .ol-card-body')).toHaveText(/^startet · 2\d s$/);
    await expect(page.locator('[data-notice="clock"]')).toContainText('Die Uhrzeit ist nicht synchronisiert.');
    await page.goto('/system/services');
    await expect(page.locator('tr[data-service="hmipserver"] [data-cell="status"]')).toHaveText(/^Startet · 2\d s$/);
});
