import {expect, test} from '@playwright/test';

// Task 188: Ctrl/⌘+K opens the System menu from anywhere in the shell. Chromium and Firefox bind
// Ctrl+K to a search field of their own, so the binding had to be checked in more than one engine:
// this spec runs in the default projects and, with PW_ENGINES=firefox,webkit, in those two as well
// (playwright.config.ts's testMatch for them names this file beside phone-width.spec.ts).

const menu = 'nav.ol-sysmenu, .ol-sysmenu';
const field = {name: 'Filter the System menu'};

for (const keys of ['Control+k', 'Meta+k']) {
    test(`${keys} opens the System menu with the filter focused`, async ({page}) => {
        await page.goto('/');
        await expect(page.getByRole('button', {name: /System/})).toBeVisible();
        await page.keyboard.press(keys);
        const panel = page.locator(menu);
        await expect(panel).toBeVisible();
        await expect.poll(() => panel.getAttribute('data-phase')).toBe('open');
        // the key reached the page and not the browser's own search field: our handler took it and
        // put the caret where the reader can type at once
        await expect(page.getByRole('textbox', field)).toBeFocused();
        await page.keyboard.type('fire');
        await expect(page.getByRole('textbox', field)).toHaveValue('fire');
        await expect(panel.getByRole('menuitem')).toHaveText(['Firewall']);
        await page.keyboard.press('Escape'); // clears the field
        await page.keyboard.press('Escape'); // closes the menu
        await expect(page.locator(menu)).toHaveCount(0);
    });
}

// with the menu open the same keys put the focus back into the filter rather than closing it
test('the shortcut pressed again focuses the filter of the open menu', async ({page}) => {
    await page.goto('/');
    await page.keyboard.press('Control+k');
    const panel = page.locator(menu);
    await expect(panel).toBeVisible();
    await expect.poll(() => panel.getAttribute('data-phase')).toBe('open');
    await page.keyboard.press('ArrowDown');
    await expect(panel.getByRole('menuitem').first()).toBeFocused();
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('textbox', field)).toBeFocused();
    await expect(panel).toBeVisible();
});
