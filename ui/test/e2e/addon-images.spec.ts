import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// openccu-lite task 100: an addon's icon and logo come from its manifest (ui.icon, ui.icon_dark,
// ui.logo, ui.logo_dark), served by the box on its own origin and shown through <img> alone - in
// the addon dropdown, the tab bar, the Services rows, the Addons page and the catalogue. The dark
// variant shows in dark mode and the light one stands in for a missing dark one; a logo stands in
// for a missing icon; an image that does not load gives way to the favicon or the letter. The stub:
// ioBroker (stub-addon-more=1) declares all four, Mosquitto a logo alone, RedMatic, Homematic-Manager
// and hm2mqtt none; the catalogue's TM Devices has an icon, XML-API a logo with a dark variant. A
// declared image that is not in the tree (the box answers 404) is this spec's own route.
const menu = (page: Page) => page.locator('.ol-menu').first();
const button = (page: Page) => menu(page).locator('.ol-menubtn');
const popup = (page: Page) => menu(page).getByRole('menu');
const row = (page: Page, name: string) => popup(page).locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const shownTabs = (page: Page) => page.locator('nav.ol-nav > a.ol-pintab:not(.ol-pintab-folded)');
const card = (page: Page, id: string) => page.locator(`#addon-row-${id}`);
const dark = () => test.info().project.use.colorScheme === 'dark';
const variant = (kind: 'icon' | 'logo') => (dark() ? `${kind}-dark` : kind);
const url = (where: 'addons' | 'catalog', id: string, kind: string) => new RegExp(`^/api/system/v1/${where}/${id}/images/${kind}\\?v=`);

let n = 0;
async function prepare(page: Page, baseURL: string) {
    await page.context().addCookies([
        {name: 'stub-prefs', value: `images-${process.pid}-${Date.now()}-${n++}`, url: baseURL},
        {name: 'stub-addon-more', value: '1', url: baseURL},
    ]);
}

test('the dropdown and the tab bar: the manifest icon in the theme\'s variant, a logo for a missing icon, the favicon and the letter behind them', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    const outside: string[] = [];
    page.on('request', (r) => {
        if (/^https?:/.test(r.url()) && !r.url().startsWith(baseURL!)) outside.push(r.url());
    });
    await page.goto('/');
    await button(page).click();
    // ioBroker declares all four: the icon of the theme; Mosquitto a logo alone: it stands in for the icon
    const iob = row(page, 'ioBroker').locator('.ol-addonicon img');
    await expect(iob).toHaveAttribute('src', url('addons', 'iobroker', variant('icon')));
    await expect(row(page, 'Mosquitto').locator('.ol-addonicon img')).toHaveAttribute('src', url('addons', 'mosquitto', 'logo'));
    // without a manifest image: RedMatic's frontend favicon (task 55), Homematic-Manager's letter
    await expect(row(page, 'RedMatic').locator('.ol-addonicon img')).toHaveAttribute('src', '/addons/red/favicon.ico');
    await expect(row(page, 'Homematic-Manager').locator('.ol-addonmono')).toHaveText('H');
    // the image loaded (a 404 would have left the letter): it has a size
    expect(await iob.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
    // pinned: the tab shows the same icon at text size
    await row(page, 'ioBroker').locator('.ol-pin').click();
    await expect(shownTabs(page).filter({hasText: 'ioBroker'}).locator('img')).toHaveAttribute('src', url('addons', 'iobroker', variant('icon')));
    expect(outside, 'no request leaves the origin for an image').toEqual([]);
});

test('a declared image the box does not have (404) gives way: the other variant, the logo, at last the letter - never a broken image', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    // ioBroker's icons are gone from its tree, its logos too: the letter
    await page.route(/\/api\/system\/v1\/addons\/iobroker\/images\//, (r) => r.fulfill({status: 404, contentType: 'application/json', body: '{"error":"not-found","message":"the addon declares no such image"}'}));
    // Mosquitto's logo is there in the light variant alone (the stub has no dark one): the same image either way
    await page.goto('/');
    await button(page).click();
    await expect(row(page, 'ioBroker').locator('.ol-addonmono')).toHaveText('I');
    await expect(row(page, 'ioBroker').locator('img')).toHaveCount(0);
    await expect(row(page, 'Mosquitto').locator('.ol-addonicon img')).toHaveAttribute('src', url('addons', 'mosquitto', 'logo'));
    await page.keyboard.press('Escape');
    // the icon of the theme gone, the other variant stands in
    await page.unrouteAll({behavior: 'wait'});
    await page.route(new RegExp(`/api/system/v1/addons/iobroker/images/${variant('icon')}\\?`), (r) => r.fulfill({status: 404, contentType: 'application/json', body: '{"error":"not-found"}'}));
    await page.goto('/addons');
    await expect(card(page, 'iobroker').locator('.ad-logo img')).toHaveAttribute('src', url('addons', 'iobroker', variant('logo')));
    await button(page).click();
    await expect(row(page, 'ioBroker').locator('.ol-addonicon img')).toHaveAttribute('src', url('addons', 'iobroker', dark() ? 'icon' : 'icon-dark'));
});

test('switching the theme switches the variant at once, in the dropdown and in the tab', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    await page.goto('/');
    await button(page).click();
    await row(page, 'ioBroker').locator('.ol-pin').click();
    await page.keyboard.press('Escape');
    const tab = shownTabs(page).filter({hasText: 'ioBroker'}).locator('img');
    await expect(tab).toHaveAttribute('src', url('addons', 'iobroker', variant('icon')));
    await page.goto('/settings');
    const theme = page.locator('select.hmm-select').filter({has: page.locator('option[value="dark"]')});
    await theme.selectOption('dark');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await expect(tab).toHaveAttribute('src', url('addons', 'iobroker', 'icon-dark'));
    await button(page).click();
    await expect(row(page, 'ioBroker').locator('.ol-addonicon img')).toHaveAttribute('src', url('addons', 'iobroker', 'icon-dark'));
    await page.keyboard.press('Escape');
    await theme.selectOption('light');
    await expect(tab).toHaveAttribute('src', url('addons', 'iobroker', 'icon'));
    // a missing dark variant uses the light one: Mosquitto's logo stands in both ways
    await theme.selectOption('dark');
    await button(page).click();
    await expect(row(page, 'Mosquitto').locator('.ol-addonicon img')).toHaveAttribute('src', url('addons', 'mosquitto', 'logo'));
});

test('the Addons page: the manifest logo on an installed card, the catalogue\'s copy on one that is not installed, the Info: logo and the letter behind them', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    await page.goto('/addons');
    await expect(card(page, 'iobroker').locator('.ad-logo img')).toHaveAttribute('src', url('addons', 'iobroker', variant('logo')));
    await expect(card(page, 'mosquitto').locator('.ad-logo img')).toHaveAttribute('src', url('addons', 'mosquitto', 'logo'));
    // not installed: the catalogue's copy - TM Devices' icon stands in for its logo, XML-API's logo in the theme's variant
    await expect(card(page, 'tm-devices').locator('.ad-logo img')).toHaveAttribute('src', url('catalog', 'tm-devices', 'icon'));
    await expect(card(page, 'xml-api').locator('.ad-logo img')).toHaveAttribute('src', url('catalog', 'xml-api', variant('logo')));
    // RedMatic declares none: the logo of its Info: line; hm2mqtt and Homematic-Manager neither: the letter
    await expect(card(page, 'redmatic').locator('.ad-logo img')).toHaveAttribute('src', '/addons/redmatic/redmatic5-wide.png');
    await expect(card(page, 'hm2mqtt').locator('.ad-letter')).toHaveText('H');
    await expect(card(page, 'hm2mqtt').locator('img')).toHaveCount(0);
    await expect(card(page, 'mh').locator('.ad-letter')).toHaveText('H');
});

test('the Services page: an addon unit\'s row carries the addon\'s icon', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    await page.goto('/system/services');
    await expect(page.locator('tr[data-service="addon-mosquitto"] .sv-id img.sv-icon')).toHaveAttribute('src', url('addons', 'mosquitto', 'logo'));
    await expect(page.locator('tr[data-service="addon-redmatic"] .sv-id img')).toHaveCount(0);
    await expect(page.locator('tr[data-service="rfd"] .sv-id img')).toHaveCount(0);
});

test('the image answers carry the type by content, nosniff and the policy that keeps an SVG a picture', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    await page.goto('/');
    const res = await page.request.get('/api/system/v1/addons/iobroker/images/icon?v=1.2.0');
    expect(res.status()).toBe(200);
    expect(res.headers()['content-type']).toBe('image/svg+xml');
    expect(res.headers()['x-content-type-options']).toBe('nosniff');
    expect(res.headers()['content-security-policy']).toBe("default-src 'none'; style-src 'unsafe-inline'");
    expect((await res.text()).startsWith('<svg')).toBe(true);
    expect((await page.request.get('/api/system/v1/addons/mosquitto/images/icon?v=2.1.2%2B3')).status()).toBe(404);
});
