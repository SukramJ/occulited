import {expect, test, type Page} from '@playwright/test';

// B-133 and B-134, found by the maintainer on the Charly (2026-09-15) with the real Homematic Manager,
// whose frontend the stub models here: /addons/mh/ is proxied to the addon's own server, which checks a
// ?sid= it is handed against the box's API before anything else - and the API refuses the legacy alias
// (D-77), so a frame opened with ?sid=@alias@ came back as the box's own page (B-132's notice). Its
// settings.cgi without cmd=config is the CCU's hand-over into the app (302 to the frontend), so the
// gear showed the app instead of the settings (B-134); the box's config_url now carries cmd=config.

const NOTICE = /This addon sent you back to openccu-lite/;
const menu = (page: Page) => page.locator('.ol-menu').first();
const row = (page: Page, name: string) => menu(page).getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const kept = (page: Page, prefix: string) => page.locator(`iframe.ol-kept-frame[src^="${prefix}"]`);

test.beforeEach(async ({page, baseURL}, info) => {
    // the legacy session on, as on a box by default
    await page.context().addCookies([{name: 'stub-legacy', value: `${info.project.name}-${info.title}`.replace(/[^\w-]/g, '_'), url: baseURL}]);
});

test('B-133: the proxied frontend opens without ?sid= and shows the addon, not the notice', async ({page}) => {
    await page.goto('/');
    await menu(page).locator('.ol-menubtn').click();
    await row(page, 'Homematic-Manager').getByRole('menuitem').click();
    await expect(page).toHaveURL(/\/nav\/mh$/);
    await expect(kept(page, '/addons/mh/')).toHaveAttribute('src', /^\/addons\/mh\/\?theme=\w+&lang=\w+$/);
    const frame = page.frameLocator('iframe.ol-kept-frame[src^="/addons/mh/"]');
    await expect(frame.getByText('addon page stub: /addons/mh/')).toBeVisible();
    await expect(frame.getByText(NOTICE)).toHaveCount(0);
    // the new-tab link of the row is the same URL
    await menu(page).locator('.ol-menubtn').click();
    await expect(row(page, 'Homematic-Manager').locator('a.ol-newtab[target="_blank"]')).toHaveAttribute('href', '/addons/mh/');
});

test('B-133: a frontend opened with the alias is what the notice was made for', async ({page}) => {
    // the stub's frontend refuses the alias the way the real one did; the shell never sends it
    const r = await page.request.get('/addons/mh/?sid=@LeGaCy0001@', {maxRedirects: 0});
    expect(r.status()).toBe(302);
    expect(r.headers()['location']).toBe('/');
});

test('B-134: the gear opens the settings page with cmd=config and the alias, not the app', async ({page}) => {
    await page.goto('/');
    await menu(page).locator('.ol-menubtn').click();
    await row(page, 'Homematic-Manager').locator('a[href^="/addon-settings/"]').click();
    await expect(page).toHaveURL(/\/addon-settings\/mh$/);
    await expect(kept(page, '/addons/mh/settings.cgi')).toHaveAttribute('src', /^\/addons\/mh\/settings\.cgi\?cmd=config&sid=@LeGaCy0001@&theme=\w+&lang=\w+$/);
    const frame = page.frameLocator('iframe.ol-kept-frame[src^="/addons/mh/settings.cgi"]');
    await expect(frame.getByRole('heading', {name: 'Homematic-Manager settings stub'})).toBeVisible();
    await expect(frame.getByText('addon page stub: /addons/mh/')).toHaveCount(0);
});

test('B-134: the plain settings.cgi is the hand-over into the app, which is why config_url must not be it', async ({page}) => {
    const r = await page.request.get('/addons/mh/settings.cgi?sid=@LeGaCy0001@', {maxRedirects: 0});
    expect(r.status()).toBe(302);
    expect(r.headers()['location']).toBe('/addons/mh/');
});
