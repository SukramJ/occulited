import {expect, test, type Page} from '@playwright/test';
import fs from 'node:fs';
import {Box, buildBinary, removeBinary} from './realbox';

// openccu-lite task 262: security keys and passkeys, end to end against the real occulited with
// Chromium's virtual authenticator (CDP WebAuthn.addVirtualAuthenticator: a CTAP2 key with resident
// keys and user verification). The page is opened on http://localhost:<port> - the RP ID must be a
// name, and localhost is the one a development daemon accepts - through the real login page: a
// key is added behind the password confirmation, the next login asks for the key after the
// password, the passkey signs in alone, the key is removed, the password alone signs in again. On
// an address (127.0.0.1) the account page says a key cannot be made there.
//
// The virtual authenticator answers every ceremony at once - the login page's conditional passkey
// offer included, which a real browser completes only when the user picks a credential. So the
// password-then-key flow runs with a plain second-factor key (no resident credential: the offer
// finds nothing and the form stays), and the passkey flows with a passkey-capable authenticator
// that is attached only for them (detached, its credentials are kept through CDP and restored).

test.beforeAll(async ({}, workerInfo) => buildBinary(workerInfo));
test.afterAll(removeBinary);
test.skip(({browserName}) => browserName !== 'chromium', 'the virtual authenticator is Chromium\'s');

let box: Box;
const PASSWORD = 'webauthn-pass-1';

test.beforeEach(async () => {
    box = new Box();
    await box.start();
    const setup = await fetch(`${box.url}/api/auth/v1/setup`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({username: 'admin', password: PASSWORD})});
    const body = await setup.text();
    expect(setup.status, body).toBe(200);
    await fetch(`${box.url}/api/auth/v1/logout`, {method: 'POST', headers: {Cookie: `occulite_session=${JSON.parse(body).sid}`, 'X-Occulite-Request': '1'}});
});

test.afterEach(async () => {
    await box.stop('SIGKILL');
    fs.rmSync(box.dir, {recursive: true, force: true});
});

function byName() {
    return box.url.replace('127.0.0.1', 'localhost');
}

interface Authenticator {
    /** removes the authenticator, keeping its credentials for attach */
    detach(): Promise<void>;
    /** adds an authenticator with the saved credentials */
    attach(): Promise<void>;
}

/** passkey: a platform authenticator with resident credentials and user verification; else a plain second-factor key */
function options(passkey: boolean) {
    return passkey
        ? {protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true}
        : {protocol: 'ctap2', transport: 'usb', hasResidentKey: false, hasUserVerification: false, automaticPresenceSimulation: true};
}

async function virtualAuthenticator(page: Page, passkey = true): Promise<Authenticator> {
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('WebAuthn.enable');
    const AUTHENTICATOR = options(passkey);
    let id = ((await cdp.send('WebAuthn.addVirtualAuthenticator', {options: AUTHENTICATOR})) as {authenticatorId: string}).authenticatorId;
    let saved: unknown[] = [];
    return {
        async detach() {
            if (!id) return;
            saved = ((await cdp.send('WebAuthn.getCredentials', {authenticatorId: id})) as {credentials: unknown[]}).credentials;
            await cdp.send('WebAuthn.removeVirtualAuthenticator', {authenticatorId: id});
            id = '';
        },
        async attach() {
            if (id) return;
            id = ((await cdp.send('WebAuthn.addVirtualAuthenticator', {options: AUTHENTICATOR})) as {authenticatorId: string}).authenticatorId;
            for (const c of saved) await cdp.send('WebAuthn.addCredential', {authenticatorId: id, credential: c});
        },
    };
}

async function logoutByFetch(page: Page) {
    await page.evaluate(() => fetch('/api/auth/v1/logout', {method: 'POST', headers: {'X-Occulite-Request': '1', 'Content-Type': 'application/json'}, body: '{}'}));
}

async function loginWithPassword(page: Page, base: string) {
    await page.goto(`${base}/login`);
    const form = page.locator('form.ol-card');
    await expect(form).toBeVisible();
    await page.locator('input:not([type=password])').first().fill('admin');
    await page.locator('input[type=password]').fill(PASSWORD);
    await page.getByRole('button', {name: /^(Login|Anmelden)$/}).click();
}

async function expectLoggedIn(page: Page) {
    await expect(page.locator('[data-user-menu], .ol-user, header')).toBeVisible();
    await expect.poll(async () => (await page.evaluate(() => fetch('/api/auth/v1/state').then((r) => r.json()))).authenticated).toBe(true);
}

async function addKey(page: Page, name: string, expectCount = 1) {
    await page.goto(`${byName()}/account`);
    const card = page.locator('[data-section="security-keys"]');
    await expect(card).toBeVisible();
    await card.locator('[data-add-key]').click();
    // the name
    const dialog = page.locator('[role=dialog]');
    await dialog.locator('input').fill(name);
    await dialog.getByRole('button', {name: /Continue|Weiter/}).click();
    // the confirmation with the password
    await dialog.locator('input[type=password]').fill(PASSWORD);
    await dialog.getByRole('button', {name: /Confirm|Bestätigen/}).click();
    await expect(card.locator('tbody tr')).toHaveCount(expectCount);
    await expect(card.locator('tbody tr', {hasText: name})).toHaveCount(1);
}

async function removeKey(page: Page, name: string) {
    const card = page.locator('[data-section="security-keys"]');
    await card.locator('tbody tr', {hasText: name}).getByRole('button', {name: /^(Remove|Entfernen)$/}).click();
    const dialog = page.locator('[role=dialog]');
    await dialog.getByRole('button', {name: /^(Remove|Entfernen)$/}).click();
    await dialog.locator('input[type=password]').fill(PASSWORD);
    await dialog.getByRole('button', {name: /Confirm|Bestätigen/}).click();
    await expect(card.locator('tbody tr', {hasText: name})).toHaveCount(0);
}

test('a key is added, asked for after the password, signs in alone as a passkey, and is removed', async ({page}) => {
    // a plain second-factor key first (a USB key without a PIN): the password stays the first step
    const usb = await virtualAuthenticator(page, false);
    await loginWithPassword(page, byName());
    await expectLoggedIn(page);
    await addKey(page, 'blue key');
    const card = page.locator('[data-section="security-keys"]');
    await expect(card.locator('tbody tr').first()).toContainText(/second factor|zweiter Faktor/);
    await expect(card, 'one key: the hint to add a second').toContainText(/second one|zweiten/);
    // and no passkey exists yet: the login page has no passkey button
    await logoutByFetch(page);
    await page.goto(`${byName()}/login`);
    await expect(page.locator('form.ol-card')).toBeVisible();
    await expect(page.locator('[data-passkey]')).toHaveCount(0);

    // the password alone opens nothing now: the key step follows and the key answers it
    await page.locator('input:not([type=password])').first().fill('admin');
    await page.locator('input[type=password]').fill(PASSWORD);
    // the virtual key answers within milliseconds, so the step's page is not waited for by eye:
    // the key step's request is, and the password's answer must have been the second factor
    const [passwordAnswer, keyStep] = await Promise.all([
        page.waitForResponse((r) => r.url().endsWith('/api/auth/v1/login') && r.request().method() === 'POST'),
        page.waitForResponse((r) => r.url().endsWith('/api/auth/v1/login/webauthn')),
        page.getByRole('button', {name: /^(Login|Anmelden)$/}).click(),
    ]);
    expect((await passwordAnswer.json()).second_factor).toBe('webauthn');
    expect(keyStep.status()).toBe(200);
    await expectLoggedIn(page);
    expect((await page.evaluate(() => fetch('/api/auth/v1/state').then((r) => r.json()))).method).toBe('password');
    // the key's last use is on the card
    await page.goto(`${byName()}/account`);
    await expect(card.locator('tbody tr').first()).not.toContainText(/never|nie/);

    // a passkey (the phone) as the second key; the USB key goes away for the passkey flows
    await usb.detach();
    const phone = await virtualAuthenticator(page, true);
    await addKey(page, 'phone', 2);
    await expect(card.locator('tbody tr', {hasText: 'phone'})).toContainText(/passkey/i);
    await expect(card).not.toContainText(/second one|zweiten/);

    // the passkey signs in alone, through the button: the login page is opened without the
    // authenticator (its offer in the name field would sign in by itself with one), then the
    // phone comes and the button is pressed
    await logoutByFetch(page);
    await phone.detach();
    await page.goto(`${byName()}/login`);
    await expect(page.locator('[data-passkey]')).toBeVisible();
    await phone.attach();
    if (await page.locator('[data-passkey]').isVisible()) await page.locator('[data-passkey]').click();
    await expectLoggedIn(page);
    expect((await page.evaluate(() => fetch('/api/auth/v1/state').then((r) => r.json()))).method).toBe('passkey');

    // removal asks for the password again; without keys the password alone signs in
    await page.goto(`${byName()}/account`);
    await removeKey(page, 'phone');
    await removeKey(page, 'blue key');
    await expect(card).toContainText(/No security key yet|Noch kein Sicherheitsschlüssel/);
    await logoutByFetch(page);
    await loginWithPassword(page, byName());
    await expectLoggedIn(page);
    await expect(page.locator('[data-key-step]')).toHaveCount(0);
});

test('the passkey signs in through the browser\'s offer in the name field', async ({page}) => {
    const key = await virtualAuthenticator(page);
    await loginWithPassword(page, byName());
    await expectLoggedIn(page);
    await addKey(page, 'phone');
    await logoutByFetch(page);
    // the authenticator stays: the conditional offer completes as a user picking the passkey would
    void key;
    await page.goto(`${byName()}/login`);
    await expectLoggedIn(page);
    expect((await page.evaluate(() => fetch('/api/auth/v1/state').then((r) => r.json()))).method).toBe('passkey');
});

test('a wrong password does not reach the key step', async ({page}) => {
    const key = await virtualAuthenticator(page);
    await loginWithPassword(page, byName());
    await expectLoggedIn(page);
    await addKey(page, 'blue');
    await logoutByFetch(page);
    await key.detach();
    await page.goto(`${byName()}/login`);
    await page.locator('input:not([type=password])').first().fill('admin');
    await page.locator('input[type=password]').fill('not-the-password');
    await page.getByRole('button', {name: /^(Login|Anmelden)$/}).click();
    await expect(page.locator('.ol-notice.error')).toBeVisible();
    await expect(page.locator('[data-key-step]')).toHaveCount(0);
});

test('on an address the account page says a key cannot be made there', async ({page}) => {
    await loginWithPassword(page, box.url); // 127.0.0.1
    await expectLoggedIn(page);
    await page.goto(`${box.url}/account`);
    const card = page.locator('[data-section="security-keys"]');
    await expect(card.locator('[data-keys-wrong-place]')).toBeVisible();
    await expect(card.locator('[data-add-key]')).toHaveCount(0);
    // and the API refuses a registration on the address (409 webauthn-needs-name)
    const r = await page.evaluate(async () => {
        const t = await fetch('/api/auth/v1/ticket', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-Occulite-Request': '1'}, body: JSON.stringify({path: '/api/auth/v1/me/webauthn', confirm: true, password: 'webauthn-pass-1'})}).then((x) => x.json());
        const b = await fetch('/api/auth/v1/me/webauthn/begin', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-Occulite-Request': '1', 'X-Occulite-Confirm': t.ticket}, body: JSON.stringify({name: 'x'})});
        return {status: b.status, body: await b.json()};
    });
    expect(r.status).toBe(409);
    expect(r.body.error).toBe('webauthn-needs-name');
});
