import {expect, test} from '@playwright/test';

// Task 75: the service messages on the Status page - the box's own, without ReGa - and what the
// RPC process adds to the Interfaces page: the system's own subscriber marked and without a
// removal, and when each interface process last spoke.

test('the Status page lists the service messages with the device name and since when', async ({page}) => {
    await page.goto('/');
    const h = page.locator('h2#service-messages');
    await expect(h).toContainText('Service messages');
    await expect(h.locator('.sm-count')).toHaveAttribute('data-count', '2');
    const card = page.locator('[data-service-messages="2"]');
    const wrc2 = card.locator('[data-message="000193C9951175:0:LOW_BAT"]');
    // the name from the metadata store, the room, the message in words, and "at least since" for
    // one that was already active when the system looked
    await expect(wrc2.locator('.sm-device')).toContainText('Wandtaster Flur');
    await expect(wrc2.locator('.sm-device')).toContainText('flur');
    await expect(wrc2.locator('.sm-text')).toHaveText('Low battery');
    await expect(wrc2.locator('.sm-meta')).toContainText('at least since');
    // a device without an object: the CCU's default name, and "since" for one seen happening
    const tc = card.locator('[data-message="JEQ0230153:0:UNREACH"]');
    await expect(tc.locator('.sm-device')).toHaveText('HM-CC-TC JEQ0230153');
    await expect(tc.locator('.sm-text')).toHaveText('Communication disturbed');
    await expect(tc.locator('.sm-meta')).toContainText('since');
    await expect(tc.locator('.sm-meta')).not.toContainText('at least');
    // read-only: no button in the list
    await expect(card.getByRole('button')).toHaveCount(0);
});

test('without messages the section says so, in German too', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-servicemsg', value: 'none', url: baseURL!}]);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/');
    await expect(page.locator('h2#service-messages')).toContainText('Servicemeldungen');
    await expect(page.locator('[data-service-messages="none"]')).toHaveText('Keine Servicemeldungen.');
});

test('the Interfaces page marks the system\'s own subscriber and says when each process last spoke', async ({page}) => {
    await page.goto('/radio');
    const hmip = page.locator('.ol-process[data-interface="HmIP-RF"]');
    const own = hmip.locator('.ol-sub', {hasText: 'occulited_HmIP-RF'});
    await expect(own.locator('.ol-sub-meta')).toContainText('this system (occulited)');
    await expect(own.locator('.ol-badge')).toHaveText('own');
    // no removal for the system's own registration; the others keep theirs
    await expect(own.getByRole('button', {name: 'Remove subscription'})).toHaveCount(0);
    await expect(hmip.locator('.ol-sub', {hasText: 'nr_D24DjW_HmIP-RF'}).getByRole('button', {name: 'Remove subscription'})).toHaveCount(1);
    // the liveness from the RPC process
    await expect(page.locator('[data-last-event="BidCos-RF"]')).toContainText('last event');
    await expect(page.locator('[data-last-event="VirtualDevices"]')).toHaveCount(0); // never spoke
});
