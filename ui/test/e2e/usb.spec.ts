import {test, expect} from '@playwright/test';

// task 42: the USB list on the Interfaces page - collapsed, the hubs folded away, the RFUSB row
// with its id, name, port, driver, the raw-uart it produced and the protocols it serves
test('the USB section lists the stick with its radio join and folds the hubs away', async ({page}) => {
    await page.goto('/radio');
    await expect(page.locator('h2', {hasText: 'Modules'})).toBeVisible();
    const table = page.locator('.ol-usb');
    await expect(table).toBeHidden();
    await page.getByRole('button', {name: 'USB devices'}).click();
    await expect(table).toBeVisible();
    const rows = table.locator('tbody tr');
    await expect(rows).toHaveCount(1);
    const stick = rows.first();
    await expect(stick).toContainText('eQ-3 HmIP-RFUSB');
    await expect(stick).toContainText('1b1f:c020');
    await expect(stick).toContainText('2-1');
    await expect(stick).toContainText('hb_rf_usb_2');
    await expect(stick).toContainText('/dev/raw-uart');
    await expect(stick).toContainText('BidCos-RF, HmIP-RF');
    // the hubs on request, nested: the stick under its root hub
    await page.getByLabel('Show hubs').check();
    await expect(rows).toHaveCount(4);
    await expect(rows.nth(0)).toContainText('UHCI Host Controller');
    await expect(rows.nth(2)).toContainText('eQ-3 HmIP-RFUSB');
    await expect(rows.nth(2)).toHaveClass(/child/);
    // the link to the module card above
    await rows.nth(2).getByRole('button', {name: 'Show module'}).click();
    await expect(page.locator('#ol-module-BidCos-RF')).toBeInViewport();
});
