import {expect, test, type Page} from '@playwright/test';

// openccu-lite task 219: a program's request on the Status page - the code, the access per area
// with its own words, devices at administer in red, the look-alike mark, Approve and Reject - and
// the API tokens panel's switch and a paired program's line.

const REQ = {
    id: 'a1b2c3', app: 'homematic-manager', app_version: '3.0.0', instance: 'pc', name: 'homematic-manager on pc', address: '192.168.1.50',
    access: {devices: 'administer', names: 'configure', system: 'read'}, purpose: {devices: 'pairs and configures devices'},
    scopes: ['meta:write', 'rpc:admin', 'system:read'], code: '482719', fingerprint: 'AB:CD:EF:01:23:45:67:89:AB:CD:EF', created: '2026-09-25T00:00:00Z', expires: '2026-09-25T00:05:00Z',
};

async function withRequests(page: Page, requests: unknown[]) {
    await page.route('**/api/auth/v1/pairing', (route) => route.fulfill({json: {enabled: true, requests}}));
    await page.route('**/api/auth/v1/pairing/stream', (route) => route.fulfill({status: 200, contentType: 'text/event-stream', body: `event: pairing\ndata: ${JSON.stringify({enabled: true, requests})}\n\n`}));
}

test('the card: code, access, approve with the code', async ({page}) => {
    await withRequests(page, [{...REQ, look_alike: true}, {...REQ, look_alike: true, id: 'd4e5f6', code: '915204', address: '192.168.1.66', access: {devices: 'read'}, purpose: {}, fingerprint: ''}]);
    let body: unknown;
    await page.route('**/api/auth/v1/pairing/a1b2c3/approve', async (route) => {
        body = route.request().postDataJSON();
        await route.fulfill({json: {ok: true, token: 'homematic-manager-pc'}});
    });
    await page.goto('/');
    const card = page.locator('[data-pair="a1b2c3"]');
    await expect(card).toContainText('homematic-manager on pc asks for access');
    await expect(card.locator('[data-pair-code]')).toHaveText('482 719');
    await expect(card.locator('[data-pair-area="devices"] dd')).toContainText('administer · may pair and delete devices');
    await expect(card.locator('[data-pair-area="devices"] dd')).toHaveClass(/pr-red/);
    await expect(card.locator('[data-pair-area="devices"] dd')).toContainText('“pairs and configures devices”');
    await expect(card.locator('[data-pair-area="names"] dd')).toHaveText('configure');
    await expect(card.locator('[data-pair-lookalike]')).toBeVisible();
    await expect(card.locator('[data-pair-cert]')).toContainText('bound to the certificate');
    await expect(page.locator('[data-pair="d4e5f6"] [data-pair-cert]')).toContainText('Plain HTTP');
    await expect(page.locator('[data-pair="d4e5f6"] [data-pair-area="system"] dd')).toHaveText('none');
    await page.locator('[data-notice="pairing"]').first().getByRole('button', {name: 'Approve'}).click();
    await expect.poll(() => body).toEqual({code: '482719'});
    await expect(page.locator('[data-pair="a1b2c3"]')).toHaveCount(0);
});

// openccu-lite task 307: a request that asks for an addon's pages through the system (the scope
// addon:<id>) names the addon on the card, alone or beside the areas, in both languages.
test('the card names the addons asked for', async ({page}) => {
    await withRequests(page, [{...REQ, id: 'f0f0f0', name: 'homematicip-local on ha', access: {}, purpose: {}, scopes: ['addon:openccu-loom'], addons: [{id: 'openccu-loom', name: 'OpenCCU-Loom'}]}]);
    await page.goto('/');
    const card = page.locator('[data-pair="f0f0f0"]');
    await expect(card).toContainText('homematicip-local on ha asks for access');
    // B-296: the addon is the only grant - no areas at "none"
    await expect(card.locator('[data-pair-area]')).toHaveCount(0);
    await expect(card.locator('[data-pair-addon="openccu-loom"] dt')).toHaveText('Addon');
    await expect(card.locator('[data-pair-addon="openccu-loom"] dd')).toContainText('OpenCCU-Loom: its pages through the system');
    await expect(card.locator('[data-pair-addon="openccu-loom"] dd')).toContainText('addon:openccu-loom');
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/');
    await expect(page.locator('[data-pair="f0f0f0"] [data-pair-addon="openccu-loom"] dt')).toHaveText('Zusatzsoftware');
    await expect(page.locator('[data-pair="f0f0f0"] [data-pair-addon="openccu-loom"] dd')).toContainText('OpenCCU-Loom: ihre Seiten über das System');
});

// openccu-lite B-296 (@SukramJ, GitHub #3): a system before the fix listed an addons-only request
// with access: null, the card threw while rendering and was not drawn - the administrator could not
// approve it. With null as with {}, the card shows the addon as its only grant, and Approve works;
// beside an area the addon row stays with the areas.
test('an addons-only request with access null: the card renders and approves', async ({page}) => {
    await withRequests(page, [
        {...REQ, id: 'b296aa', name: 'homematicip-local on ha', access: null, purpose: null, scopes: ['addon:openccu-loom'], addons: [{id: 'openccu-loom', name: 'OpenCCU-Loom'}]},
        {...REQ, id: 'b296bb', code: '111222', access: {system: 'read'}, purpose: {}, scopes: ['system:read', 'addon:hmm'], addons: [{id: 'hmm', name: 'Homematic Manager'}]},
    ]);
    let body: unknown;
    await page.route('**/api/auth/v1/pairing/b296aa/approve', async (route) => {
        body = route.request().postDataJSON();
        await route.fulfill({json: {ok: true, token: 'homematicip-local-ha'}});
    });
    await page.goto('/');
    const card = page.locator('[data-pair="b296aa"]');
    await expect(card).toContainText('homematicip-local on ha asks for access');
    await expect(card.locator('[data-pair-area]')).toHaveCount(0);
    await expect(card.locator('[data-pair-addon="openccu-loom"] dd')).toContainText('OpenCCU-Loom: its pages through the system');
    const mixed = page.locator('[data-pair="b296bb"]');
    await expect(mixed.locator('[data-pair-area]')).toHaveCount(3);
    await expect(mixed.locator('[data-pair-area="system"] dd')).toHaveText('read');
    await expect(mixed.locator('[data-pair-addon="hmm"]')).toBeVisible();
    await page.locator('[data-notice="pairing"]').filter({has: card}).getByRole('button', {name: 'Approve'}).click();
    await expect.poll(() => body).toEqual({code: '482719'});
    await expect(card).toHaveCount(0);
});

test('no request, no card; German', async ({page}) => {
    await page.goto('/');
    await expect(page.locator('[data-notice="pairing"]')).toHaveCount(0);
    await withRequests(page, [REQ]);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/');
    await expect(page.locator('[data-pair="a1b2c3"]')).toContainText('homematic-manager on pc bittet um Zugriff');
    await expect(page.locator('[data-notice="pairing"]').getByRole('button', {name: 'Genehmigen'})).toBeVisible();
});

test('the API tokens panel: the switch and a paired program', async ({page}) => {
    await page.route('**/api/auth/v1/tokens', (route) =>
        route.fulfill({json: {scopes: ['meta:read', 'rpc:read'], tokens: [{name: 'hm2mqtt-js-nas', scopes: ['meta:read', 'rpc:operate'], prefix: 'abcd1234', created: '2026-09-25T00:00:00Z',
            client: {app: 'hm2mqtt.js', instance: 'nas', label: 'hm2mqtt.js on nas', paired_at: '2026-09-25T00:00:00Z', paired_by: 'admin', address: '192.168.1.50', last_address: '192.168.1.51'}}]}}));
    let put: unknown;
    await page.route('**/api/auth/v1/pairing/settings', async (route) => {
        put = route.request().postDataJSON();
        await route.fulfill({json: {enabled: false, requests: []}});
    });
    await page.goto('/system/remote-access#api-tokens');
    await expect(page.locator('[data-paired="hm2mqtt-js-nas"]')).toHaveText('hm2mqtt.js on nas · paired by admin · last from 192.168.1.51');
    const sw = page.locator('[data-pairing-switch] input');
    await expect(sw).toBeChecked();
    await sw.uncheck();
    await expect.poll(() => put).toEqual({enabled: false});
    await expect(sw).not.toBeChecked();
    let patch: unknown;
    await page.route('**/api/auth/v1/tokens/hm2mqtt-js-nas', async (route) => {
        patch = route.request().postDataJSON();
        await route.fulfill({json: {}});
    });
    await page.locator('[data-rename="hm2mqtt-js-nas"]').click();
    const dlg = page.getByRole('dialog');
    await dlg.getByRole('textbox').fill('MQTT bridge');
    await dlg.getByRole('button', {name: 'Save'}).click();
    await expect.poll(() => patch).toEqual({label: 'MQTT bridge'});
});
