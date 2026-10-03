import {expect, test, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';
import {buttonBecomesPanel} from './panels';

// openccu-lite task 232 (the maintainer, 2026-10-03: "only OIDC and ACME ... per pin, the admin
// chooses whether the pin is checked in addition to the CA validation (the default) or instead of
// it ... Pin the current certificate after a successful check, with the fingerprint shown to
// confirm; a mismatch fails loudly with the presented fingerprint and Re-pin"): the Pinned keys of
// the OAuth / OIDC and the ACME store on the Trust stores page - Pin the current certificate from
// the fetched chain with the fingerprint and key hash to confirm, pin only in red, a backup pin by
// hash, remove; the mismatch panel with the presented fingerprint and Re-pin; the Status warning;
// the OIDC settings' Pin the current certificate after a passed test; German; a phone.

async function own(page: Page, baseURL: string | undefined, opts: {failure?: boolean; anchor?: string} = {}) {
    const cookies = [{name: 'stub-trust', value: `tp-${Math.random().toString(36).slice(2)}`, url: baseURL!}];
    if (opts.failure) cookies.push({name: 'stub-trust-pin-failure', value: '1', url: baseURL!});
    await page.context().addCookies(cookies);
    await page.goto('/system/trust' + (opts.anchor ?? '#oidc'));
    await expect(page.locator('[data-trust-store]')).toBeVisible();
}
const pins = (page: Page, id: string) => page.locator(`[data-pins="${id}"]`);
const tab = (page: Page, id: string) => page.getByRole('tablist', {name: 'Trust stores'}).locator(`[data-tab="${id}"]`);

test('the two anchor stores have Pinned keys, the other two do not; the panel grows from the button', async ({page, baseURL}) => {
    await own(page, baseURL);
    const p = pins(page, 'oidc');
    await expect(p.getByRole('heading', {level: 2, name: 'Pinned keys'})).toBeVisible();
    await expect(p.locator('[data-pins-empty]')).toHaveText('No pin: the CA validation alone applies.');
    await expect(p.locator('[data-pin-current="oidc"]')).toHaveText('Pin the current certificate…');
    await expect(p.locator('[data-pin-backup="oidc"]')).toHaveText('Add a backup pin…');
    await tab(page, 'acme').click();
    await expect(pins(page, 'acme')).toBeVisible();
    for (const id of ['system', 'occulited']) {
        await tab(page, id).click();
        await expect(page.locator(`[data-trust-store="${id}"]`)).toBeVisible();
        await expect(page.locator('[data-pins]')).toHaveCount(0);
    }
    await tab(page, 'oidc').click();
    await buttonBecomesPanel(p.locator('[data-pin-backup="oidc"]'), p.locator('.ol-disclosure'));
    expect(await fitsWindow(page)).toBe(true);
});

test('Pin the current certificate: the fetched chain, the leaf chosen, the fingerprint and key hash to confirm, the row', async ({page, baseURL}) => {
    await own(page, baseURL);
    const p = pins(page, 'oidc');
    await p.locator('[data-pin-current="oidc"]').click();
    const panel = p.locator('.ol-disclosure');
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('Pin the current certificate');
    // the chain: the provider's own certificate first (chosen), its CA second; the state line
    await expect(panel.locator('[data-pins-peer-state]')).toContainText('auth.example.org: the connection does not verify now');
    const certs = panel.locator('[data-pin-peer]');
    await expect(certs).toHaveCount(2);
    await expect(certs.nth(0)).toContainText('auth.example.org');
    await expect(certs.nth(0)).toContainText('server certificate');
    await expect(certs.nth(0).locator('[data-peer-fingerprint]')).toContainText('AA:11:00');
    await expect(certs.nth(0).locator('[data-peer-spki]')).toContainText('LeafKeyHash');
    await expect(certs.nth(0).getByRole('radio')).toBeChecked();
    await expect(certs.nth(1)).toContainText('Lab CA');
    await expect(certs.nth(1)).toContainText('certificate authority');
    // the default mode is with the CA check; Pin asks with both hashes, not in red
    await expect(panel.getByRole('radio', {name: /In addition to the CA validation/})).toBeChecked();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/trust/oidc/pins')),
        (async () => {
            await panel.locator('[data-action="pin"]').click();
            const dialog = page.getByRole('dialog');
            await expect(dialog).toContainText('Pin auth.example.org for the OAuth / OIDC store?');
            await expect(dialog).toContainText('The CA validation still applies as well.');
            await expect(dialog).toContainText('SHA-256 of the certificate: AA:11:00');
            await expect(dialog).toContainText('SHA-256 of the public key: LeafKeyHash');
            const confirm = dialog.getByRole('button', {name: 'Pin', exact: true});
            await expect(confirm).not.toHaveClass(/danger/);
            await confirm.click();
        })(),
    ]);
    expect(req.postDataJSON()).toMatchObject({mode: 'with-ca', replace: false});
    expect(req.postDataJSON().pem).toContain('BEGIN CERTIFICATE');
    await expect(p.locator('[data-notice="pins-oidc"]')).toHaveText('auth.example.org is pinned for the OAuth / OIDC store (with the CA check).');
    await expect(panel).toHaveCount(0);
    const row = p.locator('[data-pin]');
    await expect(row).toHaveCount(1);
    await expect(row).toContainText('auth.example.org');
    await expect(row.locator('[data-pin-spki]')).toHaveText('LeafKeyHash0…');
    await expect(row.locator('[data-pin-mode="with-ca"]')).toHaveText('with the CA check');
    await expect(row).toContainText('admin');
    // the same key again: present, the panel says pinned and offers the CA instead
    await p.locator('[data-pin-current="oidc"]').click();
    await expect(p.locator('[data-pin-peer="0"] [data-badge="pinned"]')).toHaveText('pinned');
    await expect(p.locator('[data-pin-peer="0"] input')).toBeDisabled();
    await expect(p.locator('[data-action="pin"]')).toBeDisabled();
    await p.locator('[data-pin-peer="1"] input').check();
    await expect(p.locator('[data-action="pin"]')).toBeEnabled();
    expect(await fitsWindow(page)).toBe(true);
});

test('pin only is the danger path: the button and the question in red, the row says pin only; the last pin removed says the CA check applies again', async ({page, baseURL}) => {
    await own(page, baseURL, {anchor: '#acme'});
    const p = pins(page, 'acme');
    await p.locator('[data-pin-current="acme"]').click();
    const panel = p.locator('.ol-disclosure');
    await expect(panel.locator('[data-pin-peer="0"]')).toContainText('ca.lan');
    await panel.locator('[data-pin-mode-only]').check();
    await expect(panel.locator('[data-action="pin"]')).toHaveClass(/danger/);
    await panel.locator('[data-action="pin"]').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Pin only: the CA validation and the name check are skipped');
    await expect(dialog).toContainText('SHA-256 of the public key: StepCALeafKeyHash');
    const confirm = dialog.getByRole('button', {name: 'Pin', exact: true});
    await expect(confirm).toHaveClass(/danger/);
    await confirm.click();
    await expect(p.locator('[data-notice="pins-acme"]')).toHaveText('ca.lan is pinned for the ACME store (pin only).');
    const row = p.locator('[data-pin]');
    await expect(row.locator('[data-pin-mode="only"]')).toHaveText('pin only');
    // the peer now verifies by the pin alone
    await p.locator('[data-pin-current="acme"]').click();
    await expect(panel.locator('[data-pins-peer-state]')).toHaveText('ca.lan: the connection verifies now - a pin alone vouches.');
    await panel.getByRole('button', {name: 'Cancel', exact: true}).click();
    // remove: the last pin, so the CA check alone applies again
    await row.locator('[data-action="pin-remove"]').click();
    await expect(dialog).toContainText('ca.lan (StepCALeafKe…) is no longer pinned for the ACME store.');
    await expect(dialog).toContainText('It is the last pin: the CA validation alone applies again.');
    await dialog.getByRole('button', {name: 'Remove'}).click();
    await expect(p.locator('[data-pins-empty]')).toBeVisible();
});

test('a backup pin by hash: hex with colons is taken, a malformed one keeps Pin off; the Certificate page counts it', async ({page, baseURL}) => {
    await own(page, baseURL, {anchor: '#acme'});
    const p = pins(page, 'acme');
    await p.locator('[data-pin-backup="acme"]').click();
    const panel = p.locator('.ol-disclosure');
    await expect(panel).toContainText('Add a backup pin');
    const field = panel.locator('[data-pin-hash]');
    await field.fill('not a hash');
    await expect(panel.locator('[data-action="pin"]')).toBeDisabled();
    await field.fill('AB:CD:' + 'EF:'.repeat(29) + '01');
    await expect(panel.locator('[data-action="pin"]')).toBeEnabled();
    await panel.locator('[data-action="pin"]').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Pin the key hash for the ACME store?');
    await dialog.getByRole('button', {name: 'Pin', exact: true}).click();
    const row = p.locator('[data-pin]');
    await expect(row).toHaveCount(1);
    await expect(row).toContainText('Key hash (backup pin)');
    await expect(row.locator('[data-pin-spki]')).toHaveText('q83v7+/v7+/v…');
    // the Certificate page's ACME list (under the custom directory's CA root field) counts the pins
    await page.goto('/system/certificates?mode=acme');
    await page.locator('.ol-certform select').first().selectOption('custom');
    await expect(page.locator('[data-acme-pins]')).toHaveText('Pinned keys: 1.');
});

test('a mismatch: the panel names the host and the presented fingerprint, Re-pin replaces the stale pin; the Status warning links here', async ({page, baseURL}) => {
    await own(page, baseURL, {failure: true, anchor: '#system'});
    const failure = page.locator('[data-trust-pin-failure="auth.example.org"]');
    await expect(failure).toContainText("auth.example.org presents a certificate none of the OAuth / OIDC store's pins match.");
    await expect(failure).toContainText('The connection fails until the key is pinned here or the server presents a pinned key again.');
    await expect(failure.locator('[data-presented-fingerprint]')).toHaveText('AA:11:' + '00:'.repeat(29) + '01');
    await expect(failure.locator('[data-presented-spki]')).toContainText('LeafKeyHash');
    // Re-pin: the OIDC tab opens with the panel in replace mode, the question says so, in the stale pin's mode
    await failure.locator('[data-action="trust-repin"]').click();
    await expect(tab(page, 'oidc')).toHaveAttribute('aria-selected', 'true');
    const p = pins(page, 'oidc');
    await expect(p.locator('[data-pin]')).toHaveCount(1);
    await expect(p.locator('[data-pin]')).toContainText('EE:55:00');
    const panel = p.locator('.ol-disclosure');
    await expect(panel).toContainText('Re-pin: the key the server presents now');
    await expect(panel.locator('[data-pin-replace]')).toBeChecked();
    await expect(panel.locator('[data-pin-peer="0"] input')).toBeChecked();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/trust/oidc/pins')),
        (async () => {
            await panel.locator('[data-action="pin"]').click();
            const dialog = page.getByRole('dialog');
            await expect(dialog).toContainText('The 1 pins of the OAuth / OIDC store are replaced by this one.');
            await expect(dialog).toContainText('SHA-256 of the certificate: AA:11:00');
            await dialog.getByRole('button', {name: 'Re-pin', exact: true}).click();
        })(),
    ]);
    expect(req.postDataJSON()).toMatchObject({mode: 'with-ca', replace: true});
    await expect(p.locator('[data-notice="pins-oidc"]')).toHaveText('The OAuth / OIDC store pins auth.example.org now; the earlier pins are gone.');
    await expect(p.locator('[data-pin]')).toHaveCount(1);
    await expect(p.locator('[data-pin]')).toContainText('AA:11:00');
    await expect(page.locator('[data-trust-pin-failure]')).toHaveCount(0);
    // the Status page's warning, before the fix, in another browser state
    await page.context().clearCookies();
    await page.context().addCookies([{name: 'stub-trust', value: `tp-${Math.random().toString(36).slice(2)}`, url: baseURL!}, {name: 'stub-trust-pin-failure', value: '1', url: baseURL!}]);
    await page.goto('/');
    const warning = page.locator('[data-warnings] [data-notice="trust-pin"]');
    await expect(warning).toContainText("auth.example.org presents a certificate none of the OAuth / OIDC store's pins match.");
    await expect(warning).toContainText('Presented: CN=auth.example.org, SHA-256 AA:11:00');
    await expect(warning.getByRole('link', {name: 'Trust stores'})).toHaveAttribute('href', '/system/trust#oidc');
    await warning.getByRole('link', {name: 'Trust stores'}).click();
    await expect(tab(page, 'oidc')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('[data-trust-pin-failure="auth.example.org"]')).toBeVisible();
});

test('the OIDC settings: Pin the current certificate after a passed test, the pinned key named afterwards', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-trust', value: `tp-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
    await page.goto('/system/users');
    const s = page.locator('[data-oidc-trust]');
    await expect(s).toBeVisible();
    const p = s.locator('[data-pins="oidc"]');
    await expect(p.getByRole('heading', {level: 3, name: 'Pinned keys'})).toBeVisible();
    // the test fails before a CA is trusted: no pin offer on a lost handshake
    await s.locator('[data-action="oidc-test"]').click();
    await expect(s.locator('[data-oidc-test="failed"]')).toBeVisible();
    await expect(s.locator('[data-action="oidc-pin-current"]')).toHaveCount(0);
    // trust the CA, the test passes and offers the pin; the offer opens the pin panel on the leaf
    await s.locator('[data-oidc-trust-pem]').fill('-----BEGIN CERTIFICATE-----\nMIIBca\n-----END CERTIFICATE-----\n');
    await s.getByRole('button', {name: 'Trust', exact: true}).click();
    await expect(s.locator('[data-anchor]')).toHaveCount(1);
    await s.locator('[data-action="oidc-test"]').click();
    await expect(s.locator('[data-oidc-test="ok"]')).toBeVisible();
    await s.locator('[data-action="oidc-pin-current"]').click();
    const panel = p.locator('.ol-disclosure');
    await expect(panel.locator('[data-pin-peer="0"]')).toContainText('auth.example.org');
    await panel.locator('[data-action="pin"]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Pin', exact: true}).click();
    await expect(p.locator('[data-pin]')).toHaveCount(1);
    // the test names the pin now
    await s.locator('[data-action="oidc-test"]').click();
    await expect(s.locator('[data-oidc-test="ok"] [data-oidc-test-pinned]')).toHaveText('The pinned key matched (with the CA check).');
    await expect(s.locator('[data-action="oidc-pin-current"]')).toHaveCount(0);
    expect(await fitsWindow(page)).toBe(true);
});

test('German: the heading, the buttons, the mode words and the question', async ({page, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await own(page, baseURL);
    const p = pins(page, 'oidc');
    await expect(p.getByRole('heading', {level: 2, name: 'Gepinnte Schlüssel'})).toBeVisible();
    await expect(p.locator('[data-pins-empty]')).toHaveText('Kein Pin: Allein die CA-Prüfung gilt.');
    await expect(p.locator('[data-pin-current="oidc"]')).toHaveText('Aktuelles Zertifikat pinnen…');
    await expect(p.locator('[data-pin-backup="oidc"]')).toHaveText('Backup-Pin hinzufügen…');
    await p.locator('[data-pin-current="oidc"]').click();
    const panel = p.locator('.ol-disclosure');
    await expect(panel.getByRole('radio', {name: /Zusätzlich zur CA-Prüfung/})).toBeChecked();
    await panel.locator('[data-pin-mode-only]').check();
    await panel.locator('[data-action="pin"]').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Nur Pin: Die CA-Prüfung und die Namensprüfung entfallen');
    await dialog.getByRole('button', {name: 'Pinnen', exact: true}).click();
    await expect(p.locator('[data-pin] [data-pin-mode="only"]')).toHaveText('nur Pin');
    expect(await fitsWindow(page)).toBe(true);
});
