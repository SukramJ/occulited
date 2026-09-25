import {expect, test} from '@playwright/test';

// B-174: a browser session opened with an API token has no role; tokens do not administer in the
// browser (maintainer, 2026-09-23). The admin pages stay read-only and say why, with a way to sign
// in with a password; a password session sees no such notice.
const NOTICE = "This session was opened with an API token: it shows the system's settings but cannot change them. Sign in with a password to change them.";

test.describe('a token session', () => {
    test.beforeEach(async ({page, baseURL}) => {
        await page.context().addCookies([{name: 'stub-token', value: '1', url: baseURL!}]);
    });

    test('the system pages and the Addons page say it is read-only', async ({page}) => {
        for (const path of ['/system/firewall', '/system/network', '/system/users', '/addons']) {
            await page.goto(path);
            const notice = page.getByTestId('token-readonly');
            await expect(notice, path).toHaveText(new RegExp(NOTICE.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
            await expect(notice.getByRole('button', {name: 'Sign in with a password'})).toBeVisible();
        }
        // read-only as before: the firewall's editing is not offered
        await page.goto('/system/firewall');
        await expect(page.getByRole('heading', {name: /Firewall/})).toBeVisible();
        await expect(page.getByRole('button', {name: 'Reset counters'})).toHaveCount(0);
    });

    test('in German', async ({page}) => {
        await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
        await page.goto('/system/firewall');
        const notice = page.getByTestId('token-readonly');
        await expect(notice).toContainText('Diese Sitzung wurde mit einem API-Token geöffnet');
        await expect(notice).toContainText('Einstellungen des Systems');
        await expect(notice.getByRole('button', {name: 'Mit Passwort anmelden'})).toBeVisible();
    });

    test('signing in with a password ends the token session and shows the login', async ({page}) => {
        await page.goto('/system/firewall');
        await page.getByTestId('token-readonly').getByRole('button', {name: 'Sign in with a password'}).click();
        await expect(page.getByRole('heading', {name: 'Login'})).toBeVisible();
    });
});

test('a password session sees no token notice', async ({page}) => {
    await page.goto('/system/firewall');
    await expect(page.getByRole('heading', {name: /Firewall/})).toBeVisible();
    await expect(page.getByTestId('token-readonly')).toHaveCount(0);
    await expect(page.getByRole('button', {name: 'Reset counters'})).toBeVisible();
});
