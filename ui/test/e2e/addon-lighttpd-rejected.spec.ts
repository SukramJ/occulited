import {expect, test} from '@playwright/test';

// occulited task 23: an addon whose lighttpd fragment the system refused is marked on its card -
// a red badge with the verdict and the line - until an install brings a fragment that passes. The
// stub lists such an addon with the cookie stub-addon-rejected=1.
const card = (page: import('@playwright/test').Page) => page.locator('.ad-card').filter({has: page.locator('.ad-name', {hasText: 'Fragment Test'})}).first();

test('a refused lighttpd fragment is a red badge on the card, with the reason and the line', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-addon-rejected', value: '1', url: baseURL!}]);
    await page.goto('/addons');
    const badge = card(page).locator('.ad-lighttpd-rejected');
    await expect(badge).toHaveText('web configuration refused');
    await expect(badge).toHaveClass(/\bbad\b/);
    await badge.click();
    const tip = page.getByRole('tooltip');
    await expect(tip).toContainText('lighttpd serves nothing through it: proxy.server may point at this system only, not at "10.0.0.1" — line 3: proxy.server = ( "" => ( ( "host" => "10.0.0.1", "port" => 8088 ) )).');
    // no other card carries the mark
    await expect(page.locator('.ad-lighttpd-rejected')).toHaveCount(1);
});

test('the same in German', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-addon-rejected', value: '1', url: baseURL!}]);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/addons');
    const badge = card(page).locator('.ad-lighttpd-rejected');
    await expect(badge).toHaveText('Web-Konfiguration abgelehnt');
    await badge.click();
    await expect(page.getByRole('tooltip')).toContainText('lighttpd liefert also nichts darüber aus: proxy.server may point at this system only, not at "10.0.0.1" — Zeile 3:');
});

test('without such an addon no card carries the mark', async ({page}) => {
    await page.goto('/addons');
    await expect(page.locator('.ad-card').first()).toBeVisible();
    await expect(page.locator('.ad-lighttpd-rejected')).toHaveCount(0);
});
