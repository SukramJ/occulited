import {expect, test, type Page} from '@playwright/test';

// task 79: the RPC trace - the switch (on System → Remote access, the lite-rpc section's last
// panel since openccu-lite task 224; the Interfaces page's before) (off, on for a while, permanently,
// with the SD-card warning where the journal persists), and its lines on the Log page: a long
// line folded behind a triangle, a copy button on every line.
async function own(page: Page, baseURL: string | undefined, sd = false) {
    const id = Math.random().toString(36).slice(2);
    await page.context().addCookies([
        {name: 'stub-trace', value: `tr-${id}`, url: baseURL!},
        ...(sd ? [{name: 'stub-trace-sd', value: '1', url: baseURL!}] : []),
    ]);
}

test('the switch: off, on for an hour, permanently, off again', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/remote-access');
    await expect(page.locator('[data-panel="rpc-trace"]').getByRole('heading', {level: 3, name: 'RPC trace'})).toBeVisible();
    await expect(page.locator('[data-rpc-trace-state]')).toHaveText('off');
    await expect(page.getByRole('link', {name: 'Open the trace in the Log'})).toHaveAttribute('href', '/system/log?tag=rpc-trace&severity=debug');
    await page.getByRole('button', {name: 'Change…'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Lines are capped at 8 kB');
    await expect(dialog).not.toContainText('SD card');
    const select = dialog.getByRole('listbox');
    await expect(select.locator('option')).toHaveText(['off', 'on for 15 minutes', 'on for 1 hour', 'on for 6 hours', 'on for 24 hours', 'on for 7 days', 'on permanently']);
    await select.selectOption('h1');
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/api/system/v1/rpc-trace')),
        dialog.getByRole('button', {name: 'Apply'}).click(),
    ]);
    expect(req.postDataJSON()).toEqual({mode: 'until', minutes: 60});
    await expect(page.locator('[data-rpc-trace-state]')).toContainText('on until');
    await expect(page.locator('[data-rpc-trace="on"]')).toBeVisible();
    await page.getByRole('button', {name: 'Change…'}).click();
    await page.getByRole('dialog').getByRole('listbox').selectOption('permanent');
    await page.getByRole('dialog').getByRole('button', {name: 'Apply'}).click();
    await expect(page.locator('[data-rpc-trace-state]')).toHaveText('on permanently');
    await page.getByRole('button', {name: 'Change…'}).click();
    await page.getByRole('dialog').getByRole('listbox').selectOption('off');
    await page.getByRole('dialog').getByRole('button', {name: 'Apply'}).click();
    await expect(page.locator('[data-rpc-trace-state]')).toHaveText('off');
});

test('the SD-card warning where the journal persists', async ({page, baseURL}) => {
    await own(page, baseURL, true);
    await page.goto('/system/remote-access');
    await page.getByRole('button', {name: 'Change…'}).click();
    await expect(page.getByRole('dialog')).toContainText('Warning: this system\'s journal persists on the SD card.');
    await page.keyboard.press('Escape');
});

test('the Log page folds a long trace line and copies one', async ({page, baseURL, context}) => {
    await own(page, baseURL);
    await context.grantPermissions(['clipboard-read', 'clipboard-write']).catch(() => undefined);
    await page.goto('/system/log?tag=rpc-trace&severity=debug');
    const lines = page.locator('.ol-line.lg-trace');
    await expect(lines).toHaveCount(6);
    await expect(lines.nth(0)).toContainText('xmlrpc 192.0.2.50 (token home-assistant) → HmIP-RF getValue');
    // the long listDevices answer is folded; the triangle opens it
    const long = lines.nth(5);
    await expect(long.locator('.lg-trace-msg')).toHaveClass(/lg-trace-folded/);
    await expect(long.locator('.lg-trace-msg')).toContainText('…');
    await long.getByRole('button', {name: 'Expand'}).click();
    await expect(long.locator('.lg-trace-msg')).not.toHaveClass(/lg-trace-folded/);
    await expect(long.locator('.lg-trace-msg')).toContainText('"FIRMWARE":"1.6.2"}]');
    await long.getByRole('button', {name: 'Collapse'}).click();
    await expect(long.locator('.lg-trace-msg')).toHaveClass(/lg-trace-folded/);
    // a short line has no triangle, every line a copy button
    await expect(lines.nth(0).getByRole('button', {name: 'Expand'})).toHaveCount(0);
    await expect(lines.nth(0).getByRole('button', {name: 'Copy line'})).toBeVisible();
    await lines.nth(1).getByRole('button', {name: 'Copy line'}).click();
    const copied = await page.evaluate(() => navigator.clipboard.readText().catch(() => ''));
    if (copied) expect(copied).toBe('← HmIP-RF 192.0.2.50 (token home-assistant) getValue 0.5');
});

// openccu-lite task 224: gone from the Interfaces page; task 242 (the maintainer: "remove rpc trace
// link from system/interfaces page") took the link there away too. An old bookmark with #rpc-trace
// still lands on the panel
test('the Interfaces page has no trace and no link to it; the old anchor leads to Remote access', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/interfaces');
    await expect(page.locator('h2#modules')).toBeVisible();
    await expect(page.locator('#rpc-trace')).toHaveCount(0);
    await expect(page.locator('[data-trace-link]')).toHaveCount(0);
    await expect(page.locator('main a[href*="rpc-trace"]')).toHaveCount(0);
    await page.goto('/system/interfaces#rpc-trace');
    await expect(page).toHaveURL(/\/system\/remote-access#rpc-trace$/);
    await expect(page.locator('h3#rpc-trace')).toBeInViewport();
    await page.goto('/radio#rpc-trace');
    await expect(page).toHaveURL(/\/system\/remote-access#rpc-trace$/);
});

test('in German, dark', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.emulateMedia({colorScheme: 'dark'});
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/remote-access');
    await expect(page.locator('h3#rpc-trace')).toHaveText('RPC-Trace');
    await expect(page.locator('[data-rpc-trace-state]')).toHaveText('aus');
});
