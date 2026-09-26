import {expect, test, type Page} from '@playwright/test';
import {buttonBecomesPanel} from './panels';

// openccu-lite task 268 (the maintainer: "buttons that open panels in the page should transform to the
// panel itself (so the button is not visible anymore when a panel gets opened) ... make it consistent,
// check all buttons that show/hide panels"): every button that opens an in-page panel is hidden while
// the panel is open and comes back when it closes - the Disclosure's own buttons (Set key, Add user,
// Create token, Always allowed) and the ones a page passes it as its trigger (a section's heading
// button, a card's button). The Trust stores, the shares, Format and the LAN devices' Network
// settings are checked in their own specs.

const group = (page: Page, name: string) => page.getByRole('group', {name});

test('Radio: USB devices', async ({page}) => {
    await page.goto('/radio');
    await expect(page.locator('[data-process="rfd"]')).toBeVisible();
    await buttonBecomesPanel(page.locator('[data-usb-toggle]'), group(page, 'USB devices'), 'Close');
});

test('LAN devices: Add gateway', async ({page}) => {
    await page.goto('/system/lan-devices');
    await buttonBecomesPanel(page.locator('[data-gw-add]'), group(page, 'Add a gateway'));
});

test('Backup targets: Backups here, and Edit is gone while its form is open', async ({page}) => {
    await page.goto('/system/backup');
    const dir = page.locator('[data-target="directory"]');
    await buttonBecomesPanel(dir.locator('[data-action="backups"]'), dir.getByRole('group', {name: 'Backups here'}), 'Close');
    await dir.locator('[data-action="edit"]').click();
    await expect(dir.locator('[data-form]')).toBeVisible();
    await expect(dir.locator('[data-action="edit"]')).toHaveCount(0);
    await dir.locator('[data-action="cancel"]').click();
    await expect(dir.locator('[data-form]')).toHaveCount(0);
    await expect(dir.locator('[data-action="edit"]')).toBeVisible();
});

test('Network: the Wi-Fi QR code', async ({page}) => {
    await page.goto('/system/network');
    const wifi = page.locator('.ol-wifi');
    await wifi.getByRole('switch').check();
    await buttonBecomesPanel(wifi.locator('[data-action="wifi-qr"]'), wifi.getByRole('group', {name: 'Scan a QR code'}));
});

test("the Disclosure's own buttons: Add user, Create token, Always allowed", async ({page}) => {
    await page.goto('/system/users');
    const own = (label: string) => page.locator('.ol-disclosure-trigger button', {hasText: new RegExp(`^${label}$`)});
    await buttonBecomesPanel(own('Add user'), group(page, 'Create user'));
    await page.goto('/system/remote-access');
    await buttonBecomesPanel(own('Create token'), group(page, 'Create token'));
    await page.goto('/system/firewall');
    await buttonBecomesPanel(own('Always allowed, before the rules'), group(page, 'Always allowed'), 'Close');
});
