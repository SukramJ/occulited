import {test, expect, type Page} from '@playwright/test';
import {pageWidth, scrollTo} from './scroll';

// task 55: the addon dropdown lists every installed addon. The stub is shaped like the lab Pi:
// RedMatic has a frontend (Node-RED at /addons/red/, which declares its favicon), Homematic-Manager
// has one that declares none, Mosquitto, hm2mqtt and JP HB Devices have a settings page only; the
// cookie `stub-addon-off=1` adds the NEO Server, switched off, without a settings page; its name is the API's built-in one.
// Task 59: the rows stand in two groups - the frontends first, in the user's order (by name until
// the user moves one), then the rest by name (addon-pins.spec.ts tests the pins and the order).
async function withOffAddon(page: Page, baseURL: string) {
    await page.context().addCookies([{name: 'stub-addon-off', value: '1', url: baseURL}]);
}
const menu = (page: Page) => page.locator('.ol-menu').first();
const menuButton = (page: Page) => menu(page).locator('.ol-menubtn');
const popup = (page: Page) => menu(page).getByRole('menu');
const row = (page: Page, name: string) => popup(page).locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const NAMES = ['Homematic-Manager', 'RedMatic', 'hm2mqtt', 'JP HB Devices', 'Mosquitto', 'NEO Server'];
// every row: the addons, then the stub's nav.d link that is no addon (the follow-up to task 131)
const ROWS = [...NAMES, 'CCU WebUI'];

test('every installed addon is a row, the frontends first and the rest by name; without a frontend the name and ↗ are inert', async ({page, baseURL}) => {
    await withOffAddon(page, baseURL!);
    await page.goto('/');
    await menuButton(page).click();
    await expect(popup(page).locator('.ol-addonname')).toHaveText(ROWS);

    // a frontend: the name is a live item, ↗ opens it in a new tab, ⚙ its settings page
    const red = row(page, 'RedMatic');
    await expect(red.getByRole('menuitem')).not.toHaveAttribute('aria-disabled', 'true');
    await expect(red.getByRole('link', {name: 'Open in new tab'})).toHaveAttribute('href', /^\/addons\/red\/(\?sid=@\w+@)?$/);
    await expect(red.getByRole('link', {name: 'Open in new tab'})).toHaveAttribute('target', '_blank');
    await expect(red.getByRole('link', {name: 'Settings for RedMatic'})).toHaveAttribute('href', '/addon-settings/redmatic');

    // a settings page only (Mosquitto): listed, name and ↗ disabled and muted, ⚙ live
    const mosq = row(page, 'Mosquitto');
    const name = mosq.locator('.ol-menuitem');
    await expect(name).toHaveAttribute('aria-disabled', 'true');
    // task 51: why it is inert is behind the row's ?, not a title; Escape closes it and not the menu
    await mosq.getByRole('button', {name: 'Help'}).click();
    await expect(page.getByRole('tooltip')).toHaveText('No web interface of its own; its settings are behind ⚙.');
    await page.keyboard.press('Escape');
    await expect(page.getByRole('tooltip')).toHaveCount(0);
    await expect(popup(page)).toBeVisible();
    await expect(name).toHaveCSS('cursor', 'default');
    await expect(mosq.locator('.ol-newtab').first()).toHaveAttribute('aria-disabled', 'true');
    await expect(mosq.locator('a')).toHaveCount(1);
    await expect(mosq.getByRole('link', {name: 'Settings for Mosquitto'})).toHaveAttribute('href', '/addon-settings/mosquitto');
    // a click on the inert name goes nowhere (forced: Playwright itself will not click what is
    // aria-disabled, which is the point - a user's click still lands on it)
    await name.click({force: true});
    await expect(page).toHaveURL(/\/$/);

    // switched off: it says so, the reason is in the popup, ⚙ leads to its row on Installed addons
    const neo = row(page, 'NEO Server');
    await expect(neo).toContainText('switched off');
    await expect(neo.locator('.ol-menuitem')).toHaveAttribute('aria-disabled', 'true');
    await neo.getByRole('button', {name: 'Help'}).click();
    await expect(page.getByRole('tooltip')).toContainText(/switched off: needs ReGaHSS/);
    await expect(neo.locator('a')).toHaveCount(1);
    await expect(neo.getByRole('link', {name: 'Settings for NEO Server'})).toHaveAttribute('href', '/addons?addon=97NeoServer');
});

test('the icon is the frontend\'s favicon, fetched when the dropdown first opens and cached per version', async ({page}) => {
    const frontends: string[] = [];
    page.on('request', (r) => {
        const p = new URL(r.url()).pathname;
        if (/^\/addons\/(red|mh)\//.test(p)) frontends.push(p);
    });
    await page.goto('/');
    await expect(menuButton(page)).toBeVisible();
    await page.waitForTimeout(500);
    expect(frontends, 'nothing is fetched before the dropdown opens').toEqual([]);

    await menuButton(page).click();
    // RedMatic: Node-RED's declared icon
    await expect(row(page, 'RedMatic').locator('.ol-addonicon img')).toHaveAttribute('src', '/addons/red/favicon.ico');
    // Homematic-Manager declares none and has no favicon.ico either: the letter, remembered as such
    await expect.poll(() => page.evaluate(() => localStorage.getItem('ol.favicon.mh@3.0.0'))).toBe('');
    await expect(row(page, 'Homematic-Manager').locator('.ol-addonmono')).toHaveText('H');
    await expect(row(page, 'Homematic-Manager').locator('img')).toHaveCount(0);
    // no frontend, no fetch: the logo from the Info: line is not used any more - the icon is the
    // manifest's (task 100: Mosquitto declares a logo, which stands in for the icon), never a fetch
    // of an addon page; hm2mqtt declares none and has no frontend: the letter
    await expect(row(page, 'Mosquitto').locator('.ol-addonicon img')).toHaveAttribute('src', /^\/api\/system\/v1\/addons\/mosquitto\/images\/logo\?v=/);
    await expect(row(page, 'hm2mqtt').locator('.ol-addonmono')).toHaveText('H');
    await expect(row(page, 'hm2mqtt').locator('img')).toHaveCount(0);
    expect(await page.evaluate(() => localStorage.getItem('ol.favicon.redmatic@9.4.0'))).toBe('/addons/red/favicon.ico');
    expect(frontends.filter((p) => p === '/addons/red/')).toHaveLength(1);

    // a reload opens the dropdown from the cache: neither frontend page is fetched again
    frontends.length = 0;
    await page.reload();
    await menuButton(page).click();
    await expect(row(page, 'RedMatic').locator('.ol-addonicon img')).toBeVisible();
    await expect(row(page, 'Homematic-Manager').locator('.ol-addonmono')).toHaveText('H');
    expect(frontends.filter((p) => p === '/addons/red/' || p === '/addons/mh/')).toEqual([]);
});

// task 59: the button is the word *Addons* - no breadcrumb, no caret - marked active on the pages
// behind it, and it keeps its width whatever page is open
test('⚙ opens the settings in the shell\'s frame, and the Addons tab is marked without changing width', async ({page, baseURL}) => {
    await withOffAddon(page, baseURL!);
    await page.goto('/');
    await expect(menuButton(page).locator('.ol-menubtn-text')).toHaveText('Addons');
    await expect(menuButton(page)).not.toHaveClass(/active/);
    const width = (await menuButton(page).boundingBox())!.width;
    const sameWidth = async () => expect(Math.abs((await menuButton(page).boundingBox())!.width - width)).toBeLessThan(0.5);

    // Mosquitto: its settings.cgi in the frame, the tab active, ⚙ active in the dropdown
    await menuButton(page).click();
    await row(page, 'Mosquitto').getByRole('link', {name: 'Settings for Mosquitto'}).click();
    await expect(page).toHaveURL(/\/addon-settings\/mosquitto$/);
    await expect(page.locator('iframe.ol-frame:visible')).toHaveAttribute('src', /^\/addons\/mosquitto\/settings\.cgi\?/);
    await expect(menuButton(page).locator('.ol-menubtn-text')).toHaveText('Addons');
    await expect(menuButton(page)).toHaveClass(/active/);
    await sameWidth();
    await menuButton(page).click();
    await expect(row(page, 'Mosquitto').getByRole('link', {name: 'Settings for Mosquitto'})).toHaveClass(/active/);

    // RedMatic's name: Node-RED in the frame, at the route named after its proxied path (task 88)
    await row(page, 'RedMatic').getByRole('menuitem').click();
    await expect(page).toHaveURL(/\/nav\/red$/);
    await expect(page.locator('iframe.ol-frame:visible')).toHaveAttribute('src', /^\/addons\/red\//);
    await expect(menuButton(page).locator('.ol-menubtn-text')).toHaveText('Addons');
    await expect(menuButton(page)).toHaveClass(/active/);
    await sameWidth();

    // the NEO Server has no settings page: Installed addons, the footer entry active
    await menuButton(page).click();
    await row(page, 'NEO Server').getByRole('link', {name: 'Settings for NEO Server'}).click();
    await expect(page).toHaveURL(/\/addons\?addon=97NeoServer$/);
    await expect(page.locator('#addon-row-97NeoServer')).toBeVisible();
    await expect(menuButton(page)).toHaveClass(/active/);
    await sameWidth();
    await menuButton(page).click();
    await expect(popup(page).getByRole('menuitem', {name: 'Manage addons'})).toHaveClass(/active/);
});

// A window of a fixed desktop size in every project: the phone project's emulation zooms the wide
// Addons table out until nothing is below the fold, and "scrolled into view" would prove nothing.
test('⚙ of an addon without a settings page scrolls its row on Installed addons into view', async ({browser, baseURL}) => {
    const ctx = await browser.newContext({baseURL, viewport: {width: 1000, height: 300}});
    const page = await ctx.newPage();
    await withOffAddon(page, baseURL!);
    await page.goto('/addons');
    await expect(page.locator('#addon-row-97NeoServer')).toBeAttached();
    await expect(page.locator('#addon-row-97NeoServer')).not.toBeInViewport();
    await page.goto('/');
    await menuButton(page).click();
    await row(page, 'NEO Server').getByRole('link', {name: 'Settings for NEO Server'}).click();
    await expect(page).toHaveURL(/\/addons\?addon=97NeoServer$/);
    await expect(page.locator('#addon-row-97NeoServer')).toBeInViewport();
    // a second click on the same ⚙, scrolled away in between: the route stays, the row comes back
    await scrollTo(page, 0);
    await expect(page.locator('#addon-row-97NeoServer')).not.toBeInViewport();
    await menuButton(page).click();
    await row(page, 'NEO Server').getByRole('link', {name: 'Settings for NEO Server'}).click();
    await expect(page.locator('#addon-row-97NeoServer')).toBeInViewport();
    await ctx.close();
});

// task 51: an inert row's name and ↗ are still skipped, but the ? that says why it is inert is a
// tab stop - the keyboard reaches every ?; task 59: a frontend's row also has its handle and its pin
test('Tab reaches handle, name, ↗, pin and ⚙ of a row with a frontend, only ? and ⚙ of an inert one', async ({page, baseURL}) => {
    await withOffAddon(page, baseURL!);
    await page.goto('/');
    await menuButton(page).focus();
    await page.keyboard.press('Enter');
    await expect(popup(page).locator('.ol-addonname')).toHaveText(ROWS);
    const focused = () =>
        page.evaluate(() => {
            const el = document.activeElement as HTMLElement;
            return el.getAttribute('aria-label') ?? el.querySelector('.ol-addonname')?.textContent ?? el.textContent?.trim() ?? '';
        });
    const seen: string[] = [];
    for (let i = 0; i < 20; i++) {
        await page.keyboard.press('Tab');
        seen.push(await focused());
    }
    expect(seen).toEqual([
        'Move Homematic-Manager',
        'Homematic-Manager',
        'Open in new tab',
        'Pin Homematic-Manager to the tab bar',
        'Settings for Homematic-Manager',
        'Move RedMatic',
        'RedMatic',
        'Open in new tab',
        'Pin RedMatic to the tab bar',
        'Settings for RedMatic',
        'Help',
        'Settings for hm2mqtt',
        'Help',
        'Settings for JP HB Devices',
        'Help',
        'Settings for Mosquitto',
        'Help',
        'Settings for NEO Server',
        'CCU WebUI',
        'Manage addons',
    ]);
    // back past the link to the NEO Server's ⚙, and Enter follows it
    await page.keyboard.press('Shift+Tab');
    await page.keyboard.press('Shift+Tab');
    expect(await focused()).toBe('Settings for NEO Server');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/addons\?addon=97NeoServer$/);
});

test('the dropdown fits a phone screen', async ({page, baseURL}) => {
    await withOffAddon(page, baseURL!);
    await page.setViewportSize({width: 360, height: 740});
    await page.goto('/');
    await menuButton(page).click();
    await expect(popup(page).locator('.ol-addonname')).toHaveText(ROWS);
    const pop = (await popup(page).boundingBox())!;
    expect(pop.x).toBeGreaterThanOrEqual(0);
    expect(pop.x + pop.width).toBeLessThanOrEqual(360);
    for (const n of NAMES) {
        const gear = (await row(page, n).getByRole('link', {name: `Settings for ${n}`}).boundingBox())!;
        expect(gear.x + gear.width, n).toBeLessThanOrEqual(pop.x + pop.width);
        expect(gear.width, n).toBeGreaterThanOrEqual(20);
    }
    expect(await pageWidth(page)).toBeLessThanOrEqual(360);
    await row(page, 'Mosquitto').getByRole('link', {name: 'Settings for Mosquitto'}).click();
    await expect(page).toHaveURL(/\/addon-settings\/mosquitto$/);
});
