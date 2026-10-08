import {expect, test, type Page} from '@playwright/test';

// occulited task 24 (openccu-lite #11): an addon whose manifest declares ui.fullscreen - its frontend
// brings a header and a menu of its own and offers its own way back - gets a card on the Settings
// page, beside Control's; ticked, the shell shows its frontend without the top bar. Off until ticked,
// kept with the account as a flag on the addon's entry in the preferences. The stub marks
// Homematic-Manager so only for a browser with stub-addon-fullscreen=1; RedMatic never declares it.
// Both themes and the phone run it (the projects).

const header = (page: Page) => page.locator('header.ol-header');
const card = (page: Page) => page.locator('[data-setting="addon-fullscreen"][data-addon="mh"]');
const kept = (page: Page) => page.locator('iframe.ol-kept-frame[src^="/addons/mh/"]');

async function cookies(page: Page, baseURL: string | undefined, declared: boolean): Promise<void> {
    const list = [{name: 'stub-prefs', value: `full-${Math.random().toString(36).slice(2)}`, url: baseURL!}];
    if (declared) list.push({name: 'stub-addon-fullscreen', value: '1', url: baseURL!});
    await page.context().addCookies(list);
}

function putPrefs(page: Page) {
    return page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/api/auth/v1/me/preferences'));
}

/** the shell has read the account's preferences */
async function gotoWithPrefs(page: Page, path: string): Promise<void> {
    await Promise.all([page.waitForResponse((r) => r.request().method() === 'GET' && r.url().endsWith('/api/auth/v1/me/preferences')), page.goto(path)]);
}

test('a declaring addon: the card, the frontend without the top bar once ticked, kept with the account', async ({page, baseURL}) => {
    await cookies(page, baseURL, true);
    await page.goto('/settings');
    // one card, for the addon that declares the flag; RedMatic has none
    await expect(card(page).locator('.k')).toHaveText('Homematic-Manager without the top bar');
    await expect(page.locator('[data-setting="addon-fullscreen"]')).toHaveCount(1);
    await expect(card(page).locator('.ol-card-detail')).toHaveText('The tab bar and the icons stay away while the addon is open; the addon itself offers the way back to the system.');
    const box = card(page).getByRole('checkbox', {name: 'Show Homematic-Manager as the whole window'});
    await expect(box).not.toBeChecked();

    // off by default: the frontend under the bar, as before
    await gotoWithPrefs(page, '/nav/mh');
    await expect(kept(page)).toBeVisible();
    await expect(header(page)).toBeVisible();

    // ticked: the flag on the addon's entry, nothing else in the list yet
    await page.goto('/settings');
    const [req] = await Promise.all([putPrefs(page), box.check()]);
    expect(req.postDataJSON()).toEqual({addons: [{id: 'mh', fullscreen: true}]});
    // Settings itself keeps the bar
    await expect(header(page)).toBeVisible();

    // the frontend fills the window: no bar, the frame there
    await gotoWithPrefs(page, '/nav/mh');
    await expect(kept(page)).toBeVisible();
    await expect(header(page)).toHaveCount(0);
    const box2 = await kept(page).boundingBox();
    expect(box2!.y).toBeLessThan(2);

    // the addon's settings page and another addon's frontend keep the bar
    await page.goto('/addon-settings/mh');
    await expect(page.locator('iframe.ol-kept-frame[src^="/addons/mh/settings.cgi"]')).toBeVisible();
    await expect(header(page)).toBeVisible();
    await page.goto('/nav/red');
    await expect(page.locator('iframe.ol-kept-frame[src^="/addons/red/"]')).toBeVisible();
    await expect(header(page)).toBeVisible();

    // kept with the account: a reload reads it back
    await gotoWithPrefs(page, '/settings');
    await expect(box).toBeChecked();

    // off again: the entry stays in the list, without the flag
    const [req2] = await Promise.all([putPrefs(page), box.uncheck()]);
    expect(req2.postDataJSON()).toEqual({addons: [{id: 'mh'}]});
    await gotoWithPrefs(page, '/nav/mh');
    await expect(kept(page)).toBeVisible();
    await expect(header(page)).toBeVisible();
});

test('the flag rides beside the pin and survives a pin change', async ({page, baseURL}) => {
    await cookies(page, baseURL, true);
    await page.goto('/settings');
    const box = card(page).getByRole('checkbox');
    const [req] = await Promise.all([putPrefs(page), box.check()]);
    expect(req.postDataJSON()).toEqual({addons: [{id: 'mh', fullscreen: true}]});
    // pin the addon from the Addons dropdown: the flag stays on its entry
    const menu = page.locator('.ol-menu').first();
    await menu.locator('.ol-menubtn').click();
    const row = menu.getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: /^Homematic-Manager$/})});
    const [req2] = await Promise.all([putPrefs(page), row.getByRole('button', {name: 'Pin Homematic-Manager to the tab bar'}).click()]);
    const addons = req2.postDataJSON().addons as {id: string; pinned?: boolean; fullscreen?: boolean}[];
    expect(addons.find((a) => a.id === 'mh')).toEqual({id: 'mh', pinned: true, fullscreen: true});
});

test('no card without the declaration, whatever the preference says', async ({page, baseURL}) => {
    await cookies(page, baseURL, false);
    // a stale flag from an earlier version of the addon: harmless, the shell ignores it
    await page.route('**/api/auth/v1/me/preferences', (r) => r.fulfill({json: {addons: [{id: 'mh', fullscreen: true}]}}));
    await page.goto('/settings');
    await expect(page.locator('[data-setting="app-fullscreen"]')).toBeVisible();
    await expect(page.locator('[data-setting="addon-fullscreen"]')).toHaveCount(0);
    await gotoWithPrefs(page, '/nav/mh');
    await expect(kept(page)).toBeVisible();
    await expect(header(page)).toBeVisible();
});

test('German: Zusatzsoftware ohne die Kopfleiste', async ({page, baseURL}) => {
    await cookies(page, baseURL, true);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/settings');
    await expect(card(page).locator('.k')).toHaveText('Homematic-Manager ohne die Kopfleiste');
    await expect(card(page).getByRole('checkbox', {name: 'Homematic-Manager als ganzes Fenster zeigen'})).toBeVisible();
    await expect(card(page).locator('.ol-card-detail')).toHaveText('Die Reiterleiste und die Symbole bleiben weg, solange die Zusatzsoftware offen ist; den Weg zurück zum System bietet die Zusatzsoftware selbst.');
});
