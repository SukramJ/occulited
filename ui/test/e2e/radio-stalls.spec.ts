import {expect, test, type Page} from '@playwright/test';

// openccu-lite B-201: an interface process held by a callback listener that takes its call and never
// answers. The Status page's warning names the listener and links to the Interfaces page (#stalls),
// where the held process stands above the registered clients, and an administrator ends the
// connection to the listener (POST /radio/subscribers/drop) through the shell's dialog.

const STALLS = '**/api/system/v1/radio/subscribers/stalls*';
const DROP = '**/api/system/v1/radio/subscribers/drop';

const held = {
    interface: 'HmIP-RF',
    kind: 'delivery',
    since: '2026-09-25T10:50:56Z',
    checked_at: '2026-09-25T10:57:00Z',
    listeners: [
        {address: '127.0.0.1:8199', connected: true, local: true, owner: 'addon-frozen', verdict: 'holds'},
        {address: '127.0.0.1:2048', id: 'nr_D24DjW_HmIP-RF', url: 'http://127.0.0.1:2048', connected: false, local: true, owner: 'addon-redmatic', verdict: 'answers'},
    ],
};

async function plantStall(page: Page, stalls: object[]) {
    await page.route(STALLS, (route) => route.fulfill({json: {stalls}}));
}

test('the held interface stands above the clients, named, with the way to end it', async ({page}) => {
    await plantStall(page, [held]);
    await page.goto('/system/interfaces#stalls');
    const notice = page.locator('#stalls [data-stall="HmIP-RF"]');
    await expect(notice).toContainText('HmIP-RF delivers no events to any client. The client at 127.0.0.1:8199, user addon-frozen takes its calls and never answers');
    // only the listener that holds it is listed, not the one that answers
    await expect(notice.locator('[data-listener]')).toHaveCount(1);
    await expect(notice.locator('[data-listener="127.0.0.1:8199"] .ol-stall-drop')).toHaveText('End connection');
    // it stands before the process cards
    const stall = (await notice.boundingBox())!;
    const cards = (await page.locator('.ol-subscribers').boundingBox())!;
    expect(stall.y).toBeLessThan(cards.y);
    const overflow = await page.locator('#stalls').evaluate((el) => el.scrollWidth > el.clientWidth + 1);
    expect(overflow).toBe(false);
});

test('ending the connection asks first, sends the interface and the address, and reads again', async ({page}) => {
    let stalls: object[] = [held];
    await page.route(STALLS, (route) => route.fulfill({json: {stalls}}));
    const sent: unknown[] = [];
    await page.route(DROP, async (route) => {
        sent.push(route.request().postDataJSON());
        stalls = [];
        await route.fulfill({json: {connections: 1, deregistered: 0, entries: 0}});
    });
    await page.goto('/system/interfaces');
    await page.locator('[data-listener="127.0.0.1:8199"] .ol-stall-drop').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('End the connection from HmIP-RF to 127.0.0.1:8199, user addon-frozen?');
    await expect(dialog).toContainText("It is not on HmIP-RF's list of clients: its registration never finished.");
    await dialog.getByRole('button', {name: 'End connection'}).click();
    await expect(dialog).toHaveCount(0);
    expect(sent).toEqual([{interface: 'HmIP-RF', address: '127.0.0.1:8199'}]);
    await expect(page.locator('#stalls')).toHaveCount(0);
});

test('an image whose helper may not do it says what does', async ({page}) => {
    await plantStall(page, [{...held, kind: 'calls', interface: 'BidCos-RF', listeners: [{...held.listeners[0], id: 'b201hang2', url: 'http://127.0.0.1:8199', verdict: 'no-answer'}]}]);
    await page.route(DROP, (route) => route.fulfill({status: 409, json: {error: 'drop-not-allowed', message: 'this system does not allow the helper to end another process\'s connection (its unit filters pidfd_getfd)'}}));
    await page.goto('/system/interfaces');
    const notice = page.locator('#stalls [data-stall="BidCos-RF"]');
    await expect(notice).toContainText('BidCos-RF answers no client any more. The client at b201hang2 (127.0.0.1:8199), user addon-frozen takes its calls');
    await notice.locator('.ol-stall-drop').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText("It is on BidCos-RF's list of clients, and is removed from it as well.");
    await dialog.getByRole('button', {name: 'End connection'}).click();
    await expect(dialog).toContainText("This system's image does not let it end another process's connection yet.");
});

test('the Status page names the listener and links to the Interfaces page', async ({page}) => {
    await page.route('**/api/system/v1/warnings', (route) =>
        route.fulfill({json: {warnings: [{id: 'rpc-stalled', variant: 'HmIP-RF', severity: 'error', href: '/system/interfaces#stalls', params: {interface: 'HmIP-RF', kind: 'delivery', since: held.since, checked: true, listeners: [held.listeners[0]]}}], periods: [1, 7, 90]}}),
    );
    await plantStall(page, [held]);
    await page.goto('/');
    const notice = page.locator('[data-warnings] [data-notice="rpc-stalled"]');
    await expect(notice).toContainText('HmIP-RF delivers no events to any client. The client at 127.0.0.1:8199, user addon-frozen takes its calls and never answers');
    await notice.getByRole('link', {name: 'Interfaces'}).click();
    await expect(page).toHaveURL(/\/system\/interfaces#stalls$/);
    await expect(page.locator('#stalls [data-stall="HmIP-RF"]')).toBeVisible();
});

test('German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await plantStall(page, [held]);
    await page.goto('/system/interfaces');
    await expect(page.locator('#stalls [data-stall="HmIP-RF"]')).toContainText('HmIP-RF liefert keinem Client mehr Ereignisse. Der Client unter 127.0.0.1:8199, Benutzer addon-frozen nimmt die Aufrufe an');
    await expect(page.locator('.ol-stall-drop')).toHaveText('Verbindung beenden');
});

// openccu-lite task 234: a listener hmipserver's log names - its registration finished, it took an
// event and does not answer; hmipserver holds its events (only its) and logs that every second.
const blockedStall = {
    interface: 'HmIP-RF',
    kind: 'listener',
    since: '2026-09-25T13:26:46Z',
    checked_at: '2026-09-25T13:29:48Z',
    listeners: [
        {address: '127.0.0.1:8234', id: 't234hang', url: 'http://127.0.0.1:8234', connected: true, local: true, owner: 'addon-frozen', verdict: 'blocked', blocked_for: 181},
        {address: '', id: 'gone', connected: false, local: false, verdict: 'blocked', blocked_for: 70},
    ],
};

test('a listener that does not take its events: named, how long, and ended through the dialog', async ({page}) => {
    let stalls: object[] = [blockedStall];
    await page.route(STALLS, (route) => route.fulfill({json: {stalls}}));
    const sent: unknown[] = [];
    await page.route(DROP, async (route) => {
        sent.push(route.request().postDataJSON());
        stalls = [];
        await route.fulfill({json: {connections: 1, deregistered: 1, entries: 1}});
    });
    await page.goto('/system/interfaces#stalls');
    const notice = page.locator('#stalls [data-stall="HmIP-RF"]');
    await expect(notice).toContainText('HmIP-RF: the clients t234hang (127.0.0.1:8234), user addon-frozen; gone do not take their events.');
    await expect(notice).toContainText('A client that is no longer on the list of clients cannot be ended here');
    await expect(notice.locator('[data-listener="127.0.0.1:8234"] [data-held]')).toHaveText('Held for 3 min');
    // the one without an address has no button: there is no connection to name
    await expect(notice.locator('[data-listener="gone"] .ol-stall-drop')).toHaveCount(0);
    await notice.locator('[data-listener="127.0.0.1:8234"] .ol-stall-drop').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText("HmIP-RF keeps this client's events back and writes a warning to its log every second until the connection ends.");
    await dialog.getByRole('button', {name: 'End connection'}).click();
    await expect(dialog).toHaveCount(0);
    expect(sent).toEqual([{interface: 'HmIP-RF', address: '127.0.0.1:8234'}]);
    await expect(page.locator('#stalls')).toHaveCount(0);
});

test('a listener that does not take its events, on the Status page and in German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.route('**/api/system/v1/warnings', (route) =>
        route.fulfill({json: {warnings: [{id: 'rpc-stalled', variant: 'HmIP-RF', severity: 'error', href: '/system/interfaces#stalls', params: {interface: 'HmIP-RF', kind: 'listener', since: blockedStall.since, checked: true, listeners: [blockedStall.listeners[0]]}}], periods: [1, 7, 90]}}),
    );
    await plantStall(page, [blockedStall]);
    await page.goto('/');
    await expect(page.locator('[data-warnings] [data-notice="rpc-stalled"]')).toContainText('HmIP-RF: Der Client t234hang (127.0.0.1:8234), Benutzer addon-frozen nimmt seine Ereignisse nicht an.');
    await page.goto('/system/interfaces');
    await expect(page.locator('#stalls [data-listener="127.0.0.1:8234"] [data-held]')).toHaveText('Seit 3 min blockiert');
});
