import {expect, test} from '@playwright/test';

// openccu-lite task 275: after a device import from a backup whose HmIP identity belongs to another
// radio module, the Interfaces page says how hmipserver's move of the identity onto this module
// went - pending with a retry and the battery hint, done, rejected (the exchange notice takes over),
// no module - and the BidCos line whether rfd runs with the imported identity; task 278: that the
// backup's non-default key came along. Dismiss removes the record. The stub shows the record for
// stub-import=<state>.

async function open(page: import('@playwright/test').Page, state: string, admin = true) {
    await page.context().addCookies([{name: 'stub-import', value: `${state}.${Math.random().toString(36).slice(2)}`, url: 'http://127.0.0.1'}]);
    if (!admin) await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    await page.goto('/system/interfaces');
}

test('pending: the notice, the retry restarts HmIP-RF, the BidCos line and the key hint', async ({page}) => {
    await open(page, 'pending');
    const n = page.locator('[data-notice="devices-import"]');
    await expect(n).toHaveAttribute('data-state', 'pending');
    await expect(n).toContainText('Devices imported from a backup');
    await expect(n.locator('[data-hmip="pending"]')).toContainText('the identity of module 3014F711A0001F5F000000AF is not on 3014F711A0001F0000000A03 yet');
    await expect(n.locator('[data-bidcos="took"]')).toContainText('rfd runs with the imported identity (address 0xFF5678, serial 1709ADFA00) on RPI-RF-MOD 0000000A03');
    await expect(n.locator('[data-bidcos="key"]')).toContainText('non-default BidCos security key came along');
    const retry = n.locator('[data-action="import-retry"]');
    await expect(retry).toBeEnabled();
    await retry.click();
    await expect(n.locator('[data-switching="retry"]')).toContainText('HmIP-RF is restarting');
    await expect(retry).toBeDisabled();
});

test('done: hmipserver took the identity over, the battery hint, no retry, dismiss removes the record', async ({page}) => {
    await open(page, 'done');
    const n = page.locator('[data-notice="devices-import"]');
    await expect(n).toHaveAttribute('data-state', 'done');
    await expect(n.locator('[data-hmip="done"]')).toContainText('hmipserver took the identity of module 3014F711A0001F5F000000AF over onto 3014F711A0001F0000000A03');
    await expect(n.locator('[data-hmip="done"]')).toContainText('press a button on it if it stays silent');
    await expect(n.locator('[data-hmip-line]')).toHaveText('Adapter exchange successful.');
    await expect(n.locator('[data-action="import-retry"]')).toHaveCount(0);
    await n.locator('[data-action="import-dismiss"]').click();
    await expect(n).toHaveCount(0);
    await page.reload();
    await expect(page.locator('[data-notice="devices-import"]')).toHaveCount(0);
});

test('no module, and a user sees the notice without its buttons; German', async ({page}) => {
    await open(page, 'no-module', false);
    const n = page.locator('[data-notice="devices-import"]');
    await expect(n.locator('[data-hmip="no-module"]')).toContainText('this system has no HmIP module to take it over');
    await expect(n.locator('[data-action="import-dismiss"]')).toHaveCount(0);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.reload();
    await expect(n).toContainText('Geräte aus einer Sicherung importiert');
    await expect(n.locator('[data-hmip="no-module"]')).toContainText('dieses System hat kein HmIP-Funkmodul');
});
