import {expect, test, type Page} from '@playwright/test';

// Task 88 (D-61): the addon URLs. An addon's embedded view is /nav/<the path segment its drop-in
// proxies> - /nav/red for RedMatic's Node-RED at /addons/red/ - and the addon id stays in the nav
// entry for the dropdown row, the settings and the kept page. Standalone is the proxied path itself,
// the settings page /addon-settings/<addon id>, and /nav/<addon id> is replaced by the new route.
const menu = (page: Page) => page.locator('.ol-menu').first();
const row = (page: Page, name: string) => menu(page).getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const frame = (page: Page, prefix: string) => page.locator(`iframe.ol-kept-frame[src^="${prefix}"]`);

test('/nav/red shows Node-RED at /addons/red/ in the shell, and a reload stays there', async ({page}) => {
    await page.goto('/nav/red');
    await expect(frame(page, '/addons/red/')).toBeVisible();
    await expect(menu(page).locator('.ol-menubtn')).toHaveClass(/active/); // task 59: RedMatic is not pinned, so the Addons tab is the active one
    await page.reload();
    await expect(page).toHaveURL(/\/nav\/red$/);
    await expect(frame(page, '/addons/red/')).toBeVisible();
});

test('/nav/redmatic, the route before, is replaced by /nav/red with its query string', async ({page}) => {
    await page.goto('/');
    await page.goto('/nav/redmatic?x=1');
    await expect(page).toHaveURL(/\/nav\/red\?x=1$/);
    await expect(frame(page, '/addons/red/')).toBeVisible();
    // replaced, not pushed: Back leaves the addon instead of landing on the old route again
    await page.goBack();
    await expect(page).toHaveURL(/\/$/);
});

test('the dropdown row: the name opens /nav/red, ↗ the proxied path, ⚙ /addon-settings/redmatic', async ({page}) => {
    await page.goto('/');
    await menu(page).locator('.ol-menubtn').click();
    const red = row(page, 'RedMatic');
    await expect(red.locator('a.ol-newtab[target="_blank"]')).toHaveAttribute('href', /^\/addons\/red\//);
    await expect(red.getByRole('link', {name: 'Settings for RedMatic'})).toHaveAttribute('href', '/addon-settings/redmatic');
    await red.getByRole('menuitem').click();
    await expect(page).toHaveURL(/\/nav\/red$/);
    await expect(frame(page, '/addons/red/')).toBeVisible();
    await menu(page).locator('.ol-menubtn').click();
    await expect(row(page, 'RedMatic')).toHaveClass(/active/);
    // the kept page belongs to the addon: ✕ closes it and leaves for Status
    await row(page, 'RedMatic').getByRole('button', {name: 'Close RedMatic'}).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(frame(page, '/addons/red/')).toHaveCount(0);
});

test('a frontend whose path and addon id agree keeps its route', async ({page}) => {
    await page.goto('/');
    await menu(page).locator('.ol-menubtn').click();
    await row(page, 'Homematic-Manager').getByRole('menuitem').click();
    await expect(page).toHaveURL(/\/nav\/mh$/);
    await expect(frame(page, '/addons/mh/')).toBeVisible();
});

test('an unknown /nav/ route is not redirected anywhere', async ({page}) => {
    await page.goto('/nav/nothing-here');
    await expect(page).toHaveURL(/\/nav\/nothing-here$/);
    await expect(page.locator('iframe.ol-kept-frame')).toHaveCount(0);
});
