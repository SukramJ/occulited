import {expect, test} from '@playwright/test';

// openccu-lite task 262, occulited task 14 against the stub: the pages' shape - the Account page's
// card with the stub's two keys (a second-factor key from before task 14, marked as one that
// cannot sign in with the offer to replace it, and a passkey), the console's way back in, and, on
// 127.0.0.1, the notice that a key is
// bound to the system's name; the Users page's key count with an administrator's Remove keys; the
// session lengths in the login settings, sent with the save; the passkey button on the login page.
// The ceremonies themselves run against the real daemon in webauthn.spec.ts.

test('the account page lists the keys and says where one can be added', async ({page}) => {
    await page.goto('/account');
    const card = page.locator('[data-section="security-keys"]');
    await expect(card).toBeVisible();
    await expect(card.locator('tbody tr')).toHaveCount(2);
    await expect(card.locator('tr[data-key="a1b2c3d4e5f6g7h8"] [data-key-kind]')).toHaveText('cannot be used to sign in');
    await expect(card.locator('tr[data-key="h8g7f6e5d4c3b2a1"] [data-key-kind]')).toHaveText('passkey');
    // the old second-factor key: the offer to remove it and add a passkey instead
    await expect(card.locator('[data-keys-unusable]')).toContainText('Remove it and add a passkey instead');
    // the way back in: the console command
    await expect(card.locator('[data-keys-recovery]')).toContainText('occulited admin reset-auth <user>');
    // on an address (the stub's 127.0.0.1): the notice names the system's full name
    await expect(card.locator('[data-keys-wrong-place]')).toContainText('https://ccu.example.home/');
    await expect(card.locator('[data-add-key]')).toHaveCount(0);
});

test('the users page shows the key count and lets an administrator remove the keys', async ({page}) => {
    await page.goto('/system/users');
    const admin = page.locator('section[data-section="accounts"] tbody tr', {hasText: 'admin'}).first();
    await expect(admin.locator('[data-keys]')).toHaveText(/2 passkey\(s\) · 1 cannot sign in/);
    await expect(admin.locator('[data-remove-keys]')).toBeVisible();
    const sebastian = page.locator('section[data-section="accounts"] tbody tr', {hasText: 'sebastian'}).first();
    await expect(sebastian.locator('[data-remove-keys]')).toHaveCount(0);
    await admin.locator('[data-remove-keys]').click();
    const dialog = page.locator('[role=dialog]');
    await expect(dialog).toContainText(/2/);
    await dialog.getByRole('button', {name: /Cancel|Abbrechen/}).click();
});

test('the login settings carry the session lengths and send them with the save', async ({page}) => {
    await page.goto('/system/users#authentication');
    const idle = page.locator('[data-session-idle]');
    await expect(idle).toHaveValue('30');
    await expect(page.locator('[data-session-max]')).toHaveValue('12');
    await idle.fill('45');
    await page.locator('[data-session-max]').fill('24');
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.url().endsWith('/api/auth/v1/config') && r.method() === 'PUT'),
        page.locator('section[data-section="authentication"]').getByRole('button', {name: /^(Save|Speichern)$/}).click(),
    ]);
    const body = req.postDataJSON() as Record<string, unknown>;
    expect(body.session_idle).toBe('45m');
    expect(body.session_max).toBe('24h');
    expect(body).not.toHaveProperty('modes');
});

test('the network page warns before a rename while an account has a key', async ({page}) => {
    await page.context().addCookies([{name: 'stub-webauthn-registered', value: '1', url: 'http://127.0.0.1/'}]);
    await page.goto('/system/network');
    const field = page.locator('#net-hostname');
    await expect(field).toBeVisible();
    await field.fill('renamed-ccu');
    await page.locator('#net-hostname ~ button, button', {hasText: /^(Save|Speichern)$/}).first().click();
    const dialog = page.locator('[role=dialog]');
    await expect(dialog).toContainText(/Rename the system|System umbenennen/);
    await expect(dialog).toContainText(/bound to the system's name|an den Namen des Systems gebunden/);
    await dialog.getByRole('button', {name: /Cancel|Abbrechen/}).click();
    await expect(dialog).toHaveCount(0);
});

test('the login page offers the passkey where one exists', async ({page}) => {
    await page.context().addCookies([{name: 'stub-token', value: 'out', url: 'http://127.0.0.1/'}]).catch(() => undefined);
    await page.goto('/login');
    // 127.0.0.1 is a secure context with WebAuthn in Chromium, and the stub says a passkey exists
    await expect(page.locator('[data-passkey]')).toBeVisible();
    await expect(page.locator('[data-passkey]')).toHaveText('Sign in with a passkey');
});

test('in German the account page marks the old key and the login page says Mit Passkey anmelden', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/account');
    const card = page.locator('[data-section="security-keys"]');
    await expect(card.locator('tr[data-key="a1b2c3d4e5f6g7h8"] [data-key-kind]')).toHaveText('kann nicht zur Anmeldung genutzt werden');
    await expect(card.locator('[data-keys-unusable]')).toContainText('stattdessen einen Passkey hinzu');
    await expect(card.locator('[data-keys-recovery]')).toContainText('Passkey verloren?');
    await page.context().addCookies([{name: 'stub-token', value: 'out', url: 'http://127.0.0.1/'}]).catch(() => undefined);
    await page.goto('/login');
    await expect(page.locator('[data-passkey]')).toHaveText('Mit Passkey anmelden');
});
