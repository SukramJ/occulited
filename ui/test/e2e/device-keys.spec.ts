import {expect, test, type Page} from '@playwright/test';
import {qrPNG} from './qrpng';

// task 154 (D-103, D-104): the HmIP device keys on the Interfaces page - the count, the three ways
// in (a photo of a sticker, a pasted code, the SGTIN and printed key typed), the confirm for a
// device that is not paired, apply, remove, and the key sheet behind the password or a fresh login
// at the identity provider.

async function own(page: Page, baseURL: string | undefined, extra: Record<string, string> = {}) {
    const id = `dk-${Math.random().toString(36).slice(2)}`;
    await page.context().addCookies([{name: 'stub-dk', value: id, url: baseURL!}, ...Object.entries(extra).map(([name, value]) => ({name, value, url: baseURL!}))]);
}

const row = (page: Page, sgtinOrAddress: string) => page.locator(`tr[data-row="${sgtinOrAddress}"]`);

test('the count, a pasted code, a photo, the SGTIN and printed key, a device not paired, apply, remove', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/keys');
    await expect(page.locator('h2#device-keys')).toContainText('HmIP device keys');
    await expect(page.locator('[data-count]')).toHaveText('1 of 3 paired HmIP devices have their key here.');
    // the paired ones without a key first
    await expect(page.locator('.dk-table tbody tr').first()).toHaveAttribute('data-key', 'missing');
    await expect(row(page, '3014F711A0000A1B2C3D4E5F')).toContainText('Dimmer Flur');
    await expect(row(page, '3014F711A0000A1B2C3D4E5F')).toContainText('3014-F711-A000-0A1B-2C3D-4E5F');
    await expect(row(page, '3014F711A0000A1B2C3D4E5F')).toContainText('stored');
    // 127.0.0.1 is a secure context, so the live camera is offered (over plain HTTP to the system's
    // address it is not)
    await expect(page.locator('[data-qr="camera"]')).toBeVisible();

    // a pasted code of the thermostat
    await page.getByLabel('Device code').fill('EQ01SG3014F711A0000B1B2C3D4E5FDLK0123456789ABCDEFFEDCBA9876543210');
    await page.locator('.dk-paste').getByRole('button', {name: 'Store'}).click();
    await expect(page.locator('[data-notice="device-key-added"]')).toHaveText('Key stored for Heizung Bad.');
    await expect(page.locator('[data-count]')).toHaveText('2 of 3 paired HmIP devices have their key here.');
    await expect(page.locator('[data-notice="device-keys-pending"]')).toContainText('One key change waits for the next HmIP-RF start. Apply it before pairing the scanned device.');
    await expect(row(page, '3014F711A0000B1B2C3D4E5F')).toContainText('waits for the HmIP-RF start');
    // not a device code
    await page.getByLabel('Device code').fill('WIFI:S:Cafe;;');
    await page.locator('.dk-paste').getByRole('button', {name: 'Store'}).click();
    await expect(page.locator('[data-notice="device-key-added"]')).toContainText('That is not an HmIP device code');

    // a photo of the access point's sticker, read in the browser
    await page.locator('[data-qr="file"]').setInputFiles({name: 'sticker.png', mimeType: 'image/png', buffer: qrPNG('EQ01SG3014F711A0000D1B2C3D4E5FDLK00112233445566778899AABBCCDDEEFF')});
    await expect(page.locator('[data-notice="device-key-added"]')).toHaveText('Key stored for HmIPW-DRAP 3014-F711-A000-0D1B-2C3D-4E5F.');
    await expect(page.locator('[data-count]')).toHaveText('3 of 3 paired HmIP devices have their key here.');
    await expect(page.locator('[data-notice="device-keys-pending"]')).toContainText('2 key changes wait for the next HmIP-RF start');

    // typed from a sticker whose device is not paired: an O is caught, then the confirm
    await page.getByRole('button', {name: 'Type the SGTIN and the key'}).click();
    await page.getByLabel('SGTIN', {exact: true}).fill('3014-F711-A000-0E00-0000-0A05');
    await page.getByLabel('Key', {exact: true}).fill('014E2-PG2EB-SQQZX-Q5TL1-U58CHO');
    await expect(page.locator('[data-note="odiv"]')).toBeVisible();
    await expect(page.locator('.dk-typed').getByRole('button', {name: 'Store'})).toBeDisabled();
    await page.getByLabel('Key', {exact: true}).fill('014E2-PG2EB-SQQZX-Q5TL1-U58CHH');
    await page.locator('.dk-typed').getByRole('button', {name: 'Store'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('3014-F711-A000-0E00-0000-0A05 is not a paired HmIP device of this system');
    await dialog.getByRole('button', {name: 'Store the key'}).click();
    await expect(page.locator('[data-notice="device-key-added"]')).toHaveText('Key stored for 3014-F711-A000-0E00-0000-0A05; it is used when the device is paired.');
    await expect(page.locator('[data-count]')).toContainText('1 more for devices not paired yet.');
    await expect(row(page, '3014F711A0000E0000000A05')).toContainText('device not paired yet');

    // apply: HmIP-RF restarts, then nothing waits
    await page.getByRole('button', {name: 'Apply now (restarts HmIP-RF)'}).click();
    await expect(dialog).toContainText('HmIP-RF restarts and reads the stored keys');
    await dialog.getByRole('button', {name: 'Restart HmIP-RF'}).click();
    await expect(page.locator('[data-notice="device-keys-applying"]')).toBeVisible();
    await expect(page.locator('[data-notice="device-keys-pending"]')).toHaveCount(0, {timeout: 10_000});
    await expect(page.locator('[data-notice="device-keys-applying"]')).toHaveCount(0);

    // remove one
    await row(page, '3014F711A0000E0000000A05').getByRole('button', {name: 'Remove'}).click();
    await expect(dialog).toContainText("Without it, pairing this device needs eQ-3's key server again");
    await dialog.getByRole('button', {name: 'Remove'}).click();
    await expect(row(page, '3014F711A0000E0000000A05')).toHaveCount(0);
});

test('the key sheet asks for the password every time and shows every key as its code', async ({page, baseURL}) => {
    await own(page, baseURL);
    const exports: (string | undefined)[] = [];
    page.on('request', (r) => {
        if (r.url().endsWith('/device-keys/export')) exports.push(r.headers()['x-occulite-confirm']);
    });
    await page.goto('/system/keys');
    await page.getByRole('button', {name: 'Print the key sheet…'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('The sheet holds every HmIP device key in clear. Confirm with your login password.');
    await dialog.getByLabel('Login password (admin)').fill('wrong');
    await dialog.getByRole('button', {name: 'Confirm'}).click();
    // a wrong password asks again and keeps the session
    await expect(dialog).toContainText('The password was wrong.');
    await dialog.getByLabel('Login password (admin)').fill('labpass1');
    await dialog.getByRole('button', {name: 'Confirm'}).click();
    const sheet = page.locator('[data-sheet="keys"]');
    await expect(sheet).toBeVisible();
    await expect(sheet).toContainText('This sheet holds every HmIP device key of this system in clear.');
    await expect(sheet.locator('.ks-card')).toHaveCount(1);
    await expect(sheet.locator('.ks-card')).toContainText('Dimmer Flur');
    await expect(sheet.locator('.ks-card .ks-mono').first()).toHaveText('3014-F711-A000-0A1B-2C3D-4E5F');
    await expect(sheet.locator('.ks-card .ks-mono').nth(1)).toHaveText('014E2-PG2EB-SQQH2-8T5CY-4TQLGG');
    await expect(sheet.locator('.ks-card svg path')).toHaveCount(1);
    expect(exports).toEqual(['CONFIRMEDAAAAAAAAAAAAAAAAA']);
    await sheet.getByRole('button', {name: 'Close'}).click();
    await expect(sheet).toHaveCount(0);
    // nothing is remembered: the next sheet asks again
    await page.getByRole('button', {name: 'Print the key sheet…'}).click();
    await expect(dialog).toContainText('Confirm with your login password.');
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    await expect(page.locator('[data-sheet="keys"]')).toHaveCount(0);
});

test('an account of the identity provider confirms there and comes back to the sheet', async ({page, baseURL}) => {
    await own(page, baseURL, {'stub-confirm': 'oidc'});
    await page.goto('/system/keys');
    await page.getByRole('button', {name: 'Print the key sheet…'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('you sign in at the identity provider once more and come back here');
    await dialog.getByRole('button', {name: 'Continue'}).click();
    await expect(page.locator('[data-sheet="keys"]')).toBeVisible();
    // the ticket was in the fragment; it is gone from the address bar
    expect(new URL(page.url()).hash).toBe('');
    expect(new URL(page.url()).pathname).toBe('/system/keys');
});

test('a refused confirmation at the provider says why', async ({page, baseURL}) => {
    await own(page, baseURL, {'stub-confirm': 'oidc', 'stub-confirm-refuse': '1'});
    await page.goto('/system/keys');
    await page.getByRole('button', {name: 'Print the key sheet…'}).click();
    await page.getByRole('dialog').getByRole('button', {name: 'Continue'}).click();
    await expect(page.locator('[data-notice="device-keys-error"]')).toContainText('The confirmation at the identity provider was refused: the provider did not ask for the password again');
    await expect(page.locator('[data-sheet="keys"]')).toHaveCount(0);
});

test('in German', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/keys');
    await expect(page.locator('h2#device-keys')).toContainText('HmIP-Geräteschlüssel');
    await expect(page.locator('[data-count]')).toHaveText('1 von 3 angelernten HmIP-Geräten haben ihren Schlüssel hier.');
    await expect(page.getByRole('button', {name: 'Schlüsselblatt drucken…'})).toBeVisible();
});

test('a user sees no device keys and does not ask for them', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    let asked = false;
    await page.route('**/api/system/v1/radio/hmip/device-keys', (r) => {
        asked = true;
        return r.fulfill({status: 403, json: {error: 'forbidden', scope: 'radio:keys'}});
    });
    await page.goto('/system/keys');
    await expect(page.locator('h2').first()).toBeVisible();
    await expect(page.locator('h2#device-keys')).toHaveCount(0);
    expect(asked).toBe(false);
});
