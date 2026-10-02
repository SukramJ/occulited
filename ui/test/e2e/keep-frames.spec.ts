import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// Task 39: the shell keeps an addon page loaded while the user is elsewhere, so switching back to
// Node-RED does not load the editor from scratch. The stub's framed pages count their loads in the
// tab's sessionStorage (`stub.loads:<path>`, shared with the shell - same origin) and keep what the
// shell posts to them in `__looks`, so a kept page can be told from a reloaded one. Only frontends
// count towards the three kept pages; an addon's settings page is dropped when it is left.
const menu = (page: Page) => page.locator('.ol-menu').first();
const menuButton = (page: Page) => menu(page).locator('.ol-menubtn');
const row = (page: Page, name: string) => menu(page).getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const kept = (page: Page) => page.locator('iframe.ol-kept-frame');
const frameOf = (page: Page, prefix: string) => page.locator(`iframe.ol-kept-frame[src^="${prefix}"]`);
const loads = (page: Page, path: string) => page.evaluate((p) => Number(sessionStorage.getItem(`stub.loads:${p}`) ?? 0), path);
const statusTab = (page: Page) => page.locator('.ol-nav > a[href="/"]');

/**
 * Opens the Addons menu and waits until its panel has grown out of the tab (MenuPanel's data-phase
 * goes opening → open): a row measured or clicked while the box still morphs sits at a scaled
 * position - the two ⚙ differed by 0.5 px, once by 10.6 px (openccu-lite B-171).
 */
async function menuOpen(page: Page) {
    await menuButton(page).click();
    await expect(page.locator('nav.ol-addonpanel')).toHaveAttribute('data-phase', 'open');
}
async function openFrontend(page: Page, name: string) {
    await menuOpen(page);
    await row(page, name).getByRole('menuitem').click();
}
async function openSettings(page: Page, name: string) {
    await menuOpen(page);
    await row(page, name).getByRole('link', {name: `Settings for ${name}`}).click();
}

/** The stub has two frontends (RedMatic, Homematic-Manager); these addons get one as well. */
async function moreFrontends(page: Page, addons: {id: string; name: string; href: string}[]) {
    await page.route('**/api/system/v1/nav', async (r) => {
        const res = await r.fetch();
        const body = (await res.json()) as {entries: object[]};
        body.entries = [...body.entries, ...addons.map((a) => ({id: a.id, label: {de: a.name, en: a.name}, href: a.href, target: 'iframe', order: 500, source: 'addon'}))];
        return r.fulfill({response: res, json: body});
    });
}

test('a kept page survives a switch to a shell page and back: no reload, its input kept, Back and Forward show it', async ({page}) => {
    await page.goto('/');
    await openFrontend(page, 'RedMatic');
    const red = frameOf(page, '/addons/red/');
    await expect(red).toBeVisible();
    await expect.poll(() => loads(page, '/addons/red/')).toBe(1);
    await red.contentFrame().locator('#probe').fill('typed before the switch');

    await statusTab(page).click();
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    await expect(red).toBeHidden();
    await expect(red).toHaveCount(1);

    await openFrontend(page, 'RedMatic');
    await expect(red).toBeVisible();
    await expect(red.contentFrame().locator('#probe')).toHaveValue('typed before the switch');
    expect(await loads(page, '/addons/red/')).toBe(1);

    await page.goBack();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    await expect(red).toBeHidden();
    await page.goForward();
    await expect(page).toHaveURL(/\/nav\/red$/);
    await expect(red).toBeVisible();
    expect(await loads(page, '/addons/red/')).toBe(1);
    // the hidden page is out of the way: the shell page on top of it takes the clicks
    await expect(kept(page)).toHaveCount(1);
});

test('a hidden kept page hears a theme and a language change', async ({page}) => {
    await page.goto('/nav/red');
    const red = frameOf(page, '/addons/red/');
    await expect(red).toBeVisible();
    await page.locator('a.ol-iconlink[href="/settings"]').click();
    await expect(red).toBeHidden();
    await page.locator('select').nth(1).selectOption('dark');
    await page.locator('select').nth(0).selectOption('de');
    const looks = () => red.evaluate((f: HTMLIFrameElement) => (f.contentWindow as unknown as {__looks: {type: string; theme?: string; lang?: string; payload?: {theme: string}}[]}).__looks);
    await expect.poll(async () => (await looks()).some((m) => m.type === 'openccu-lite:theme' && m.theme === 'dark' && m.lang === 'de')).toBe(true);
    expect((await looks()).some((m) => m.type === 'set-theme' && m.payload?.theme === 'dark')).toBe(true);
    expect(await loads(page, '/addons/red/')).toBe(1);
});

test('at most three frontends are kept: opening a fourth drops the least recently shown, and nothing else reloads', async ({page}) => {
    await moreFrontends(page, [
        {id: 'hm2mqtt', name: 'hm2mqtt', href: '/addons/hm2mqtt/web/'},
        {id: 'mosquitto', name: 'Mosquitto', href: '/addons/mosquitto/web/'},
    ]);
    await page.goto('/');
    await openFrontend(page, 'RedMatic');
    await expect(frameOf(page, '/addons/red/')).toBeVisible();
    await openFrontend(page, 'Homematic-Manager');
    await expect(frameOf(page, '/addons/mh/')).toBeVisible();
    await openFrontend(page, 'hm2mqtt');
    await expect(frameOf(page, '/addons/hm2mqtt/web/')).toBeVisible();
    // RedMatic again: now Homematic-Manager is the one shown longest ago
    await openFrontend(page, 'RedMatic');
    await expect(frameOf(page, '/addons/red/')).toBeVisible();

    await openFrontend(page, 'Mosquitto');
    await expect(frameOf(page, '/addons/mosquitto/web/')).toBeVisible();
    await expect(kept(page)).toHaveCount(3);
    await expect(frameOf(page, '/addons/mh/')).toHaveCount(0);
    // the pages before and after the dropped one in the DOM did not load again
    expect(await loads(page, '/addons/red/')).toBe(1);
    expect(await loads(page, '/addons/hm2mqtt/web/')).toBe(1);

    // Homematic-Manager again is a fresh load, and hm2mqtt is the oldest now
    await openFrontend(page, 'Homematic-Manager');
    await expect(frameOf(page, '/addons/mh/')).toBeVisible();
    await expect.poll(() => loads(page, '/addons/mh/')).toBe(2);
    await expect(frameOf(page, '/addons/hm2mqtt/web/')).toHaveCount(0);
    await expect(kept(page)).toHaveCount(3);
});

test('a settings page never pushes a frontend out, and goes as soon as it is left', async ({page}) => {
    await moreFrontends(page, [{id: 'hm2mqtt', name: 'hm2mqtt', href: '/addons/hm2mqtt/web/'}]);
    await page.goto('/');
    await openFrontend(page, 'RedMatic');
    await expect(frameOf(page, '/addons/red/')).toBeVisible();
    await openFrontend(page, 'Homematic-Manager');
    await expect(frameOf(page, '/addons/mh/')).toBeVisible();
    await openFrontend(page, 'hm2mqtt');
    await expect(frameOf(page, '/addons/hm2mqtt/web/')).toBeVisible();
    await expect(kept(page)).toHaveCount(3);

    // three frontends kept, and a settings page on top of them: nobody goes
    await openSettings(page, 'Mosquitto');
    const mosquitto = frameOf(page, '/addons/mosquitto/settings.cgi');
    await expect(mosquitto).toBeVisible();
    await expect(kept(page)).toHaveCount(4);
    await expect(frameOf(page, '/addons/red/')).toHaveCount(1);

    // leaving it for a shell page drops it; the frontends stay loaded
    await statusTab(page).click();
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    await expect(mosquitto).toHaveCount(0);
    await expect(kept(page)).toHaveCount(3);

    // back to it is a fresh load; another settings page replaces it
    await openSettings(page, 'Mosquitto');
    await expect(mosquitto).toBeVisible();
    await expect.poll(() => loads(page, '/addons/mosquitto/settings.cgi')).toBe(2);
    await openSettings(page, 'RedMatic');
    await expect(frameOf(page, '/addons/redmatic/settings.cgi')).toBeVisible();
    await expect(mosquitto).toHaveCount(0);
    await expect(kept(page)).toHaveCount(4);

    // a frontend shown again drops the settings page and did not reload itself
    await openFrontend(page, 'RedMatic');
    await expect(frameOf(page, '/addons/red/')).toBeVisible();
    await expect(frameOf(page, '/addons/redmatic/settings.cgi')).toHaveCount(0);
    await expect(kept(page)).toHaveCount(3);
    expect(await loads(page, '/addons/red/')).toBe(1);
    expect(await loads(page, '/addons/mh/')).toBe(1);
    expect(await loads(page, '/addons/hm2mqtt/web/')).toBe(1);
});

test('the ✕ in the addon dropdown closes a kept page; closing the one on screen leaves for Status', async ({page}) => {
    await page.goto('/');
    await openFrontend(page, 'RedMatic');
    await expect(frameOf(page, '/addons/red/')).toBeVisible();
    await openFrontend(page, 'Homematic-Manager');
    await expect(frameOf(page, '/addons/mh/')).toBeVisible();
    await expect(kept(page)).toHaveCount(2);

    await menuOpen(page);
    await expect(row(page, 'RedMatic').getByRole('button', {name: 'Close RedMatic'})).toBeVisible();
    await expect(row(page, 'Mosquitto').getByRole('button')).toHaveCount(1); // its ? only
    // the rows without a ✕ keep its column, so ⚙ still lines up
    const gearX = async (n: string) => (await row(page, n).getByRole('link', {name: `Settings for ${n}`}).boundingBox())!.x;
    expect(Math.abs((await gearX('Mosquitto')) - (await gearX('RedMatic')))).toBeLessThan(0.5);

    await row(page, 'RedMatic').getByRole('button', {name: 'Close RedMatic'}).click();
    await expect(kept(page)).toHaveCount(1);
    await expect(page).toHaveURL(/\/nav\/mh$/);
    await expect(frameOf(page, '/addons/mh/')).toBeVisible();
    expect(await loads(page, '/addons/mh/')).toBe(1);

    await menuOpen(page);
    await expect(row(page, 'RedMatic').getByRole('button', {name: 'Close RedMatic'})).toHaveCount(0);
    await row(page, 'Homematic-Manager').getByRole('button', {name: 'Close Homematic-Manager'}).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
    await expect(kept(page)).toHaveCount(0);
});

test('a kept page goes when its addon was stopped, and every one goes on logout', async ({page}) => {
    await page.goto('/');
    await openFrontend(page, 'RedMatic');
    await expect(frameOf(page, '/addons/red/')).toBeVisible();
    await openFrontend(page, 'Homematic-Manager');
    await expect(frameOf(page, '/addons/mh/')).toBeVisible();

    // the Services page stops RedMatic: from now on the addon list says so
    await page.route('**/api/system/v1/addons', async (r) => {
        const res = await r.fetch();
        const body = (await res.json()) as {addons: {id: string; running: boolean; pid?: number}[]};
        body.addons = body.addons.map((a) => (a.id === 'redmatic' ? {...a, running: false, pid: 0} : a));
        return r.fulfill({response: res, json: body});
    });
    // task 57: the System tab opens the popover menu
    await page.locator('.ol-systab').click();
    await page.locator('.ol-sysmenu').getByRole('menuitem', {name: 'Services'}).click();
    await expect(page.getByRole('heading', {level: 1, name: 'Services'})).toBeVisible();
    await expect(kept(page)).toHaveCount(2);
    // leaving the page that operates addons looks again
    await statusTab(page).click();
    await expect(frameOf(page, '/addons/red/')).toHaveCount(0);
    await expect(frameOf(page, '/addons/mh/')).toHaveCount(1);

    await page.evaluate(() => dispatchEvent(new CustomEvent('ol:unauthenticated')));
    await expect(page.locator('form.ol-card')).toBeVisible();
    await expect(kept(page)).toHaveCount(0);
});
