import {test, expect} from '@playwright/test';

// task 41: the Radio firmware section - the module with its verdict and files, the shell's own
// dialog before a flash (naming the direction), the phase log while it runs
test('the firmware section lists the files and a flash asks with the direction named', async ({page}) => {
    const posts: string[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().includes('/radio/firmware/flash')) posts.push(r.postData() ?? '');
    });
    await page.goto('/system/updates');
    await expect(page.locator('h2', {hasText: 'Radio firmware'})).toBeVisible();
    const card = page.locator('.fw-module').first();
    await expect(card).toContainText('HMIP-RFUSB');
    await expect(card.locator('.fw-running')).toHaveText('4.4.18');
    await expect(card.locator('.fw-verdict')).toHaveText('a newer firmware is on the system');
    const rows = card.locator('tbody tr');
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(0)).toContainText('shipped');
    await expect(rows.nth(0)).toContainText('same version');
    await expect(rows.nth(0).getByRole('button', {name: 'Delete'})).toHaveCount(0);
    await expect(rows.nth(1)).toContainText('upgrade');
    await expect(rows.nth(2)).toContainText('downgrade');
    // the downgrade row says so, first, and its button is not "Flash"
    await rows.nth(2).getByRole('button', {name: 'Downgrade'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText(/^Flash coprocessor firmware\s*Downgrade the coprocessor of HMIP-RFUSB from 4.4.18 to 4.2.14\?/);
    await expect(dialog).toContainText('must not lose power');
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    await expect(dialog).toBeHidden();
    expect(posts).toHaveLength(0);
    // the upgrade: confirmed, one POST with module and file, the phase log appears
    await rows.nth(1).getByRole('button', {name: 'Flash'}).click();
    await expect(dialog).toContainText('Update the coprocessor of HMIP-RFUSB from 4.4.18 to 4.4.22?');
    await dialog.getByRole('button', {name: 'Flash'}).click();
    await expect.poll(() => posts.length).toBe(1);
    expect(JSON.parse(posts[0]!)).toEqual({module: 'HMIP-RFUSB', file: 'dualcopro_update_blhmip-4.4.22.eq3'});
    await expect(page.locator('h3', {hasText: 'Flash running'})).toBeVisible();
    const log = page.locator('.fw-log');
    await expect(log).toContainText('hmipserver stopped');
    await expect(log).toContainText('coprocessor reports 4.4.18');
    await expect(log).toContainText('flashing dualcopro_update_blhmip-4.4.22.eq3');
    // no browser dialog anywhere in the flow: a window.confirm would have hung the test
});

test('an upload goes through the form and the list reloads', async ({page}) => {
    await page.goto('/system/updates');
    await expect(page.locator('h2', {hasText: 'Radio firmware'})).toBeVisible();
    // the maintainer, 2026-09-20: each module panel has its own upload
    await page.locator('.fw-module').first().locator('input[type=file]').setInputFiles({name: 'dualcopro_update_blhmip-4.4.23.eq3', mimeType: 'application/octet-stream', buffer: Buffer.from('firmware bytes')});
    await page.locator('.fw-module').first().locator('.fw-upload').getByRole('button', {name: 'Upload', exact: true}).click();
    await expect(page.locator('.ol-notice', {hasText: 'Uploaded: dualcopro_update_blhmip-4.4.23.eq3'})).toBeVisible();
});

// task 147 (D-100): the HM-CFG-USB-2 in the firmware section - its card says the image ships no
// file and where the 0.967 comes from; the upload takes an .enc
test('the HM-CFG-USB-2 is listed with where its firmware comes from', async ({page, baseURL}) => {
    // the real-box variant carries the adapter beside the RFUSB and the legacy module
    await page.context().addCookies([{name: 'stub-real', value: '1', url: baseURL!}]);
    await page.goto('/system/updates');
    const card = page.locator('.fw-module', {hasText: 'HM-CFG-USB-2'});
    await expect(card).toContainText('usb:JEQ0534849');
    await expect(card.locator('.fw-running')).toHaveText('0.956');
    await expect(card.locator('.fw-hmcfgusb-source')).toContainText('The image does not ship this firmware. Upload hmusbif.03c7.enc (0.967)');
    await expect(card.locator('.fw-hmcfgusb-source a')).toHaveAttribute('href', 'https://git.zerfleddert.de/hmcfgusb/firmware/');
    // the module's own upload, in its panel
    await expect(card.locator('input[type=file]')).toHaveAttribute('accept', '.eq3,.enc');
    await expect(card.locator('.fw-upload')).toHaveCount(1);
    await expect(page.locator('.fw-upload select')).toHaveCount(0);
});
