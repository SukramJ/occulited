import {expect, test} from '@playwright/test';
import {buttonBecomesPanel} from './panels';

// openccu-lite task 220 (the maintainer, Q&A 2026-09-24): a LAN devices section (on its own page
// since task 222) - eQ-3's LAN devices as NetFinder finds them, the network settings of the gateways and the
// access points, other systems read-only with a link. The sticker password is asked every time; a
// configured gateway's key is the system's own.

const card = (page: import('@playwright/test').Page, serial: string) => page.locator(`[data-lan-device="${serial}"]`);

test('the found devices, each with what the system knows about it', async ({page}) => {
    await page.goto('/system/lan-devices');
    await expect(page.locator('h2#lan-devices')).toHaveText('LAN devices');
    const hap = card(page, '30150377DC0003DB3393B323');
    await expect(hap.locator('.ol-card-title')).toHaveText('HmIP-HAP');
    await expect(hap.locator('[data-lan-ip]')).toHaveText('192.0.2.155');
    await expect(hap.locator('[data-lan-mode]')).toHaveText('DHCP, then Auto IP');
    await expect(hap).toContainText('paired to HmIP-RF');
    const wired = card(page, 'LEQ0636432');
    await expect(wired.locator('.ol-card-title')).toHaveText('Wired');
    await expect(wired).toContainText('BidCos-Wired (hs485d)');
    await expect(wired.locator('[data-lan-mode]')).toHaveText('static');
    // another system: a link, no settings
    const ccu = card(page, '4118f6a32');
    await expect(ccu.getByRole('link', {name: 'Open its web UI'})).toHaveAttribute('href', 'http://192.0.2.230/');
    await expect(ccu.locator('[data-lan-edit]')).toHaveCount(0);
    // an unconfigured gateway is taken into the add form
    await card(page, 'KEQ1065511').locator('[data-lan-pick]').click();
    const form = page.getByRole('group', {name: 'Add a gateway'});
    await expect(form.getByPlaceholder('NEQ0123456')).toHaveValue('KEQ1065511');
    await expect(form.getByPlaceholder('192.168.1.50')).toHaveValue('192.0.2.124');
    // the access point list shows the address and the mode from the find
    await expect(page.locator('[data-access-point="0003DB3393B323"] [data-ap-ip]')).toContainText('DHCP');
});

test('the network settings of an access point: the sticker password, the rails, the result', async ({page}) => {
    // what this page posted, not the stub's shared log: the tests run in parallel, and the next one
    // writes the gateway's settings at the same time
    const posted: {url: string; body: Record<string, unknown>}[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && /\/radio\/lan-devices\/[^/]+\/network$/.test(r.url())) posted.push({url: new URL(r.url()).pathname, body: r.postDataJSON()});
    });
    await page.goto('/system/lan-devices');
    await card(page, '30150377DC0003DB3393B323').locator('[data-lan-edit]').click();
    // task 247: an in-page panel in the device's card, not a dialog
    const dialog = card(page, '30150377DC0003DB3393B323').getByRole('group', {name: 'Network settings'});
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(dialog).toContainText('Password (PW on the sticker)');
    await expect(dialog).toContainText('Used for this change only; it is not stored.');
    // without the password nothing is sent
    await expect(dialog.locator('[data-lan-save]')).toBeDisabled();
    await dialog.getByLabel('Static address').check();
    await expect(dialog.locator('[data-lan-field="ip"]')).toHaveValue('192.0.2.155');
    // the lab (2026-09-24): the HAP-B1 kept DHCP on after a static write and a power cycle
    await expect(dialog.locator('[data-lan-ap-dhcp]')).toContainText('keeps DHCP on');
    await dialog.locator('input[type="password"], input.hmm-input').last().fill('wrong');
    await dialog.locator('[data-lan-save]').click();
    await expect(dialog.locator('[data-lan-error]')).toHaveText('The device did not take the password.');
    // another subnet has to be confirmed
    await dialog.locator('input[type="password"], input.hmm-input').last().fill('stickerpw');
    await dialog.locator('[data-lan-field="ip"]').fill('10.1.2.3');
    await dialog.locator('[data-lan-save]').click();
    await expect(dialog.locator('[data-lan-error]')).toContainText('none of the networks of the system');
    await dialog.locator('[data-lan-field="ip"]').fill('192.0.2.160');
    await dialog.locator('[data-lan-save]').click();
    await expect(page.locator('[data-lan-result]')).toContainText('the network settings were taken. It answers with 192.0.2.160 now.');
    await expect(dialog).toHaveCount(0);
    // three posts - the wrong password, the other subnet, the one taken - all to this device
    expect(posted.map((p) => p.url)).toEqual(Array(3).fill('/api/system/v1/radio/lan-devices/30150377DC0003DB3393B323/network'));
    expect(posted.at(-1)!.body).toMatchObject({dhcp: false, ip: '192.0.2.160', netmask: '255.255.255.0', password: 'stickerpw'});
    // and the stub took exactly that for this device
    const writes: Record<string, unknown>[] = await (await page.request.get('/api/stub/lan-writes')).json();
    expect(writes.filter((w) => w.serial === '30150377DC0003DB3393B323').at(-1)).toMatchObject({dhcp: false, ip: '192.0.2.160', netmask: '255.255.255.0', password: 'stickerpw'});
});

test('a configured gateway: no password asked, the configuration follows', async ({page}) => {
    await page.goto('/system/lan-devices');
    await card(page, 'LEQ0636432').locator('[data-lan-edit]').click();
    const dialog = card(page, 'LEQ0636432').getByRole('group', {name: 'Network settings'});
    await expect(dialog).toContainText("The gateway's key is taken from the configuration of the system.");
    await expect(dialog.locator('input[type="password"]')).toHaveCount(0);
    await dialog.getByLabel('DHCP (and Auto IP without a DHCP server)').check();
    await dialog.locator('[data-lan-save]').click();
    await expect(page.locator('[data-lan-result]')).toContainText('hs485d restarted');
});

test('nobody answers: the page says where to look', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-lan', value: 'none', url: baseURL!}]);
    await page.goto('/system/lan-devices');
    await expect(page.locator('[data-lan-none]')).toContainText('discovery replies (from udp 43439)');
});

test('in German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/lan-devices');
    await expect(page.locator('h2#lan-devices')).toHaveText('LAN-Geräte');
    await expect(card(page, '30150377DC0003DB3393B323').locator('[data-lan-mode]')).toHaveText('DHCP, dann Auto-IP');
    await expect(card(page, '30150377DC0003DB3393B323').locator('[data-lan-edit]')).toHaveText('Netzwerkeinstellungen');
});

// openccu-lite task 237 (the maintainer): "if it sends something to the network that's an outgoing
// connection i want it only when user presses the button". Opening the page reads the last search
// (GET, which sends nothing); only Search posts one.
test('opening the page searches nothing; Search does', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-lan', value: 'unsearched', url: baseURL!}]);
    const sent: string[] = [];
    page.on('request', (r) => {
        if (r.url().includes('/radio/lan-devices') || r.url().includes('/hb-rf-eth/find')) sent.push(`${r.method()} ${new URL(r.url()).pathname}`);
    });
    await page.goto('/system/lan-devices');
    await expect(page.locator('[data-lan-unsearched]')).toContainText('Not searched yet');
    await expect(page.locator('[data-lan-scanned]')).toHaveCount(0);
    await page.waitForTimeout(300);
    expect(sent.filter((s) => s.startsWith('POST'))).toEqual([]);
    expect(sent).toEqual(['GET /api/system/v1/radio/lan-devices']);
    const search = page.locator('[data-lan-search]');
    await expect(search).toHaveText('Search');
    await search.click();
    await expect(card(page, 'KEQ1065511')).toBeVisible();
    expect(sent).toContain('POST /api/system/v1/radio/lan-devices/search');
    await expect(page.locator('[data-lan-scanned]')).toContainText('Last search:');
    await expect(page.locator('[data-lan-unsearched]')).toHaveCount(0);
});

test('a search kept from before is shown with its time, without a new one', async ({page}) => {
    const posts: string[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST') posts.push(new URL(r.url()).pathname);
    });
    await page.goto('/system/lan-devices');
    await expect(card(page, 'KEQ1065511')).toBeVisible();
    await expect(page.locator('[data-lan-scanned]')).toContainText('Last search:');
    await page.waitForTimeout(300);
    expect(posts).toEqual([]);
});

test('German: Suchen, and the button keeps its icon and label on one line', async ({page, context, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await context.addCookies([{name: 'stub-lan', value: 'unsearched', url: baseURL!}]);
    await page.goto('/system/lan-devices');
    await expect(page.locator('[data-lan-unsearched]')).toContainText('Noch nicht gesucht');
    const b = page.locator('[data-lan-search]');
    await expect(b).toHaveText('Suchen');
    const box = (await b.boundingBox())!;
    const icon = (await b.locator('svg').boundingBox())!;
    expect(Math.abs(icon.y + icon.height / 2 - (box.y + box.height / 2))).toBeLessThan(3);
    expect(box.height).toBeLessThan(34);
});

// openccu-lite task 238 (the maintainer): a green check for a device this system uses, a grey mark
// for one another system uses - where that can be told: the HB-RF-ETH names its host; a gateway or an
// access point cannot be asked without taking it, so it gets no grey mark.
test('whose a device is: the green check for this system, the grey padlock for another', async ({page}) => {
    await page.goto('/system/lan-devices');
    await expect(card(page, 'LEQ0636432').locator('[data-use="self"]')).toHaveAttribute('title', 'Used by this system: BidCos-Wired (hs485d)');
    await expect(card(page, '30150377DC0003DB3393B323').locator('[data-use="self"]')).toHaveAttribute('aria-label', 'Used by this system: paired to HmIP-RF');
    // an unconfigured gateway and another CCU: no mark
    await expect(card(page, 'KEQ1065511').locator('[data-use]')).toHaveCount(0);
    await expect(card(page, '4118f6a32').locator('[data-use]')).toHaveCount(0);
    await expect(page.locator('[data-lan-legend]')).toContainText('used by this system');
    // the HB-RF-ETH's Find: another system's board carries the padlock with its host
    await page.locator('[data-hb-find]').click();
    const other = page.locator('[data-hb-found-board="192.0.2.99"] [data-use="other"]');
    await expect(other).toHaveAttribute('title', 'Used by another system: 192.0.2.7');
    await expect(page.locator('[data-hb-found-board="192.0.2.50"] [data-use]')).toHaveCount(0);
    // not by colour alone: two shapes
    const shape = async (sel: string) => page.locator(sel).locator('svg').innerHTML();
    expect(await shape('[data-lan-device="LEQ0636432"] [data-use="self"]')).not.toBe(await shape('[data-hb-found-board="192.0.2.99"] [data-use="other"]'));
});

test('whose a device is, in German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/lan-devices');
    await expect(card(page, 'LEQ0636432').locator('[data-use="self"]')).toHaveAttribute('title', 'Von diesem System verwendet: BidCos-Wired (hs485d)');
    await page.locator('[data-hb-find]').click();
    await expect(page.locator('[data-hb-found-board="192.0.2.99"] [data-use="other"]')).toHaveAttribute('title', 'Von einem anderen System verwendet: 192.0.2.7');
});

// task 247 (the maintainer: "a panel opens animated inside the page, like e.g. add gateway"): the
// button opens the panel in its card and closes it again; Cancel closes it; another card's button
// moves it there
test('the network settings open in the card and close again', async ({page}) => {
    await page.goto('/system/lan-devices');
    const hap = card(page, '30150377DC0003DB3393B323');
    const gw = card(page, 'LEQ0636432');
    const button = hap.locator('[data-lan-edit]');
    const panel = hap.getByRole('group', {name: 'Network settings'});
    // task 268: the button becomes the panel, Cancel brings it back
    await buttonBecomesPanel(button, panel);
    // another card's button while one is open: that card's panel, this one's button back
    await button.click();
    await expect(panel).toBeVisible();
    await gw.locator('[data-lan-edit]').click();
    await expect(gw.getByRole('group', {name: 'Network settings'})).toBeVisible();
    await expect(gw.locator('[data-lan-edit]')).toBeHidden();
    await expect(panel).toHaveCount(0);
    await expect(button).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);
    expect(overflow).toBe(false);
});
