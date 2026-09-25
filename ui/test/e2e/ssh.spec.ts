import {expect, test, type Page} from '@playwright/test';

// task 185: SSH on the Remote access page - the switch, the sessions open now (one from this
// browser's address), root's keys (the page's own and one added elsewhere, which stays read-only),
// a pasted key and root's password behind the user's password every time, and the old
// /system/network#ssh leading here.

async function own(page: Page, baseURL: string | undefined, extra: Record<string, string> = {}) {
    const id = `ssh-${Math.random().toString(36).slice(2)}`;
    await page.context().addCookies([{name: 'stub-ssh', value: id, url: baseURL!}, ...Object.entries(extra).map(([name, value]) => ({name, value, url: baseURL!}))]);
}

const KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEFSx3g/oLwaC84xZMOZIv9tk8m/eImdz0UxBwEjrnMj laptop ed';

async function confirmWithPassword(page: Page, first = 'labpass1') {
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Enter your password to confirm; it is asked every time.');
    await dialog.getByLabel('Password').fill(first);
    await dialog.getByRole('button', {name: 'Confirm'}).click();
    if (first !== 'labpass1') {
        await expect(dialog).toContainText('The password was wrong.');
        await dialog.getByLabel('Password').fill('labpass1');
        await dialog.getByRole('button', {name: 'Confirm'}).click();
    }
}

test('the sessions, one of them from this browser, and ending one', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/remote-access');
    await expect(page.locator('h2#ssh')).toContainText('SSH');
    const rows = page.locator('[data-ssh="sessions"] tbody tr');
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText('127.0.0.1');
    await expect(rows.nth(0)).toContainText("this browser's address");
    await expect(rows.nth(0)).toContainText('terminal (pts/0)');
    await expect(rows.nth(1)).toContainText('192.0.2.114');
    await expect(rows.nth(1)).toContainText('command or file copy');
    await expect(rows.nth(1)).toContainText('ED25519 SHA256:MBm1');
    await rows.nth(1).getByRole('button', {name: 'End'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('End the session of root from 192.0.2.114? Whatever runs in it stops.');
    await dialog.getByRole('button', {name: 'End'}).click();
    await expect(rows).toHaveCount(1);
    await expect(page.locator('[data-notice="ssh-notice"]')).toHaveText('Session ended.');
});

test('a pasted key asks for the password, lands in the list and is removed again; a key added elsewhere stays', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/remote-access');
    const keys = page.locator('[data-ssh="keys"] tbody tr');
    // the stub's file holds one key on two lines: both are listed
    await expect(keys).toHaveCount(2);
    await expect(keys.first()).toContainText('added outside this page');
    await expect(keys.first()).toContainText('command="uptime"');
    await expect(keys.first().getByRole('button', {name: 'Remove'})).toHaveCount(0);

    // a broken key is refused before the password is asked
    await page.locator('#ssh-new-key').fill('ssh-rsa AAAA short');
    await page.locator('[data-ssh="add"]').click();
    await expect(page.locator('[data-notice="ssh-error"]')).toContainText('the key is not valid base64');
    await expect(page.getByRole('dialog')).toHaveCount(0);

    await page.locator('#ssh-new-key').fill(KEY);
    await page.locator('[data-ssh="add"]').click();
    await confirmWithPassword(page, 'wrong');
    await expect(page.locator('[data-notice="ssh-notice"]')).toHaveText('Key added: laptop ed');
    await expect(page.locator('#ssh-new-key')).toHaveValue('');
    // task 223: said in the panel it was done in
    await expect(page.locator('[data-panel="ssh-keys"] [data-notice="ssh-notice"]')).toBeVisible();
    const mine = page.locator('[data-ssh="keys"] tr[data-key="managed"]');
    await expect(mine).toHaveCount(1);
    await expect(mine).toContainText('laptop ed');
    await expect(mine).toContainText('ssh-ed25519 · 256');

    // the same key again: already there, said before the password is asked
    await page.locator('#ssh-new-key').fill(KEY);
    await page.locator('[data-ssh="add"]').click();
    await expect(page.locator('[data-notice="ssh-error"]')).toContainText('this key is already there');
    await expect(page.getByRole('dialog')).toHaveCount(0);

    await mine.getByRole('button', {name: 'Remove'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Remove the key laptop ed? It no longer logs in as root.');
    await dialog.getByRole('button', {name: 'Remove'}).click();
    await expect(mine).toHaveCount(0);
    await expect(page.locator('[data-notice="ssh-notice"]')).toHaveText('Key removed: laptop ed');
});

test("root's password asks for the user's password; cancelling sets nothing", async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/remote-access');
    await page.locator('[data-ssh="pw"]').fill('rootpass123');
    await page.locator('[data-ssh="pw2"]').fill('rootpass124');
    await page.locator('[data-ssh="set-pw"]').click();
    await expect(page.locator('[data-notice="ssh-error"]')).toHaveText('The two passwords differ.');
    await page.locator('[data-ssh="pw2"]').fill('rootpass123');
    await page.locator('[data-ssh="set-pw"]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Cancel'}).click();
    await expect(page.locator('[data-notice="ssh-notice"]')).toHaveCount(0);
    await page.locator('[data-ssh="set-pw"]').click();
    await confirmWithPassword(page);
    await expect(page.locator('[data-notice="ssh-notice"]')).toHaveText('Root password set.');
    await expect(page.locator('[data-panel="ssh-password"] [data-notice="ssh-notice"]')).toBeVisible();
    await expect(page.locator('[data-ssh="pw"]')).toHaveValue('');
});

test('an account that signs in at the provider: the pasted key goes in after the round trip', async ({page, baseURL}) => {
    await own(page, baseURL, {'stub-confirm': 'oidc'});
    await page.goto('/system/remote-access');
    await page.locator('#ssh-new-key').fill(KEY);
    await page.locator('[data-ssh="add"]').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('you sign in at the identity provider once more and come back here');
    await dialog.getByRole('button', {name: 'Continue'}).click();
    await expect(page.locator('[data-notice="ssh-notice"]')).toHaveText('Key added: laptop ed');
    await expect(page).toHaveURL(/\/system\/remote-access$/);
});

test('SSH is not on the Network page any more; its old anchor leads here', async ({page, baseURL}) => {
    await own(page, baseURL);
    await page.goto('/system/network');
    await expect(page.locator('h1')).toContainText('Network');
    await expect(page.getByRole('heading', {name: 'SSH'})).toHaveCount(0);
    await page.goto('/system/network#ssh');
    await expect(page).toHaveURL(/\/system\/remote-access#ssh$/);
    await expect(page.locator('h2#ssh')).toBeVisible();
});

// task 245 (the maintainer: "an option (in the password panel) to disable root login via password
// and only allow key login"): the switch in the Root password panel - not to be switched on
// without a key, saved with PUT /ssh/key-only, a refusal shown; German.
async function keyOnly(page: Page, on: boolean, keys: {managed: unknown[]; other: unknown[]}) {
    const puts: unknown[] = [];
    let state = on;
    await page.route('**/api/system/v1/ssh', (route) => (route.request().method() === 'GET' ? route.fulfill({json: {enabled: true, running: true, key_only: state}}) : route.fallback()));
    await page.route('**/api/system/v1/ssh/keys', (route) => (route.request().method() === 'GET' ? route.fulfill({json: keys}) : route.fallback()));
    await page.route('**/api/system/v1/ssh/key-only', (route) => {
        const b = route.request().postDataJSON() as {on: boolean};
        puts.push(b);
        state = b.on;
        return route.fulfill({json: {enabled: true, running: true, key_only: b.on}});
    });
    return puts;
}
const ED = {type: 'ED25519', bits: 256, comment: 'laptop ed', fingerprint: 'SHA256:mZ+3k7WSTBEIONtZ4sWyKZdRbsGKX4yQvqbX/7pwwqs', line: KEY};

test('only key login: switched on with a key, off again', async ({page, baseURL}) => {
    await own(page, baseURL);
    const puts = await keyOnly(page, false, {managed: [ED], other: []});
    await page.goto('/system/remote-access');
    const panel = page.locator('[data-panel="ssh-password"]');
    const box = panel.locator('[data-ssh="key-only"] input');
    await expect(panel.locator('[data-ssh="key-only"]')).toContainText('Only key login (no password)');
    await expect(box).not.toBeChecked();
    await expect(box).toBeEnabled();
    await box.check();
    await expect.poll(() => puts).toEqual([{on: true}]);
    await expect(panel).toContainText('Root logs in over SSH with a key only now; the password is refused.');
    await expect(box).toBeChecked();
    await box.uncheck();
    await expect.poll(() => puts).toEqual([{on: true}, {on: false}]);
    await expect(panel).toContainText('Root may log in over SSH with the password again.');
});

test('only key login: no key, no switch-on; a refusal is shown', async ({page, baseURL}) => {
    await own(page, baseURL);
    await keyOnly(page, false, {managed: [], other: []});
    await page.goto('/system/remote-access');
    const panel = page.locator('[data-panel="ssh-password"]');
    await expect(panel.locator('[data-ssh="key-only"] input')).toBeDisabled();
    await expect(panel.locator('[data-ssh="key-only-needs-key"]')).toHaveText('Add a key first: only key login needs one.');
    // the system refuses as well (a key removed elsewhere meanwhile)
    await page.unroute('**/api/system/v1/ssh/keys');
    await page.route('**/api/system/v1/ssh/keys', (route) => (route.request().method() === 'GET' ? route.fulfill({json: {managed: [ED], other: []}}) : route.fallback()));
    await page.unroute('**/api/system/v1/ssh/key-only');
    await page.route('**/api/system/v1/ssh/key-only', (route) => route.fulfill({status: 409, json: {error: 'no-key', message: 'add a key first: with only key login and no key, nobody can log in over SSH'}}));
    await page.reload();
    await panel.locator('[data-ssh="key-only"] input').click();
    await expect(panel).toContainText('add a key first: with only key login and no key, nobody can log in over SSH');
    await expect(panel.locator('[data-ssh="key-only"] input')).not.toBeChecked();
});

test('only key login in German', async ({page, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await own(page, baseURL);
    await keyOnly(page, true, {managed: [ED], other: []});
    await page.goto('/system/remote-access');
    const sw = page.locator('[data-ssh="key-only"]');
    await expect(sw).toContainText('Nur Anmeldung mit Schlüssel (kein Passwort)');
    await expect(sw.locator('input')).toBeChecked();
});
