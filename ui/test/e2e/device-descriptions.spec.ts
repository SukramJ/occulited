import {expect, test} from '@playwright/test';

// D-66: no root addon may remount the system partition. The Interfaces page shows rfd's device
// descriptions - the image's and what addons added in the writable layer - with the reset to the
// image; the Services and Addons pages mark an addon whose remount was refused in this boot.

test('the device descriptions section: the counts, the layer, the reset with its question', async ({page}) => {
    await page.goto('/radio');
    const head = page.locator('.ol-headrow', {has: page.locator('h2#device-descriptions')});
    await expect(head.locator('h2')).toContainText('Device descriptions');
    const dl = page.locator('dl.ol-dd');
    await expect(dl).toHaveAttribute('data-dd-mode', 'overlay');
    await expect(dl.locator('dt')).toHaveText(['Source', 'Writable through', 'Changed image files']);
    await expect(dl.locator('dd').nth(0)).toHaveText('129 from the image, 87 added by addons');
    await expect(dl.locator('dd').nth(1)).toHaveText('an overlay on the userfs');
    await expect(dl.locator('dd').nth(2)).toContainText('1 replaced, 0 removed by addons');

    // the reset asks first and says what it did; the stub's answer has nothing replaced any more
    await head.getByRole('button', {name: 'Reset to the image'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('rfd is restarted');
    await dialog.getByRole('button', {name: 'Reset'}).click();
    await expect(page.locator('.ol-notice', {hasText: 'Device descriptions reset; rfd restarted.'})).toBeVisible();
    await expect(dl.locator('dt')).toHaveText(['Source', 'Writable through']);
});

test('a refused remount is a hint beside the addon on the Services and Addons pages', async ({page}) => {
    await page.goto('/services');
    const row = page.locator('tr', {has: page.locator('td', {hasText: 'addon-redmatic'})}).first();
    await expect(row.getByText('remount refused')).toBeVisible();
    // the other addons carry no such mark
    await expect(page.getByText('remount refused')).toHaveCount(1);

    await page.goto('/addons');
    await expect(page.getByText('remount refused')).toHaveCount(1);
});
