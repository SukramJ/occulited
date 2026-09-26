import {expect, test, type Page} from '@playwright/test';

// openccu-lite task 62 (the maintainer: "test setting of hostname ... without reboot and also get a
// new dhcp lease to inform dhcp server of new hostname. and it should remind users with a
// certificate that they most likely have to get/create a new one and have a button that leads to
// certificates page directly"): the note after a rename says what became of the lease, and the
// certificate reminder shows with its button when the live certificate does not name the new host.

const RENAME = (lease: Record<string, unknown>) => ({hostname: 'attic', previous: 'openccu', lease});

async function rename(page: Page, answer: Record<string, unknown>) {
    await page.route('**/api/system/v1/network', (r) => (r.request().method() === 'POST' ? r.fulfill({json: {ok: true, applied: true, pending: null, ...answer}}) : r.fallback()));
    await page.goto('/system/network');
    const input = page.locator('#net-hostname');
    await expect(input).toHaveValue('openccu');
    await input.fill('attic');
    await page.locator('.ol-field-row:has(#net-hostname)').getByRole('button', {name: 'Save'}).click();
}

test('DHCP: the note names the server and the time; the self-signed certificate reminder with its button', async ({page}) => {
    await rename(page, {rename: RENAME({renewed: true, address: '192.0.2.119', at: '2026-09-25T21:30:00Z'}), certificate: {names: ['openccu', '192.0.2.119'], fits: false, mode: 'self-signed', known: true}, acme_names: {state: 'not-in-use', names: [], previous: []}});
    await expect(page.locator('[data-notice="network"]')).toContainText('Hostname set to attic. The DHCP server was told at');
    await expect(page.locator('[data-notice="network"]')).toContainText('(192.0.2.119)');
    const cert = page.locator('[data-notice="rename-certificate"]');
    await expect(cert).toContainText("The system's own certificate still names openccu. Browsers will warn until it is renewed.");
    await expect(cert.locator('[data-action="open-certificate"]')).toHaveAttribute('href', '/system/certificates');
    await cert.locator('[data-action="open-certificate"]').click();
    await expect(page).toHaveURL(/\/system\/certificates$/);
});

test('static: nothing to tell; an ACME certificate with adapted names; none when the names fit', async ({page}) => {
    await rename(page, {rename: RENAME({renewed: false, static: true}), certificate: {names: ['openccu.home.arpa', 'openccu'], fits: false, mode: 'acme', known: true}, acme_names: {state: 'adapted', names: ['attic.home.arpa', 'attic'], previous: ['openccu.home.arpa', 'openccu']}});
    await expect(page.locator('[data-notice="network"]')).toHaveText('Hostname set to attic. Static address: no DHCP server to tell.');
    const cert = page.locator('[data-notice="rename-certificate"]');
    await expect(cert).toContainText('The certificate names openccu.home.arpa, openccu; for attic you most likely need a new one.');
    await expect(cert).toContainText('The ACME names were adapted to attic.home.arpa, attic; the certificate follows at the next renewal, or on Issue now.');
    // names set by hand
    await page.unroute('**/api/system/v1/network');
    await rename(page, {rename: RENAME({renewed: false, static: true}), certificate: {names: ['homematic.example.org'], fits: false, mode: 'acme', known: true}, acme_names: {state: 'set-by-hand', names: ['homematic.example.org'], previous: ['homematic.example.org']}});
    await expect(page.locator('[data-notice="rename-certificate"]')).toContainText('The ACME names were set by hand and stay: homematic.example.org.');
    // a certificate that names the new host already: no reminder
    await page.unroute('**/api/system/v1/network');
    await rename(page, {rename: RENAME({renewed: false, static: true}), certificate: {names: ['attic.home.arpa', 'attic'], fits: true, mode: 'acme', known: true}, acme_names: {state: 'fitting', names: ['attic.home.arpa', 'attic'], previous: ['attic.home.arpa', 'attic']}});
    await expect(page.locator('[data-notice="network"]')).toContainText('Hostname set to attic.');
    await expect(page.locator('[data-notice="rename-certificate"]')).toHaveCount(0);
});

test('a lease that could not be renewed is said, the name set all the same; German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.route('**/api/system/v1/network', (r) => (r.request().method() === 'POST' ? r.fulfill({json: {ok: true, applied: true, pending: null, rename: RENAME({renewed: false, error: 'systemctl reload occu-network: exit 1'}), certificate: {names: ['openccu'], fits: false, mode: 'self-signed', known: true}}}) : r.fallback()));
    await page.goto('/system/network');
    await page.locator('#net-hostname').fill('attic');
    await page.locator('.ol-field-row:has(#net-hostname)').getByRole('button', {name: 'Speichern'}).click();
    await expect(page.locator('[data-notice="network"]')).toContainText('Hostname auf attic gesetzt. Die DHCP-Adresse konnte nicht unter dem neuen Namen erneuert werden: systemctl reload occu-network: exit 1 Bis zum nächsten Start kennt der Server das System als openccu.');
    await expect(page.locator('[data-notice="rename-certificate"]')).toContainText('Das eigene Zertifikat des Systems nennt noch openccu.');
    await expect(page.locator('[data-action="open-certificate"]')).toHaveText('Zertifikatseinstellungen öffnen');
});
