import {expect, test} from '@playwright/test';

// task 139: one Addons page built on the catalogue - installed addons first (updates on top), the
// rest by stars; a card with the unit's state dot, the version, the badges, Open/Settings/Update,
// the Log and Service links, the rare actions in one ⋯ menu; the filter and Installed only above;
// no pin and no ordering here (the Addons popup keeps them), one quiet line points at it
const card = (page: import('@playwright/test').Page, id: string) => page.locator(`#addon-row-${id}`);

test('the cards: installed first with the update on top, then by stars; logos only from the box; no request outside', async ({page, baseURL}, info) => {
    const outside: string[] = [];
    page.on('request', (r) => {
        if (/^https?:/.test(r.url()) && !r.url().startsWith(baseURL!)) outside.push(r.url());
    });
    await page.goto('/addons');
    await expect(page.getByRole('heading', {level: 1, name: 'Addons'})).toBeVisible();
    const cards = page.locator('.ad-card');
    await expect(cards.locator('.ad-name')).toHaveText(['RedMatic', 'hm2mqtt', 'Homematic-Manager', 'JP HB Devices', 'Mosquitto', 'XML-API', 'SukramJ/openccu-loom', 'TM Devices']);
    // the update at the top says old → new; the state and its dot as the Services page has them
    const red = card(page, 'redmatic');
    await expect(red.locator('.ad-sub')).toContainText('9.4.0');
    await expect(red.locator('.ol-upd')).toHaveText('→ 9.4.1');
    await expect(red.locator('[data-addon-dot]')).toHaveAttribute('data-addon-dot', 'running');
    await expect(red.locator('[data-addon-dot]')).toHaveClass(/\bok\b/);
    await expect(red.locator('[data-addon-state]')).toHaveText('· Running');
    await expect(card(page, 'jp-hb-devices-addon').locator('[data-addon-dot]')).toHaveAttribute('data-addon-dot', 'completed');
    // the logos: RedMatic's and Mosquitto's from their Info lines, a letter for the rest
    await expect(red.locator('.ad-logo img')).toHaveAttribute('src', '/addons/redmatic/redmatic5-wide.png');
    await expect(card(page, 'mosquitto').locator('.ad-logo img')).toHaveAttribute('src', '/addons/mosquitto/mosquitto-text-side-28.png');
    await expect(card(page, 'mh').locator('.ad-letter')).toHaveText('H');
    await expect(card(page, 'tm-devices').locator('.ad-letter')).toHaveText('T');
    // the stars link the repository; a card without a count says Repository
    await expect(red.locator('a.cat-stars')).toHaveAttribute('href', 'https://github.com/rdmtc/RedMatic');
    await expect(card(page, 'tm-devices').locator('a.cat-stars')).toHaveText(/^Repository/);
    // task 255: an entry the catalogue marks untested says so; there is no verified mark any more
    await expect(card(page, 'tm-devices').locator('.ol-badge')).toHaveText(['untested', 'prerelease']);
    await expect(card(page, 'tm-devices').locator('.ad-untested')).toHaveCount(1);
    await expect(card(page, 'repo-SukramJ-openccu-loom').locator('.ad-untested')).toHaveText('untested');
    await expect(card(page, 'mosquitto').locator('.ad-untested')).toHaveCount(0);
    await expect(page.locator('.ad-badges').getByText('verified', {exact: true})).toHaveCount(0);
    // an entry whose manifest is not fetched yet is a card by its repository, without an Install button
    const loom = card(page, 'repo-SukramJ-openccu-loom');
    await expect(loom).toContainText('SukramJ/openccu-loom');
    await expect(loom).toContainText('Not checked yet');
    await expect(loom.locator('[data-addon-install]')).toHaveCount(0);
    await expect(card(page, 'tm-devices')).toContainText('Not installed');
    // the primary actions and the links of an installed card; Install on one that is not
    await expect(red.locator('[data-addon-open]')).toHaveAttribute('href', '/nav/red');
    await expect(red.getByRole('link', {name: 'Settings'})).toHaveAttribute('href', '/addon-settings/redmatic');
    await expect(red.locator('[data-addon-update]')).toHaveText('Update to 9.4.1');
    await expect(red.locator('[data-addon-service]')).toHaveAttribute('href', '/system/services#service-addon-redmatic');
    await expect(red.locator('[data-addon-log]')).toHaveAttribute('href', '/system/log?unit=addon-redmatic');
    await expect(red.locator('[data-addon-firewall]')).toHaveAttribute('href', '/addons#addon-ports');
    await expect(red.locator('.ad-pinhint')).toHaveText('Pin it to the menu in the Addons menu');
    await expect(card(page, 'mosquitto').locator('[data-addon-open]')).toHaveCount(0);
    await expect(card(page, 'mosquitto').locator('[data-addon-update]')).toHaveCount(0);
    await expect(card(page, 'xml-api').locator('[data-addon-install]')).toBeVisible();
    await expect(card(page, 'xml-api').locator('[data-addon-service]')).toHaveCount(0);
    // one update waits: no Update all
    await expect(page.locator('[data-update-all]')).toHaveCount(0);
    // no pin, no handle on this page
    await expect(page.locator('main button.ol-pin, main button.ol-grip')).toHaveCount(0);
    // two columns on a desktop, one on a phone
    const lefts = new Set(await cards.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().left))));
    expect(lefts.size).toBe(info.project.name === 'phone' ? 1 : 2);
    expect(outside).toEqual([]);
});

test('the filter, Installed only (remembered), and the old /catalog?q= alias', async ({page}) => {
    await page.goto('/catalog?q=mat');
    await expect(page).toHaveURL(/\/addons\?q=mat$/);
    const cards = page.locator('.ad-card');
    await expect(cards.locator('.ad-name')).toHaveText(['RedMatic', 'Homematic-Manager']);
    await page.goto('/addons');
    await expect(cards).toHaveCount(8); // the seven with a manifest and the one not checked yet
    await page.getByRole('checkbox', {name: 'Installed only'}).check();
    await expect(cards.locator('.ad-name')).toHaveText(['RedMatic', 'hm2mqtt', 'Homematic-Manager', 'JP HB Devices', 'Mosquitto']);
    await page.reload();
    await expect(page.getByRole('checkbox', {name: 'Installed only'})).toBeChecked();
    await expect(cards).toHaveCount(5);
    await page.getByRole('checkbox', {name: 'Installed only'}).uncheck();
    await page.getByPlaceholder('Filter').fill('zzz');
    await expect(page.getByText('Nothing matches the filter.')).toBeVisible();
});

test('the ⋯ menu holds Stop, Restart, Disable at boot, Reinstall and Uninstall; Start on a stopped one', async ({page}) => {
    const posted: string[] = [];
    await page.route('**/api/system/v1/services/addon-*/*', (route) => {
        posted.push(new URL(route.request().url()).pathname);
        return route.fulfill({json: {output: ''}});
    });
    await page.goto('/addons');
    const red = card(page, 'redmatic');
    await red.getByRole('button', {name: 'More actions'}).click();
    const menu = red.getByRole('menu');
    await expect(menu.getByRole('menuitem')).toHaveText(['Restart', 'Stop', 'Disable at boot', 'Reinstall from the catalogue', 'Uninstall']);
    await menu.getByRole('menuitem', {name: 'Stop'}).click();
    await expect.poll(() => posted).toEqual(['/api/system/v1/services/addon-redmatic/stop']);
    await page.unrouteAll({behavior: 'ignoreErrors'});
});

// openccu-lite task 146 (D-106): the addons a restore brought back without their program files -
// listed above the toolbar with Reinstall where the catalogue knows the addon, "install it by hand"
// where it does not, Reinstall all, and Dismiss; the list is what the API marks payload_missing.
test('after a restore: the reinstall section, Reinstall from the catalogue, by hand, Dismiss', async ({page}) => {
    const posts: string[] = [];
    await page.route('**/api/system/v1/addons', async (r) => {
        const res = await r.fetch();
        const body = await res.json();
        body.addons = body.addons.map((a: {id: string}) =>
            a.id === 'redmatic' ? {...a, running: false, payload_missing: true, payload_missing_dirs: ['bin', 'lib', 'www']} : a.id === 'hm2mqtt' ? {...a, payload_missing: true, payload_missing_dirs: ['bin'], reinstall_dismissed: true} : a);
        // B-267: an addon whose unit the program check skipped, with no directory this daemon could name
        body.addons.push({id: 'foo', name: 'Foo', version: '1.2', operations: [], running: false, enabled: true, skipped: true, payload_missing: true});
        await r.fulfill({response: res, json: body});
    });
    await page.route('**/api/system/v1/catalog/redmatic/install', (r) => {
        posts.push('install redmatic');
        return r.fulfill({status: 202, json: {ok: true, addon_id: 'redmatic'}});
    });
    await page.route('**/api/system/v1/addons/foo/reinstall-dismiss', (r) => {
        posts.push('dismiss foo');
        return r.fulfill({json: {ok: true, id: 'foo', version: '1.2'}});
    });
    await page.goto('/addons');
    const section = page.locator('[data-section="reinstall"]');
    await expect(section.getByRole('heading', {name: 'Addons to reinstall after the restore'})).toBeVisible();
    // two rows: the dismissed one is not listed
    await expect(section.locator('li')).toHaveCount(2);
    const red = section.locator('[data-reinstall="redmatic"]');
    await expect(red).toContainText('RedMatic');
    await expect(red).toContainText('missing: bin, lib, www');
    // B-267: every row says what happened and what to do
    await expect(red.locator('[data-reinstall-note]')).toHaveText('Installed before the restore; reinstall it.');
    await expect(red.locator('[data-action="reinstall"]')).toHaveText('Reinstall 9.4.1');
    const foo = section.locator('[data-reinstall="foo"]');
    await expect(foo).toContainText('not in the catalogue — install it by hand');
    await expect(foo.locator('[data-reinstall-note]')).toHaveText('Installed before the restore; reinstall it.');
    await expect(foo.locator('.ad-reinstall-dirs')).toHaveCount(0);
    await expect(foo.locator('[data-action="reinstall"]')).toHaveCount(0);
    // one reinstallable: no Reinstall all
    await expect(section.locator('[data-action="reinstall-all"]')).toHaveCount(0);
    // Reinstall asks, then posts the catalogue install
    await red.locator('[data-action="reinstall"]').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Reinstall RedMatic 9.4.1 from the catalogue?');
    await dialog.getByRole('button', {name: 'Reinstall', exact: true}).click();
    await expect.poll(() => posts).toEqual(['install redmatic']);
    // Dismiss asks, then posts
    await foo.locator('[data-action="reinstall-dismiss"]').click();
    await expect(dialog).toContainText('Hide the reinstall hint for Foo?');
    await dialog.getByRole('button', {name: 'Dismiss', exact: true}).click();
    await expect.poll(() => posts).toEqual(['install redmatic', 'dismiss foo']);
    // occulited B-12: the page loads /addons again after each action, and the patched route is
    // still inside r.fetch() or res.json() when the last poll above is satisfied - the context's
    // close then disposed the response under it (1 run in ~400 in the full suite). Let the
    // handlers finish before the test ends.
    await page.unrouteAll({behavior: 'wait'});
});

test('after a restore, in German; no section when nothing is missing', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/addons');
    await expect(page.locator('[data-section="reinstall"]')).toHaveCount(0);
    // task 255: the untested label in German
    await expect(page.locator('.ad-untested').first()).toHaveText('ungetestet');
    await page.route('**/api/system/v1/addons', async (r) => {
        const res = await r.fetch();
        const body = await res.json();
        body.addons = body.addons.map((a: {id: string}) => (a.id === 'redmatic' || a.id === 'mosquitto' ? {...a, payload_missing: true, payload_missing_dirs: ['bin']} : a));
        await r.fulfill({response: res, json: body});
    });
    await page.goto('/addons');
    const section = page.locator('[data-section="reinstall"]');
    await expect(section.getByRole('heading', {name: 'Nach der Wiederherstellung neu zu installierende Addons'})).toBeVisible();
    await expect(section.locator('li')).toHaveCount(2);
    await expect(section.locator('[data-action="reinstall-all"]')).toHaveText('Alle neu installieren (2)');
    // B-267: the line per addon, with "Sie", and the reinstall button
    const red = section.locator('[data-reinstall="redmatic"]');
    await expect(red.locator('[data-reinstall-note]')).toHaveText('Vor der Wiederherstellung installiert; installieren Sie es neu.');
    await expect(red.locator('[data-action="reinstall"]')).toHaveText(/neu installieren$/);
    await page.unrouteAll({behavior: 'wait'}); // B-12: as above
});
