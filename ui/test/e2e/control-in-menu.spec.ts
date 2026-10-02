import {expect, test, type Page} from '@playwright/test';

// openccu-lite task 306: an account may take Control's tab out of the top bar (Settings). A
// preference, not a permission: /app still opens, and a start page of Control falls back to Status
// while the tab is hidden. Kept with the account like the App's other choices. Both themes and
// the phone run it (the projects), English and German below.

async function prefsCookie(page: Page, baseURL: string | undefined): Promise<void> {
    await page.context().addCookies([{name: 'stub-prefs', value: `hide-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
}

function putPrefs(page: Page) {
    return page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/api/auth/v1/me/preferences'));
}

/** the shell has read the account's preferences (the start page acts on them) */
async function gotoWithPrefs(page: Page, path: string): Promise<void> {
    await Promise.all([page.waitForResponse((r) => r.request().method() === 'GET' && r.url().endsWith('/api/auth/v1/me/preferences')), page.goto(path)]);
}

test('Control off the menu: no tab, the page still at /app, the start page falls back to Status', async ({page, baseURL}) => {
    await prefsCookie(page, baseURL);
    await page.goto('/settings');
    const nav = page.locator('nav.ol-nav');
    const tab = nav.getByRole('link', {name: 'Control', exact: true});
    await expect(tab).toBeVisible();
    const inMenu = page.locator('[data-setting="app-in-menu"]').getByRole('checkbox', {name: 'Show "Control" in the menu'});
    await expect(inMenu).toBeChecked();
    // the start page Control first, then the tab off
    const [req] = await Promise.all([putPrefs(page), page.locator('[data-setting="start-page"]').getByRole('combobox').selectOption('app')]);
    expect(req.postDataJSON()).toMatchObject({start_page: 'app'});
    expect(req.postDataJSON().app_hidden).toBeUndefined();
    await expect(page.locator('[data-start-fallback]')).toHaveCount(0);
    const [req2] = await Promise.all([putPrefs(page), inMenu.uncheck()]);
    expect(req2.postDataJSON()).toMatchObject({start_page: 'app', app_hidden: true});
    await expect(tab).toHaveCount(0);
    await expect(nav.getByRole('link', {name: 'Status', exact: true})).toBeVisible();
    await expect(page.locator('[data-start-fallback]')).toHaveText('While Control is not in the menu, the UI opens on Status.');

    // kept with the account: a reload reads it back
    await gotoWithPrefs(page, '/settings');
    await expect(inMenu).not.toBeChecked();
    await expect(tab).toHaveCount(0);

    // the system's own address stays on Status
    await gotoWithPrefs(page, '/');
    await expect(nav.getByRole('link', {name: 'Status', exact: true})).toHaveClass(/active/);
    await expect(page).toHaveURL(/\/$/);
    await expect(tab).toHaveCount(0);

    // the page itself is still there at its address
    await page.goto('/app');
    await expect(page.locator('[data-app-title]')).toHaveText('Favorites');
    await expect(tab).toHaveCount(0);

    // back on: the tab returns, and the start page is Control again
    await page.goto('/settings');
    const [req3] = await Promise.all([putPrefs(page), inMenu.check()]);
    expect(req3.postDataJSON().app_hidden).toBeUndefined();
    await expect(tab).toBeVisible();
    await gotoWithPrefs(page, '/');
    await expect(page).toHaveURL(/\/app$/);
});

test('German: „Bedienung“ im Menü anzeigen', async ({page, baseURL}) => {
    await prefsCookie(page, baseURL);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/settings');
    const card = page.locator('[data-setting="app-in-menu"]');
    await expect(card.locator('.k')).toHaveText('Bedienung im Menü');
    await expect(card.locator('.ol-card-detail')).toHaveText('Aus: Der Reiter verschwindet aus der Kopfleiste. Die Bedienung selbst bleibt und öffnet sich unter ihrer Adresse, /app.');
    const tab = page.locator('nav.ol-nav').getByRole('link', {name: 'Bedienung', exact: true});
    await expect(tab).toBeVisible();
    await Promise.all([putPrefs(page), card.getByRole('checkbox', {name: '„Bedienung“ im Menü anzeigen'}).uncheck()]);
    await expect(tab).toHaveCount(0);
});

test('the public principal keeps its one Control tab, whatever a preference says', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, public: true, user: 'guest', role: 'user', level: 'operate', account_id: 'abcd1234', must_change_password: false, sid: 'P1'}}));
    await page.route('**/api/auth/v1/me/preferences', (r) => r.fulfill({json: {addons: [], app_hidden: true}}));
    await page.goto('/app');
    await expect(page.locator('nav.ol-nav').getByRole('link', {name: 'Control', exact: true})).toBeVisible();
});
