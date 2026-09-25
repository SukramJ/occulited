import {expect, test, type Page} from '@playwright/test';

// task 139: the Addons popup is the System menu's panel (lib/MenuPanel.svelte): the morph out of
// the button (task 210: closed at once, no motion back into it), the backdrop and the sheet on a phone, the filter above 8 rows,
// the focus back on the button on close, and the same surface as the System panel
const menu = (page: Page) => page.locator('.ol-menu').first();
const button = (page: Page) => menu(page).locator('.ol-menubtn');
const panel = (page: Page) => page.locator('nav.ol-addonpanel');
const popup = (page: Page) => menu(page).getByRole('menu');

test('the popup opens with the morph and closes at once; Escape gives the focus back', async ({page, isMobile}) => {
    test.skip(!!isMobile, 'the desktop popover');
    await page.goto('/');
    await button(page).click();
    await expect(panel(page)).toHaveAttribute('data-phase', /opening|open/);
    await expect(panel(page)).toHaveAttribute('data-phase', 'open');
    await expect(popup(page).locator('.ol-addonname').first()).toBeVisible();
    // under the button, inside the window
    const b = (await button(page).boundingBox())!;
    const p = (await panel(page).boundingBox())!;
    expect(p.y).toBeGreaterThanOrEqual(b.y + b.height);
    expect(p.x + p.width).toBeLessThanOrEqual(page.viewportSize()!.width);
    await page.keyboard.press('Escape');
    await expect(panel(page)).toHaveCount(0);
    await expect(button(page)).toBeFocused();
    // a click outside closes too
    await button(page).click();
    await expect(panel(page)).toHaveAttribute('data-phase', 'open');
    await page.mouse.click(10, page.viewportSize()!.height - 10);
    await expect(panel(page)).toHaveCount(0);
});

test('under reduced motion the popup is simply there', async ({page, isMobile}) => {
    test.skip(!!isMobile, 'the desktop popover');
    await page.emulateMedia({reducedMotion: 'reduce'});
    await page.goto('/');
    await button(page).click();
    await expect(panel(page)).toHaveAttribute('data-phase', 'open');
    await page.keyboard.press('Escape');
    await expect(panel(page)).toHaveCount(0);
});

test('on a phone it is a sheet with a backdrop, and a drag on the handle dismisses it', async ({page, isMobile}) => {
    test.skip(!isMobile, 'the phone project');
    await page.goto('/');
    await button(page).click();
    const sheet = page.locator('nav.ol-addonpanel.ol-syssheet');
    await expect(sheet).toHaveAttribute('data-phase', 'open');
    await expect(page.locator('.ol-sysbackdrop')).toBeVisible();
    await expect(sheet.locator('.ol-syshandle')).toBeVisible();
    const box = (await sheet.boundingBox())!;
    expect(box.y + box.height).toBeGreaterThanOrEqual(page.viewportSize()!.height - 1);
    const handle = (await sheet.locator('.ol-syshandle-row').boundingBox())!;
    await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
    await page.mouse.down();
    await page.mouse.move(handle.x + handle.width / 2, handle.y + 200, {steps: 8});
    await page.mouse.up();
    await expect(sheet).toHaveCount(0);
});

test('the filter appears at nine rows, not at eight, narrows the list and suspends Alt+↑/↓', async ({page, baseURL, isMobile}) => {
    test.skip(!!isMobile, 'the desktop popover');
    // the stub: five addons and the CCU WebUI link = 6; the NEO Server 7, a nav.d page 8, ioBroker 9
    await page.context().addCookies([{name: 'stub-addon-off', value: '1', url: baseURL!}, {name: 'stub-nav-page', value: '1', url: baseURL!}]);
    await page.goto('/');
    await button(page).click();
    await expect(popup(page).locator('.ol-addonname')).toHaveCount(8);
    await expect(panel(page).getByRole('textbox')).toHaveCount(0);
    await page.keyboard.press('Escape');
    await page.context().addCookies([{name: 'stub-addon-more', value: '1', url: baseURL!}]);
    await page.reload();
    await button(page).focus();
    await page.keyboard.press('Enter');
    await expect(popup(page).locator('.ol-addonname')).toHaveCount(9);
    const filter = panel(page).getByRole('textbox', {name: 'Filter the Addons menu'});
    await expect(filter).toBeFocused();
    await filter.fill('mat');
    await expect(popup(page).locator('.ol-addonname')).toHaveText(['Homematic-Manager', 'RedMatic']);
    // the order cannot be changed on a filtered list: no handles (task 162), and Alt+↑ moves nothing
    await expect(popup(page).locator('button.ol-grip')).toHaveCount(0);
    await popup(page).getByRole('menuitem', {name: 'RedMatic'}).focus();
    await page.keyboard.press('Alt+ArrowUp');
    await expect(popup(page).locator('.ol-addonname')).toHaveText(['Homematic-Manager', 'RedMatic']);
    await filter.fill('zzz');
    await expect(popup(page).getByRole('note')).toHaveText('Nothing found');
    // Enter on the filter opens the first row
    await filter.fill('Red');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/nav\/red$/);
});

test('the two panels share their surface: background, border and radius agree; the Addons rows are the compact ones', async ({page, isMobile}) => {
    test.skip(!!isMobile, 'the desktop popover');
    const look = (sel: string) => page.locator(sel).evaluate((el) => {
        const cs = getComputedStyle(el);
        return {bg: cs.backgroundColor, border: cs.borderTopColor, radius: cs.borderTopLeftRadius, shadow: cs.boxShadow, padding: cs.paddingTop};
    });
    await page.goto('/');
    await button(page).click();
    await expect(panel(page)).toHaveAttribute('data-phase', 'open');
    const addons = await look('nav.ol-addonpanel');
    const row = (await popup(page).locator('.ol-menurow').first().boundingBox())!;
    await page.keyboard.press('Escape');
    await page.locator('.ol-systab').click();
    await expect(page.locator('nav.ol-sysmenu:not(.ol-addonpanel)')).toHaveAttribute('data-phase', 'open');
    const system = await look('nav.ol-sysmenu:not(.ol-addonpanel)');
    const sysRow = (await page.locator('.ol-sysrow').first().boundingBox())!;
    expect(addons).toEqual(system);
    // task 210: the Addons rows are compact (36 px) beside the System menu's 44 px ones
    expect(Math.round(row.height)).toBe(36);
    expect(Math.round(sysRow.height)).toBe(44);
});
