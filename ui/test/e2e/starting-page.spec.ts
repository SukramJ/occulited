import fs from 'node:fs';
import {fileURLToPath} from 'node:url';
import {expect, test, type Page} from '@playwright/test';

// Task 94: lighttpd's waiting page (deploy/lighttpd/occulite-starting.html), served the way
// lighttpd serves it while occulited does not answer - under the URL that was asked for, with a
// 503 - and with its placeholders filled in as S50lighttpd's sed does. The health route answers
// lighttpd's 503 until occulited is "started"; then the page must reload onto the same URL.

const FILE = fileURLToPath(new URL('../../../deploy/lighttpd/occulite-starting.html', import.meta.url));
const PAGE = fs.readFileSync(FILE, 'utf8');
const ENTRY = 'occulite.reboot';
const IP = '192.0.2.7';

const served = () => PAGE.replaceAll('@HOSTNAME@', 'ccu-charly').replaceAll('@VERSION@', '1.0.0-alpha.0').replaceAll('@PLATFORM@', 'rpi3').replaceAll('@IP@', IP);

/** occulited is down until `upAfterMs` from now: documents at `target` get the waiting page, the health route lighttpd's 503. */
async function occulitedDown(page: Page, target: string, upAfterMs: number) {
    const t0 = Date.now();
    const up = () => Date.now() - t0 >= upAfterMs;
    let documents = 0;
    await page.route(
        (url) => url.pathname + url.search === target,
        async (r) => {
            if (r.request().resourceType() !== 'document') return r.continue();
            documents++;
            if (up()) return r.continue();
            await r.fulfill({status: 503, headers: {'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store', 'Retry-After': '5'}, body: served()});
        },
    );
    await page.route('**/api/system/v1/health', (r) => (up() ? r.continue() : r.fulfill({status: 503, headers: {'Retry-After': '5'}, json: {error: 'starting', message: 'occulited is not answering yet'}})));
    return {documents: () => documents};
}

for (const lang of [
    {locale: 'en-US', title: 'openccu-lite is starting …', back: /^Back in about \d+ s$/, hint: 'recovery system', services: 'Services', backup: 'Backup'},
    {locale: 'de-DE', title: 'openccu-lite startet …', back: /^In etwa \d+ s wieder da$/, hint: 'Recovery-System', services: 'Dienste', backup: 'Sicherung'},
]) {
    test.describe(`the waiting page, ${lang.locale}`, () => {
        test.use({locale: lang.locale});

        test('shows the box, a spinner, and reloads the URL asked for once occulited answers', async ({page}) => {
            const target = '/system/services?tab=units';
            const rig = await occulitedDown(page, target, 5000);
            const response = await page.goto(target);
            expect(response?.status()).toBe(503);
            await expect(page.locator('h1')).toHaveText(lang.title);
            await expect(page).toHaveTitle(lang.title);
            await expect(page.locator('#ident')).toHaveText('ccu-charly · 1.0.0-alpha.0');
            await expect(page.locator('#spinner')).toBeVisible();
            await expect(page.getByRole('progressbar')).toHaveCount(0);
            await expect(page.locator('#hint')).toBeHidden();
            // light or dark, as the browser asks, and within a phone's width
            const dark = await page.evaluate(() => matchMedia('(prefers-color-scheme: dark)').matches);
            await expect(page.locator('body')).toHaveCSS('background-color', dark ? 'rgb(27, 31, 38)' : 'rgb(255, 255, 255)');
            await expect(page.locator(dark ? '.logo-dark' : '.logo-light')).toBeVisible();
            await expect(page.locator(dark ? '.logo-light' : '.logo-dark')).toBeHidden();
            const box = (await page.locator('.box').boundingBox())!;
            expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width);
            // occulited is back: the same URL, now the web interface
            // task 57: a system page's heading is the title switcher, "System › Services"
            await expect(page.locator('main h1')).toContainText(lang.services, {timeout: 20_000});
            const url = new URL(page.url());
            expect(url.pathname + url.search).toBe(target);
            expect(rig.documents()).toBe(2);
        });

        test('goes on with the countdown the web interface started, then reloads', async ({page}) => {
            // the power menu wrote it 20 s ago and saw the box go after 5 s
            await page.addInitScript((key) => {
                if (sessionStorage.getItem('planted')) return;
                sessionStorage.setItem('planted', '1');
                const s = Date.now() - 20_000;
                localStorage.setItem(key, JSON.stringify({v: 1, kind: 'reboot', started: s, expect: {down: 5, http: 10, ui: 30, ready: 19}, seen: {down: s + 5000}}));
            }, ENTRY);
            const rig = await occulitedDown(page, '/system/backup', 6000);
            await page.goto('/system/backup');
            const bar = page.getByRole('progressbar');
            await expect(bar).toBeVisible();
            await expect(page.locator('#spinner')).toBeHidden();
            await expect(bar).toHaveAttribute('aria-valuenow', /^\d+$/);
            await expect(page.locator('#text')).toHaveText(lang.back);
            // lighttpd answering, the checkpoint this page stands on
            const seen = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) ?? '{}').seen, ENTRY);
            expect(seen.http).toBeGreaterThan(seen.down);
            await expect(page.locator('main h1')).toContainText(lang.backup, {timeout: 20_000});
            expect(new URL(page.url()).pathname).toBe('/system/backup');
            expect(rig.documents()).toBe(2);
        });

        test('three minutes past a whole reboot of the product: the recovery hint by the box\'s address', async ({page}) => {
            await page.clock.install();
            await occulitedDown(page, '/', Number.POSITIVE_INFINITY);
            await page.goto('/');
            await expect(page.locator('h1')).toHaveText(lang.title);
            // rpi3: 9 + 35 + 2 s to the web interface, then three minutes
            await page.clock.fastForward('03:30');
            await expect(page.locator('#hint')).toBeHidden();
            await page.clock.fastForward('00:20');
            const hint = page.locator('#hint');
            await expect(hint).toContainText(lang.hint);
            await expect(hint.getByRole('link', {name: `http://${IP}/`})).toHaveAttribute('href', `http://${IP}/`);
        });
    });
}

test('placeholders sed did not fill show nothing', async ({page}) => {
    await page.route('**/api/system/v1/health', (r) => r.fulfill({status: 503, json: {error: 'starting'}}));
    await page.route((url) => url.pathname === '/raw', (r) => r.fulfill({status: 503, contentType: 'text/html; charset=utf-8', body: PAGE}));
    await page.goto('/raw');
    await expect(page.locator('#ident')).toBeHidden();
    await expect(page.locator('body')).not.toContainText('@');
});
