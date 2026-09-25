import {expect, test, type Locator} from '@playwright/test';

// The shell's own dialog (no browser confirm anywhere): the power menu's Reboot opens it, Cancel
// and Escape close it without a request.

// B-108: a tap on the phone, a click elsewhere. The clicks here were forced while the Services page
// was wider than a phone (B-105): the phone emulation zoomed it out, and a tap missed what it aimed at.
async function press(target: Locator) {
    if (test.info().project.use.isMobile) await target.tap();
    else await target.click();
}
test('reboot asks through the shell dialog and cancels cleanly', async ({page}) => {
    let posted = 0;
    await page.route('**/api/system/v1/reboot', (r) => {
        posted++;
        return r.fulfill({json: {ok: true}});
    });
    await page.goto('/');
    await page.locator('.ol-powerbtn').click();
    await page.locator('.ol-powerpop [data-action="reboot"]').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Reboot this system now?');
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    await expect(dialog).toBeHidden();
    expect(posted).toBe(0);

    await page.locator('.ol-powerbtn').click();
    await page.locator('.ol-powerpop [data-action="reboot"]').click();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();
    expect(posted).toBe(0);
});

test('no page calls window.confirm or window.prompt', async ({page}) => {
    // a native dialog would hang Playwright unless handled; make one fail the test instead
    page.on('dialog', async (d) => {
        await d.dismiss();
        throw new Error(`native ${d.type()} dialog: ${d.message()}`);
    });
    await page.goto('/system/services');
    // the live label, not the hidden sizers
    await press(page.locator('.ol-rowactions button:has(.ol-acttext:text-is("Stop"))').first());
    await expect(page.getByRole('dialog')).toBeVisible();
    await press(page.getByRole("dialog").getByRole("button", {name: "Cancel"}));
});
