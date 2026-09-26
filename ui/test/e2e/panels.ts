import {expect, type Locator} from '@playwright/test';

/*
 * openccu-lite task 268 (the maintainer: "buttons that open panels in the page should transform to
 * the panel itself (so the button is not visible anymore when a panel gets opened)"): the button
 * that opens an in-page panel is hidden while the panel is open and back once it is closed. One
 * check for every such button, used by each page's spec.
 */
export async function buttonBecomesPanel(button: Locator, panel: Locator, close: 'Cancel' | 'Close' = 'Cancel'): Promise<void> {
    await expect(button).toBeVisible();
    await button.click();
    await expect(panel).toBeVisible();
    await expect(button).toBeHidden();
    await panel.getByRole('button', {name: close, exact: true}).first().click();
    await expect(panel).toHaveCount(0);
    await expect(button).toBeVisible();
}
