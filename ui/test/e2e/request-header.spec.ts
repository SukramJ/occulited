import {expect, test} from '@playwright/test';

// openccu-lite task 259 (D-78): a state-changing API call that rides on the session cookie alone
// carries X-Occulite-Request; without it the daemon answers 403 request-header. The stub applies
// the rule to every browser request with a non-safe method (test/stub/server.mjs), which pins the
// shell's api() helper and every raw fetch() of the UI to the header across the whole suite. This
// spec checks the rule itself from inside a page, and one raw fetch() site end to end.
test('a browser write without the header is refused, with it it passes', async ({page}) => {
    await page.goto('/account');
    await page.locator('#app > *').first().waitFor();
    const without = await page.evaluate(() => fetch('/api/auth/v1/me/preferences', {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: '{}'}).then((r) => r.status));
    expect(without).toBe(403);
    const withHeader = await page.evaluate(() => fetch('/api/auth/v1/me/preferences', {method: 'PUT', headers: {'Content-Type': 'application/json', 'X-Occulite-Request': '1'}, body: '{}'}).then((r) => r.status));
    expect(withHeader).not.toBe(403);
    // a read needs nothing
    const read = await page.evaluate(() => fetch('/api/auth/v1/state').then((r) => r.status));
    expect(read).toBe(200);
});

test('the account page ends the other sessions through a raw fetch that carries the header', async ({page}) => {
    await page.goto('/account');
    await page.getByRole('button', {name: /Sign out everywhere else|Überall sonst abmelden/}).click();
    await expect(page.locator('.ol-notice').filter({hasText: /Other sessions ended|Andere Sitzungen beendet/})).toBeVisible();
});
