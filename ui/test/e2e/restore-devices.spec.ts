import {expect, test, type Page} from '@playwright/test';

// openccu-lite task 251 (the maintainer: "import paired devices that fetches everything needed
// that devices pairings (hmip, bidcos-rf and -wired) and possibly keys that are present are
// imported"): on the Backup page, after a checked CCU backup, what it holds of the three radios and
// the import onto a system without paired devices - refused with the reason when devices are
// paired here or an interface did not answer, a security key asked when one is involved, the danger
// question, then the POST and the reboot notice.

const CHECK = {ok: true, output: '1) Checking sbk backup file consistency:\n   generated on 3.89.11.20260919, applying to 3.89.11.20260919, OK\n   backup or system NOT protected by security key, OK', backup_version: '3.89.11.20260919', running_version: '3.89.11.20260919', needs_key: false, has_rega: true};
const BACKUP = {
    bidcos_rf: {devices: 2, address: '0xFF1234', serial: '1709ADFA00', has_key: false, gateways: 1},
    hmip: {devices: 3, identity_sgtin: '3014F711A0001F58A9A728D4', local_key: true, device_key_map: true},
    bidcos_wired: {devices: 1, gateways: 0},
    key_index: 0,
    files: ['ids', 'rfd/JEQ0230153.dev'],
    version: '3.89.11.20260919',
};
const FREE = {paired: {'BidCos-RF': {devices: 0, known: true}, 'HmIP-RF': {devices: 0, known: true}}, devices: 0, unknown: [], user_key: false, importable: true};

async function upload(page: Page) {
    await page.locator('input[type="file"][accept=".sbk,.age"]').setInputFiles({name: 'ccu.sbk', mimeType: 'application/octet-stream', buffer: Buffer.from('ustar')});
    await page.getByRole('button', {name: 'Check', exact: true}).click();
}

test('the backup\'s devices, a free system, the import asks in red and posts the file, then the reboot notice', async ({page}) => {
    await page.route('**/api/system/v1/restore/check', (r) => r.fulfill({json: {file: 'restore-ccu.sbk', check: CHECK, encryption: {encrypted: false}}}));
    await page.route('**/api/system/v1/restore/devices?**', (r) => r.fulfill({json: {backup: BACKUP, target: FREE}}));
    const posts: unknown[] = [];
    await page.route('**/api/system/v1/restore/import-devices', (r) => {
        posts.push(JSON.parse(r.request().postData() ?? '{}'));
        return r.fulfill({json: {ok: true, imported: {backup: BACKUP, written: ['ids'], aside: '/etc/config/.import-devices-aside/x', key_set: false}, rebooting: true}});
    });
    await page.goto('/system/backup');
    await upload(page);
    const block = page.locator('[data-restore="devices"]');
    await expect(block.getByRole('heading', {level: 3})).toContainText('Paired devices in this backup');
    await expect(block.locator('[data-devices="bidcos-rf"]')).toHaveText('2 devices · address 0xFF1234 · 1 LAN gateways');
    await expect(block.locator('[data-devices="hmip"]')).toHaveText('3 devices · identity of module 3014F711A0001F58A9A728D4 · local key mode · device key map');
    await expect(block.locator('[data-devices="wired"]')).toHaveText('1 devices');
    await expect(block.locator('[data-devices-target="free"]')).toContainText('This system has no paired devices');
    await expect(block.locator('[data-input="devices-key"]')).toHaveCount(0);
    const button = block.locator('[data-action="import-devices"]');
    await expect(button).toBeEnabled();
    await button.click();
    const dlg = page.getByRole('dialog');
    await expect(dlg).toContainText('2 BidCos-RF devices, 3 HmIP devices, 1 BidCos-Wired devices');
    await expect(dlg).toContainText('adapter exchange');
    await expect(dlg.getByRole('button', {name: 'Import and reboot'})).toHaveClass(/danger/);
    await dlg.getByRole('button', {name: 'Cancel'}).click();
    expect(posts).toHaveLength(0);
    await button.click();
    await page.getByRole('dialog').getByRole('button', {name: 'Import and reboot'}).click();
    await expect(page.locator('.ol-restorenotice')).toContainText('the system reboots now');
    await expect(page.locator('.ol-restorenotice pre')).toContainText('The paired devices are imported.');
    expect(posts).toEqual([{file: 'restore-ccu.sbk', key: ''}]);
});

test('refused with devices paired here, waiting while an interface is silent, the key asked when one is involved', async ({page}) => {
    await page.route('**/api/system/v1/restore/check', (r) => r.fulfill({json: {file: 'restore-ccu.sbk', check: CHECK, encryption: {encrypted: false}}}));
    let target: Record<string, unknown> = {paired: {'BidCos-RF': {devices: 2, known: true}, 'HmIP-RF': {devices: 1, known: true}}, devices: 3, unknown: [], user_key: false, importable: false};
    let backup: Record<string, unknown> = BACKUP;
    await page.route('**/api/system/v1/restore/devices?**', (r) => r.fulfill({json: {backup, target}}));
    await page.goto('/system/backup');
    await upload(page);
    const block = page.locator('[data-restore="devices"]');
    await expect(block.locator('[data-devices-target="paired"]')).toContainText('This system has 3 devices paired (BidCos-RF: 2, HmIP-RF: 1): the import is refused');
    await expect(block.locator('[data-action="import-devices"]')).toBeDisabled();
    // an interface that did not answer
    target = {paired: {'BidCos-RF': {devices: 0, known: false, error: 'no answer'}}, devices: 0, unknown: ['BidCos-RF'], user_key: false, importable: false};
    await upload(page);
    await expect(block.locator('[data-devices-target="unknown"]')).toContainText('BidCos-RF did not answer');
    await expect(block.locator('[data-action="import-devices"]')).toBeDisabled();
    // a backup with an individual security key onto a free system: the key is asked, the button
    // waits for it
    target = FREE;
    backup = {...BACKUP, key_index: 2, bidcos_rf: {...BACKUP.bidcos_rf, has_key: true}};
    await upload(page);
    await expect(block.locator('[data-devices="bidcos-rf"]')).toContainText('an individual security key');
    await expect(block.locator('[data-action="import-devices"]')).toBeDisabled();
    await block.locator('[data-input="devices-key"]').fill('secret1');
    await expect(block.locator('[data-action="import-devices"]')).toBeEnabled();
    // nothing to import
    backup = {bidcos_rf: {devices: 0, has_key: false, gateways: 0}, hmip: {devices: 0, local_key: false, device_key_map: false}, bidcos_wired: {devices: 0, gateways: 0}, key_index: 0, files: []};
    await upload(page);
    await expect(block.locator('[data-devices="none"]')).toContainText('None: the backup holds no paired device');
    await expect(block.locator('[data-action="import-devices"]')).toHaveCount(0);
});

test('German: the heading and the refusal', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.route('**/api/system/v1/restore/check', (r) => r.fulfill({json: {file: 'restore-ccu.sbk', check: CHECK, encryption: {encrypted: false}}}));
    await page.route('**/api/system/v1/restore/devices?**', (r) => r.fulfill({json: {backup: BACKUP, target: {paired: {'HmIP-RF': {devices: 1, known: true}}, devices: 1, unknown: [], user_key: false, importable: false}}}));
    await page.goto('/system/backup');
    await page.locator('input[type="file"][accept=".sbk,.age"]').setInputFiles({name: 'ccu.sbk', mimeType: 'application/octet-stream', buffer: Buffer.from('ustar')});
    await page.getByRole('button', {name: 'Prüfen', exact: true}).click();
    const block = page.locator('[data-restore="devices"]');
    await expect(block.getByRole('heading', {level: 3})).toContainText('Angelernte Geräte in dieser Sicherung');
    await expect(block.locator('[data-devices="hmip"]')).toContainText('3 Geräte · Identität des Funkmoduls');
    await expect(block.locator('[data-devices-target="paired"]')).toContainText('An diesem System sind 1 Geräte angelernt (HmIP-RF: 1)');
    await expect(block.locator('[data-action="import-devices"]')).toHaveText('Angelernte Geräte importieren und neu starten');
});
