import {expect, test, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';
import {qrPNG} from './qrpng';

// Task 89: the Network page's layout (the maintainer, 2026-09-19) - the hostname first, then one
// half-width panel per interface with its mode - and the Wi-Fi panel: the switch, scan, connect,
// the saved networks, the preferred interface. Each test has its own Wi-Fi state (stub-wifi).
test.beforeEach(async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-wifi', value: `w-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
});

const wifi = (page: Page) => page.locator('.ol-wifi');

test('the name resolution panel under the interfaces, the panels half the width, the mode in the Ethernet column', async ({page}, info) => {
    await page.goto('/system/network');
    // the maintainer, 2026-09-20: the hostname is the Name resolution panel's, under the interfaces
    const names = page.getByRole('heading', {name: 'Name resolution'});
    const ifaces = page.getByRole('heading', {name: 'Interfaces'});
    await expect(names).toBeVisible();
    expect((await names.boundingBox())!.y).toBeGreaterThan((await ifaces.boundingBox())!.y);
    await expect(page.locator('[data-panel="name-resolution"]').getByLabel('Hostname')).toBeVisible();
    const eth = page.locator('.ol-ifaces [data-iface="eth0"]');
    // task 221: the mode is the IPv4 panel's under the Ethernet panel
    const v4 = page.locator('[data-panel="ipv4"][data-of="eth0"]');
    await expect(v4.getByRole('combobox').first()).toBeVisible();
    await expect(v4.locator('option[value="dhcp"]')).toHaveCount(1);
    await expect(wifi(page)).toBeVisible();
    const a = (await eth.boundingBox())!;
    const b = (await wifi(page).boundingBox())!;
    const grid = (await page.locator('.ol-ifaces').first().boundingBox())!;
    if (info.project.name === 'phone') {
        expect(b.y).toBeGreaterThan(a.y); // stacked
    } else {
        expect(Math.abs(a.y - b.y)).toBeLessThan(2); // side by side
        expect(a.width).toBeGreaterThan(grid.width * 0.45);
        expect(a.width).toBeLessThan(grid.width * 0.55);
    }
    expect(await fitsWindow(page)).toBe(true);
});

test('switch Wi-Fi on, scan, connect with a password, forget', async ({page}) => {
    await page.goto('/system/network');
    await expect(wifi(page).locator('.ol-pill')).toHaveText('off');
    await wifi(page).getByRole('switch').check();
    await expect(wifi(page).locator('.ol-pill')).toHaveText('no network saved');
    await wifi(page).getByRole('button', {name: 'Scan'}).click();
    const list = wifi(page).locator('[data-list="scan"] li');
    await expect(list).toHaveCount(4);
    await expect(list.first()).toContainText('FRITZ!Box 7590');
    await expect(list.first()).toContainText('WPA2/WPA3');
    // an enterprise network has no Connect
    await expect(list.filter({hasText: 'Firma'}).getByRole('button')).toHaveCount(0);
    await list.filter({hasText: 'FRITZ!Box 7590'}).getByRole('button', {name: 'Connect'}).click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('input[type=password]').fill('geheimes passwort');
    await dialog.getByRole('button', {name: 'Connect'}).click();
    await expect(wifi(page).locator('.ol-pill')).toHaveText('connected');
    await expect(wifi(page).locator('dl.wifi-conn')).toContainText('5 GHz · channel 48');
    await expect(wifi(page).locator('dl.wifi-conn')).toContainText('-58 dBm');
    const saved = wifi(page).locator('[data-list="saved"] li');
    await expect(saved).toHaveCount(1);
    await saved.getByRole('button', {name: 'Forget'}).click();
    await page.getByRole('dialog').getByRole('button', {name: 'Forget'}).click();
    await expect(saved).toHaveCount(0);
    await expect(wifi(page).locator('.ol-pill')).toHaveText('no network saved');
});

test('an open network connects without a password; the preferred interface is saved', async ({page}) => {
    await page.goto('/system/network');
    await wifi(page).getByRole('switch').check();
    await wifi(page).getByRole('button', {name: 'Scan'}).click();
    await wifi(page).locator('[data-list="scan"] li').filter({hasText: 'Cafe'}).getByRole('button', {name: 'Connect'}).click();
    await expect(wifi(page).locator('.ol-pill')).toHaveText('connected');
    await wifi(page).getByLabel('Preferred interface').selectOption('wlan');
    await wifi(page).getByRole('button', {name: 'Apply'}).click();
    await expect(wifi(page).locator('.ol-card-sub')).toContainText('preferred');
    // switched off again: the saved network stays
    await wifi(page).getByRole('switch').uncheck();
    await page.getByRole('dialog').getByRole('button', {name: 'Switch off'}).click();
    await expect(wifi(page).locator('.ol-pill')).toHaveText('off');
    await wifi(page).getByRole('switch').check();
    await expect(wifi(page).locator('[data-list="saved"] li')).toHaveCount(1);
});

test('a short password is refused with the reason', async ({page}) => {
    await page.goto('/system/network');
    await wifi(page).getByRole('switch').check();
    await wifi(page).getByRole('button', {name: 'Hidden network…'}).click();
    let dialog = page.getByRole('dialog');
    await dialog.locator('input').fill('Versteckt');
    await dialog.getByRole('button', {name: 'Next'}).click();
    dialog = page.getByRole('dialog');
    // the dialog itself asks for 8 characters at least
    await dialog.locator('input[type=password]').fill('kurz');
    await expect(dialog.getByRole('button', {name: 'Connect'})).toBeDisabled();
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    await expect(wifi(page).locator('[data-list="saved"] li')).toHaveCount(0);
});

// task 89 with task 154's scanner: a network's QR code (a router's sticker, a phone's share screen)
// brings the name and the password; after the scan the network's security is the scan's
test('a Wi-Fi QR code connects, with the password from the code', async ({page}) => {
    const posts: unknown[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().endsWith('/wifi/networks')) posts.push(JSON.parse(r.postData() ?? '{}'));
    });
    await page.goto('/system/network');
    await wifi(page).getByRole('switch').check();
    await wifi(page).getByRole('button', {name: 'Scan'}).click();
    await expect(wifi(page).locator('[data-list="scan"] li')).toHaveCount(4);
    await wifi(page).getByRole('button', {name: 'QR code…'}).click();
    const panel = wifi(page).locator('[data-panel="wifi-qr"]');
    // not a Wi-Fi code
    await panel.locator('[data-qr="file"]').setInputFiles({name: 'code.png', mimeType: 'image/png', buffer: qrPNG('EQ01SG3014F711A0000D1B2C3D4E5FDLK00112233445566778899AABBCCDDEEFF')});
    await expect(panel.locator('[data-note="wifi-qr"]')).toHaveText('That QR code is not a Wi-Fi code (WIFI:…).');
    await panel.locator('[data-qr="file"]').setInputFiles({name: 'code.png', mimeType: 'image/png', buffer: qrPNG('WIFI:T:WPA;S:FRITZ!Box 7590;P:geheim\\;es passwort;;')});
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Connect to FRITZ!Box 7590');
    await expect(dialog).toContainText('The QR code carries the name and the password of the network.');
    await dialog.getByRole('button', {name: 'Connect'}).click();
    await expect(wifi(page).locator('.ol-pill')).toHaveText('connected');
    expect(posts).toEqual([{ssid: 'FRITZ!Box 7590', security: 'wpa2-wpa3', password: 'geheim;es passwort', hidden: false}]);
    await expect(panel).toHaveCount(0);
});
