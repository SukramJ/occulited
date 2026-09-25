import {expect, test} from '@playwright/test';

// openccu-lite task 218: the network radio board on the LAN devices page (task 239; the Interfaces page's until then) - find, use, save, the
// board another system uses (asked first), a board that does not answer, remove; German.

test('find a board, use it, save: connected, with what the board says', async ({page}) => {
    page.on('dialog', (d) => {
        throw new Error(`native dialog: ${d.message()}`);
    });
    await page.goto('/system/lan-devices');
    const sec = page.locator('.hb-panel');
    await expect(page.getByRole('heading', {name: 'Network radio board (HB-RF-ETH)'})).toBeVisible();
    await expect(sec.locator('[data-hb-none]')).toHaveText('No board is configured.');
    await page.locator('[data-hb-find]').click();
    const found = sec.locator('[data-hb-found] li');
    await expect(found).toHaveCount(2);
    await expect(found.first()).toContainText('HM-MOD-RPI-PCB · firmware 1.3.0 · free');
    await expect(found.nth(1)).toContainText('in use by 192.0.2.7');
    await found.first().getByRole('button', {name: 'Use this board'}).click();
    await expect(page.locator('#ol-hb-address')).toHaveValue('192.0.2.50');
    await sec.locator('[data-hb-save]').click();
    await expect(sec.locator('[data-hb-connected]')).toHaveText('Connected to the board at 192.0.2.50.');
    await expect(sec.locator('[data-hb-board]')).toContainText('1.3.0');
    // B-220: the module type and its serial with a space between them
    await expect(sec.locator('[data-hb-module]')).toHaveText('HM-MOD-RPI-PCB MEQ0835626');
    await expect(sec.locator('[data-hb-board-state]')).toHaveText('connected to this system');
    await expect(sec.getByRole('link', {name: "The board's own page (settings, firmware update)"})).toHaveAttribute('href', 'http://192.0.2.50/');
    // remove, asked first
    await sec.locator('[data-hb-remove]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Remove'}).click();
    await expect(sec.locator('[data-hb-none]')).toBeVisible();
});

test("a board another system uses is asked about; a bad address is said; one that does not answer is retried", async ({page}) => {
    await page.goto('/system/lan-devices');
    const sec = page.locator('.hb-panel');
    await page.locator('#ol-hb-address').fill('hb-rf-eth.local');
    await sec.locator('[data-hb-save]').click();
    await expect(sec.locator('[data-hb-error]')).toContainText('IPv4 only');
    await page.locator('#ol-hb-address').fill('192.0.2.99');
    await sec.locator('[data-hb-save]').click();
    const dlg = page.getByRole('dialog');
    await expect(dlg).toContainText('The board is connected to 192.0.2.7.');
    await dlg.getByRole('button', {name: 'Cancel'}).click();
    await expect(sec.locator('[data-hb-none]')).toBeVisible();
    await sec.locator('[data-hb-save]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Take the board'}).click();
    await expect(sec.locator('[data-hb-connected]')).toBeVisible();
    await page.locator('#ol-hb-address').fill('192.0.2.60');
    await sec.locator('[data-hb-save]').click();
    await expect(sec.locator('[data-hb-retrying]')).toContainText('The board at 192.0.2.60 is not connected. The system tries again every 30 seconds');
    // B-220: a space before what the board said
    await expect(sec.locator('[data-hb-retrying]')).toContainText('when it answers. The board: the board does not answer');
    // B-218: a board whose link is lost is the kernel's to reconnect - no "every 30 seconds"
    await page.locator('#ol-hb-address').fill('192.0.2.61');
    await sec.locator('[data-hb-save]').click();
    await expect(sec.locator('[data-hb-retrying]')).toHaveText('The connection to the board at 192.0.2.61 is lost. The system reconnects it on its own; the radio daemons keep running and have the module again when it is back.');
    // put the stub back
    await sec.locator('[data-hb-remove]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Remove'}).click();
});

test('German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/lan-devices');
    await expect(page.getByRole('heading', {name: 'Netzwerk-Funkplatine (HB-RF-ETH)'})).toBeVisible();
    await expect(page.locator('[data-hb-none]')).toHaveText('Keine Platine eingerichtet.');
    await expect(page.locator('[data-hb-find]')).toContainText('Platinen suchen');
});

// openccu-lite task 239 (the maintainer): "wouldnt it make sense to move the hb-rf-eth config to the
// lan-devices page? ... there is a line break between magnifier icon and search label". The section
// stands after the BidCoS gateways and before the access points; the Interfaces page links there; the
// old anchor and the Status warning lead there.
test('the board is a LAN device: its section on the LAN devices page, not on Interfaces', async ({page}) => {
    await page.goto('/system/lan-devices');
    await expect(page.locator('main h2')).toHaveText(['BidCoS Gateways', 'Network radio board (HB-RF-ETH)', 'HmIP Access Points', 'LAN devices']);
    await page.goto('/system/interfaces');
    await expect(page.locator('h2#hb-rf-eth')).toHaveCount(0);
    await expect(page.locator('[data-lan-link] a')).toContainText('the HB-RF-ETH');
});

test('the old anchor leads to the new page', async ({page, baseURL}) => {
    await page.goto('/system/interfaces#hb-rf-eth');
    await expect(page).toHaveURL(`${baseURL}/system/lan-devices#hb-rf-eth`);
    await expect(page.locator('h2#hb-rf-eth')).toBeVisible();
});

test('the Status warning links to the LAN devices page', async ({page}) => {
    await page.route('**/api/system/v1/warnings', (route) =>
        route.fulfill({json: {warnings: [{id: 'hb-rf-eth', variant: '192.0.2.209', severity: 'warning', href: '/system/lan-devices#hb-rf-eth', params: {address: '192.0.2.209'}}], periods: [1, 7, 90]}}),
    );
    await page.goto('/');
    const notice = page.locator('[data-warnings] [data-notice="hb-rf-eth"]');
    await notice.getByRole('link', {name: 'LAN devices'}).click();
    await expect(page).toHaveURL(/\/system\/lan-devices#hb-rf-eth$/);
});

for (const lang of ['en', 'de']) {
    test(`the Find button keeps its magnifier and its label on one line (${lang})`, async ({page}) => {
        await page.addInitScript((l) => localStorage.setItem('ol.language', l), lang);
        await page.goto('/system/lan-devices');
        for (const sel of ['[data-hb-find]', '[data-lan-search]']) {
            const b = page.locator(sel);
            await expect(b).toBeVisible();
            const box = (await b.boundingBox())!;
            const icon = (await b.locator('svg').boundingBox())!;
            expect(Math.abs(icon.y + icon.height / 2 - (box.y + box.height / 2)), `${sel}: the icon beside the label`).toBeLessThan(3);
            expect(box.height, `${sel}: one line high`).toBeLessThan(34);
        }
    });
}
