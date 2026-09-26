import {expect, test, type Page} from '@playwright/test';

// Task 199, the maintainer 2026-09-22: "i dont want to have a table for the gateways, i want a
// heading 'BidCoS Gateways', the add gateway button beside the heading and every configured
// gateway as a panel. and this area should be placed between modules and connections area. […]
// only possible actions for a gateway: remove and rename." And, on the HmIP panel of the
// connections, a green tick or a red cross before the sentence about routing.

const card = (page: Page, serial: string) => page.locator(`.ol-card[data-gateway="${serial}"]`);

// openccu-lite task 222 (the maintainer, 2026-09-24): "move lan-devices to their own page, including
// bidcos-gateways and hmip-access-points", and the section buttons below their headings
test('the gateways are cards on the LAN devices page, the add button below the heading', async ({page}) => {
    await page.goto('/system/lan-devices');
    await expect(page.locator('main h2')).toHaveText(['BidCoS Gateways', 'Network radio board (HB-RF-ETH)', 'HmIP Access Points', 'LAN devices']);
    // the eight-column table is gone
    await expect(page.locator('table')).toHaveCount(0);

    // the section's action stands in the toolbar row under its heading, left-aligned with it
    const head = page.locator('.ol-headrow', {has: page.locator('h2#gateways')});
    const add = head.getByRole('button', {name: 'Add gateway'});
    await expect(add).toBeVisible();
    const h = (await page.locator('h2#gateways').boundingBox())!;
    const b = (await add.boundingBox())!;
    expect(b.y).toBeGreaterThanOrEqual(h.y + h.height);
    expect(Math.abs(b.x - h.x)).toBeLessThan(1);
    expect(b.y + b.height).toBeLessThan((await page.locator('.ol-card[data-gateway]').first().boundingBox())!.y);

    const garage = card(page, 'NEQ1234567');
    await expect(garage.locator('.ol-card-title')).toHaveText('Garage');
    await expect(garage.locator('.ol-card-sub')).toHaveText('BidCos-RF · HMLGW2');
    await expect(garage.locator('dt')).toHaveText(['Serial', 'Address', 'Key', '#']);
    await expect(garage.locator('dd')).toHaveText(['NEQ1234567', '192.168.0.51', '•••', '1']);
    // the queued key change of the other one is still said, in its card
    await expect(card(page, 'KEQ0987654').locator('dd').nth(2)).toHaveText('••• (change queued)');

    // rename and remove, and nothing else: his word is that a configured gateway's key is never
    // changed from here
    await expect(garage.getByRole('button')).toHaveText(['Rename', 'Remove']);
    await expect(page.getByRole('button', {name: 'Change key'})).toHaveCount(0);
});

// The maintainer, 2026-09-22: the panels of the section buttons are two of the three card columns
// wide and open above the cards. Task 268: the button is hidden while its panel is open; the panel's
// Cancel / Close closes it and brings the button back.
test('the add panel lines up with the cards, and Cancel brings the button back', async ({page}) => {
    await page.setViewportSize({width: 1280, height: 1000});
    await page.goto('/system/lan-devices');
    const addButton = page.locator('.ol-headrow', {has: page.locator('h2#gateways')}).getByRole('button', {name: 'Add gateway'});
    await addButton.click();
    await expect(page.getByRole('group', {name: 'Add a gateway'})).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.getAnimations().length)).toBe(0);
    const w = await page.evaluate(() => {
        const box = (s: string) => document.querySelector(s)!.getBoundingClientRect();
        const cards = [...document.querySelectorAll('.ol-cards-radio:not(.ol-panelrow) .ol-card[data-gateway]')].map((c) => c.getBoundingClientRect());
        return {panel: box('.ol-panelrow .ol-disclosure'), first: cards[0]!, second: cards[1]!};
    });
    expect(Math.round(w.panel.left)).toBe(Math.round(w.first.left));
    expect(Math.round(w.panel.right)).toBe(Math.round(w.second.right));
    expect(w.panel.bottom).toBeLessThan(w.first.top);
    await expect(page.locator('[data-gw-add]')).toBeHidden();
    await page.getByRole('group', {name: 'Add a gateway'}).getByRole('button', {name: 'Cancel'}).click();
    await expect(page.getByRole('group', {name: 'Add a gateway'})).toHaveCount(0);
    await expect(addButton).toBeVisible();
});

test('the USB devices button stands under the Modules heading on Interfaces, and opens its panel above the cards, full width (task 249)', async ({page}) => {
    await page.setViewportSize({width: 1280, height: 1000});
    await page.goto('/system/interfaces');
    await expect(page.locator('[data-process="rfd"]')).toBeVisible();
    const usbButton = page.locator('.ol-headrow', {has: page.locator('h2#modules')}).getByRole('button', {name: 'USB devices'});
    const h = (await page.locator('h2#modules').boundingBox())!;
    const b = (await usbButton.boundingBox())!;
    expect(b.y).toBeGreaterThanOrEqual(h.y + h.height);
    expect(Math.abs(b.x - h.x)).toBeLessThan(1);
    await usbButton.click();
    await expect(page.locator('table.ol-usb')).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.getAnimations().length)).toBe(0);
    const w = await page.evaluate(() => {
        const r = (s: string) => document.querySelector(s)!.getBoundingClientRect();
        return {usb: r('.ol-panelrow .ol-disclosure'), grid: r('.ol-panelrow'), card: r('.ol-cards-radio:not(.ol-panelrow) .ol-card')};
    });
    // task 249 (the maintainer: "use full width for usb-devices panel"): the page's content width
    expect(Math.round(w.usb.width)).toBe(Math.round(w.grid.width));
    expect(w.usb.width).toBeGreaterThan(w.card.width);
    // no cell wraps: every cell of the stub's rows is one line high
    const tall = await page.locator('table.ol-usb td').evaluateAll((tds) => tds.filter((td) => td.getBoundingClientRect().height > 2 * parseFloat(getComputedStyle(td).lineHeight || '20')).length);
    expect(tall).toBe(0);
    const wraps = await page.locator('table.ol-usb td, table.ol-usb th').evaluateAll((cells) => cells.filter((c) => getComputedStyle(c).whiteSpace !== 'nowrap').length);
    expect(wraps).toBe(0);
    // read-only: Close, not Cancel
    await expect(page.getByRole('group', {name: 'USB devices'}).getByRole('button', {name: 'Close'})).toBeVisible();
    expect(w.usb.top).toBeLessThan(w.card.top);
    await expect(page.locator('[data-usb-toggle]')).toBeHidden();
    await page.getByRole('group', {name: 'USB devices'}).getByRole('button', {name: 'Close'}).click();
    await expect(page.locator('table.ol-usb')).toHaveCount(0);
    await expect(usbButton).toBeVisible();
});

test('rename writes the list back with the name changed and the keys kept', async ({page}) => {
    let body: {class?: string; gateways?: {serial: string; name: string; key: string}[]} = {};
    await page.route('**/api/system/v1/radio/lan-gateways', async (route) => {
        if (route.request().method() !== 'PUT') return route.continue();
        body = route.request().postDataJSON();
        await route.fulfill({json: {gateways: [], service: 'rfd', restarted: true}});
    });
    await page.goto('/system/lan-devices');
    await card(page, 'NEQ1234567').getByRole('button', {name: 'Rename'}).click();
    const input = page.getByRole('dialog').getByRole('textbox');
    await expect(input).toHaveValue('Garage');
    await input.fill('Hof');
    await page.getByRole('dialog').getByRole('button', {name: 'Rename'}).click();
    await expect.poll(() => body.gateways?.length ?? 0).toBe(2);
    expect(body.class).toBe('rf');
    // the renamed one, the other untouched, and no key resent (an empty key keeps the file's)
    expect(body.gateways?.map((g) => [g.serial, g.name, g.key])).toEqual([
        ['NEQ1234567', 'Hof', ''],
        ['KEQ0987654', 'Dachboden', ''],
    ]);
    await expect(page.locator('.ol-notice')).toContainText('rfd restarted.');
});

test('the HmIP panel marks whether HAPs and DRAPs can route through the module', async ({page, baseURL}) => {
    await page.goto('/radio');
    const line = page.locator('[data-process="hmipserver"] .conn-can');
    await expect(line).toHaveAttribute('data-routing', 'yes');
    await expect(line).toContainText('HmIP-HAPs and DRAPs can route through this module.');
    await expect(line.locator('.conn-mark.ok svg')).toBeVisible();

    // a module that carries HmIP but cannot route for them: the red cross and the other sentence
    await page.context().addCookies([{name: 'stub-conn-basic', value: '1', url: baseURL!}]);
    await page.reload();
    await expect(line).toHaveAttribute('data-routing', 'no');
    await expect(line).toContainText('No routing through HmIP-HAPs or DRAPs');
    await expect(line.locator('.conn-mark.no svg')).toBeVisible();
});

test('on a phone the USB table scrolls inside its panel, not the page; German says Schließen', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.setViewportSize({width: 390, height: 900});
    await page.goto('/system/interfaces');
    await page.locator('.ol-headrow', {has: page.locator('h2#modules')}).getByRole('button', {name: 'USB-Geräte'}).click();
    await expect(page.locator('table.ol-usb')).toBeVisible();
    const pageScroll = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);
    expect(pageScroll).toBe(false);
    await expect(page.getByRole('group', {name: 'USB-Geräte'}).getByRole('button', {name: 'Schließen'})).toBeVisible();
});
