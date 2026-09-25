import {expect, test, type Page} from '@playwright/test';

// Task 59: the Addons control is a plain tab; the dropdown has two groups - the addons with a web
// frontend, which the user can pin to the tab bar (after Status, before Addons: task 131) and order by drag &
// drop or keyboard, and the rest; pins and order are stored per account on the box (D-54), which
// the stub keeps per browser under the cookie stub-prefs=<id>. stub-addon-more=1 adds a third
// frontend addon, ioBroker; stub-addon-off=1 the switched-off NEO Server.
const menu = (page: Page) => page.locator('.ol-menu').first();
const button = (page: Page) => menu(page).locator('.ol-menubtn');
const popup = (page: Page) => menu(page).getByRole('menu');
const row = (page: Page, name: string) => popup(page).locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const pin = (page: Page, name: string) => row(page, name).locator('.ol-pin');
const grip = (page: Page, name: string) => row(page, name).locator('.ol-grip');
const front = (page: Page) => popup(page).locator('.ol-menurow-front .ol-addonname');
/** the tabs of the bar in order as a reader sees them: Status, the pinned tabs that are shown, the Addons and System buttons (the nav.d entry is in the dropdown) */
const bar = (page: Page) =>
    page.evaluate(() =>
        Array.from(document.querySelectorAll<HTMLElement>('nav.ol-nav > a:not(.ol-pintab-folded), nav.ol-nav .ol-menubtn')).map((el) => (el.querySelector('.ol-pintab-name') ?? el.querySelector('.ol-menubtn-text') ?? el).textContent?.trim() ?? ''),
    );
const shownTabs = (page: Page) => page.locator('nav.ol-nav > a.ol-pintab:not(.ol-pintab-folded)');
/** their names, without the favicon a frontend may show before it */
const shownNames = (page: Page) => shownTabs(page).locator('.ol-pintab-name');

let n = 0;
async function prepare(page: Page, baseURL: string, extra: {more?: boolean; off?: boolean} = {}) {
    const cookies = [{name: 'stub-prefs', value: `pins-${process.pid}-${Date.now()}-${n++}`, url: baseURL}];
    if (extra.more) cookies.push({name: 'stub-addon-more', value: '1', url: baseURL});
    if (extra.off) cookies.push({name: 'stub-addon-off', value: '1', url: baseURL});
    await page.context().addCookies(cookies);
}
// wide enough for three pinned tabs beside the fixed ones (the fit is tested on its own below)
const WIDE = {width: 1600, height: 800};

// task 248 (the maintainer): the word, then System's reserved dot and caret
test('the button is the word Addons with the dot\'s place and a caret, and as wide whatever page is open', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    await page.goto('/');
    await expect(button(page).locator('.ol-menubtn-text')).toHaveText('Addons');
    await expect(button(page).locator('.ol-caret')).toHaveCount(1);
    await expect(button(page).locator('.ol-sysdot')).toHaveCount(1);
    await expect(button(page).locator('.ol-menubtn-sizer')).toHaveCount(0);
    const width = (await button(page).boundingBox())!.width;
    for (const path of ['/nav/red', '/addon-settings/mosquitto', '/addons', '/catalog', '/']) {
        await page.goto(path);
        await expect(button(page).locator('.ol-menubtn-text')).toHaveText('Addons');
        expect(Math.abs((await button(page).boundingBox())!.width - width), path).toBeLessThan(0.5);
    }
});

test('three groups with a divider between each: the frontends, the rest by name, the nav.d links', async ({page, baseURL}) => {
    await prepare(page, baseURL!, {off: true});
    await page.goto('/');
    await button(page).click();
    const groups = popup(page).getByRole('group');
    await expect(groups).toHaveCount(3);
    await expect(groups.nth(0)).toHaveAttribute('aria-label', 'Addons with a web interface of their own');
    await expect(groups.nth(0).locator('.ol-addonname')).toHaveText(['Homematic-Manager', 'RedMatic']);
    await expect(groups.nth(1)).toHaveAttribute('aria-label', 'Addons without a web interface');
    await expect(groups.nth(1).locator('.ol-addonname')).toHaveText(['hm2mqtt', 'JP HB Devices', 'Mosquitto', 'NEO Server']);
    // the maintainer's follow-up to task 131: the nav.d entry that is no addon, in a group of its own
    await expect(groups.nth(2)).toHaveAttribute('aria-label', 'Links set up on this system');
    await expect(groups.nth(2).locator('.ol-addonname')).toHaveText(['CCU WebUI']);
    await expect(groups.nth(2).locator('button.ol-grip, button.ol-pin, a[aria-label^="Settings for"]')).toHaveCount(0);
    // the dividers between the three, and the footer under its own
    await expect(popup(page).locator('.ol-menusep')).toHaveCount(3);
    // the second group has no handle and no pin: nothing to move, nothing to pin
    await expect(groups.nth(1).locator('button.ol-grip')).toHaveCount(0);
    await expect(groups.nth(1).locator('button.ol-pin')).toHaveCount(0);
    await expect(groups.nth(0).locator('button.ol-grip')).toHaveCount(2);
    await expect(groups.nth(0).locator('button.ol-pin')).toHaveCount(2);
});

test('a pin puts the addon into the tab bar between Status and Addons; unpinning takes it out; the menu stays open', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    await page.setViewportSize(WIDE);
    await page.goto('/');
    await expect.poll(() => bar(page)).toEqual(['Status', 'Control', 'Addons', 'System']);
    await button(page).click();
    const red = pin(page, 'RedMatic');
    await expect(red).toHaveAttribute('aria-pressed', 'false');
    await expect(red).toHaveAttribute('aria-label', 'Pin RedMatic to the tab bar');
    await red.click();
    await expect(popup(page)).toBeVisible();
    await expect(red).toHaveAttribute('aria-pressed', 'true');
    await expect(red).toHaveAttribute('aria-label', 'Unpin RedMatic');
    await expect(red).toHaveClass(/ol-pinned/);
    await expect.poll(() => bar(page)).toEqual(['Status', 'Control', 'RedMatic', 'Addons', 'System']);
    await pin(page, 'Homematic-Manager').click();
    await expect.poll(() => bar(page)).toEqual(['Status', 'Control', 'Homematic-Manager', 'RedMatic', 'Addons', 'System']);
    // the pinned tab: the addon's frontend in the shell, the tab active there and the button not
    await page.keyboard.press('Escape');
    await shownTabs(page).filter({hasText: 'RedMatic'}).click();
    await expect(page).toHaveURL(/\/nav\/red$/);
    await expect(page.locator('iframe.ol-kept-frame[src^="/addons/red/"]')).toBeVisible();
    await expect(shownTabs(page).filter({hasText: 'RedMatic'})).toHaveClass(/active/);
    await expect(button(page)).not.toHaveClass(/active/);
    // the tab shows the frontend's favicon (Node-RED's) at text size; Homematic-Manager has none:
    // the name alone, no letter badge (D-84; the dropdown keeps the letter)
    await expect(shownTabs(page).filter({hasText: 'RedMatic'}).locator('img')).toHaveAttribute('src', '/addons/red/favicon.ico');
    await expect(shownTabs(page).filter({hasText: 'Homematic-Manager'}).locator('.ol-addonmono')).toHaveCount(0);
    await expect(shownTabs(page).filter({hasText: 'Homematic-Manager'}).locator('img')).toHaveCount(0);
    await expect(shownTabs(page).filter({hasText: 'Homematic-Manager'}).locator('.ol-pintab-name')).toHaveText('Homematic-Manager');
    // unpinned: the tab goes at once, the addon stays in the dropdown, the button is active again
    await button(page).click();
    await expect(row(page, 'Homematic-Manager').locator('.ol-addonmono')).toHaveText('H'); // the dropdown keeps the letter
    await pin(page, 'RedMatic').click();
    await expect.poll(() => bar(page)).toEqual(['Status', 'Control', 'Homematic-Manager', 'Addons', 'System']);
    await expect(row(page, 'RedMatic')).toHaveClass(/active/);
    await expect(button(page)).toHaveClass(/active/);
});

test('dragging a row by its handle reorders the rows and the tabs; the name and the buttons do not start a drag', async ({page, baseURL, isMobile}) => {
    await prepare(page, baseURL!, {more: true});
    await page.setViewportSize(WIDE);
    await page.goto('/');
    await button(page).click();
    await expect(front(page)).toHaveText(['Homematic-Manager', 'ioBroker', 'RedMatic']);
    for (const name of ['Homematic-Manager', 'ioBroker', 'RedMatic']) await pin(page, name).click();
    await expect(shownNames(page)).toHaveText(['Homematic-Manager', 'ioBroker', 'RedMatic']);

    // RedMatic's handle, dragged above Homematic-Manager
    const from = (await grip(page, 'RedMatic').boundingBox())!;
    const to = (await row(page, 'Homematic-Manager').boundingBox())!;
    const x = from.x + from.width / 2;
    const y0 = from.y + from.height / 2;
    const y1 = to.y + 4;
    if (isMobile) {
        // a finger: touch events through the devtools protocol, which the browser turns into
        // pointer events of type touch - the same handler as the mouse
        const cdp = await page.context().newCDPSession(page);
        await cdp.send('Input.dispatchTouchEvent', {type: 'touchStart', touchPoints: [{x, y: y0}]});
        for (let i = 1; i <= 6; i++) await cdp.send('Input.dispatchTouchEvent', {type: 'touchMove', touchPoints: [{x, y: y0 + ((y1 - y0) * i) / 6}]});
        await cdp.send('Input.dispatchTouchEvent', {type: 'touchEnd', touchPoints: []});
    } else {
        await page.mouse.move(x, y0);
        await page.mouse.down();
        await page.mouse.move(x, y0 - 4);
        await page.mouse.move(x, y1, {steps: 6});
        // the drop line stands above the row the dragged one would push down, and the popup stays open
        await expect(row(page, 'Homematic-Manager')).toHaveClass(/ol-drop-before/);
        await expect(row(page, 'RedMatic')).toHaveClass(/ol-dragging/);
        await page.mouse.up();
    }
    await expect(front(page)).toHaveText(['RedMatic', 'Homematic-Manager', 'ioBroker']);
    await expect(shownNames(page)).toHaveText(['RedMatic', 'Homematic-Manager', 'ioBroker']);
    await expect(popup(page)).toBeVisible();
    await expect(row(page, 'RedMatic')).not.toHaveClass(/ol-dragging/);

    // a press on the name is a click on the name, not a drag: the frontend opens and the order stays
    if (!isMobile) {
        const name = (await row(page, 'ioBroker').locator('.ol-menuitem').boundingBox())!;
        await page.mouse.move(name.x + 40, name.y + name.height / 2);
        await page.mouse.down();
        await page.mouse.move(name.x + 40, name.y - 60, {steps: 4});
        await page.mouse.up();
        await expect(shownNames(page)).toHaveText(['RedMatic', 'Homematic-Manager', 'ioBroker']);
    }
    // the order is the account's: a reload shows it again, tabs and rows alike
    await page.reload();
    await expect(shownNames(page)).toHaveText(['RedMatic', 'Homematic-Manager', 'ioBroker']);
    await button(page).click();
    await expect(front(page)).toHaveText(['RedMatic', 'Homematic-Manager', 'ioBroker']);
});

test('Alt+↓ on a focused row and ↓ on its handle move it, and a live region says where it went', async ({page, baseURL}) => {
    await prepare(page, baseURL!, {more: true});
    await page.setViewportSize(WIDE);
    await page.goto('/');
    await button(page).click();
    for (const name of ['Homematic-Manager', 'ioBroker']) await pin(page, name).click();
    await expect(shownNames(page)).toHaveText(['Homematic-Manager', 'ioBroker']);
    // the handle: plain arrows
    await grip(page, 'Homematic-Manager').focus();
    await page.keyboard.press('ArrowDown');
    await expect(front(page)).toHaveText(['ioBroker', 'Homematic-Manager', 'RedMatic']);
    await expect(page.getByRole('status').filter({hasText: /position/})).toHaveText('Homematic-Manager is now at position 2 of 3');
    // the focus stays on the handle that was pressed, so a second press moves it on
    await page.keyboard.press('ArrowDown');
    await expect(front(page)).toHaveText(['ioBroker', 'RedMatic', 'Homematic-Manager']);
    await expect(shownNames(page)).toHaveText(['ioBroker', 'Homematic-Manager']);
    await expect(page.getByRole('status').filter({hasText: /position/})).toHaveText('Homematic-Manager is now at position 3 of 3');
    // the end of the list: nothing happens
    await page.keyboard.press('ArrowDown');
    await expect(front(page)).toHaveText(['ioBroker', 'RedMatic', 'Homematic-Manager']);
    // anywhere else in the row - the name - it takes Alt; a plain arrow is left to the browser
    await row(page, 'RedMatic').getByRole('menuitem').focus();
    await page.keyboard.press('ArrowUp');
    await expect(front(page)).toHaveText(['ioBroker', 'RedMatic', 'Homematic-Manager']);
    await page.keyboard.press('Alt+ArrowUp');
    await expect(front(page)).toHaveText(['RedMatic', 'ioBroker', 'Homematic-Manager']);
    await expect(page.getByRole('status').filter({hasText: /position/})).toHaveText('RedMatic is now at position 1 of 3');
    await expect(popup(page)).toBeVisible();
    // the second group does not move
    await row(page, 'Mosquitto').getByRole('link', {name: 'Settings for Mosquitto'}).focus();
    await page.keyboard.press('Alt+ArrowUp');
    await expect(popup(page).locator('.ol-addonname')).toHaveText(['RedMatic', 'ioBroker', 'Homematic-Manager', 'hm2mqtt', 'JP HB Devices', 'Mosquitto', 'CCU WebUI']);
    // nor does a nav.d link (the follow-up to task 131)
    await row(page, 'CCU WebUI').getByRole('menuitem').focus();
    await page.keyboard.press('Alt+ArrowUp');
    await expect(popup(page).locator('.ol-addonname')).toHaveText(['RedMatic', 'ioBroker', 'Homematic-Manager', 'hm2mqtt', 'JP HB Devices', 'Mosquitto', 'CCU WebUI']);
    await expect(popup(page)).toBeVisible();
});

test('the pins survive a reload, and an addon that is gone loses its pin and its tab', async ({page, baseURL}) => {
    await prepare(page, baseURL!, {more: true});
    await page.setViewportSize(WIDE);
    await page.goto('/');
    await button(page).click();
    await pin(page, 'ioBroker').click();
    await pin(page, 'RedMatic').click();
    await expect(shownNames(page)).toHaveText(['ioBroker', 'RedMatic']);
    await page.reload();
    await expect(shownNames(page)).toHaveText(['ioBroker', 'RedMatic']);
    await button(page).click();
    await expect(pin(page, 'ioBroker')).toHaveAttribute('aria-pressed', 'true');
    await expect(pin(page, 'Homematic-Manager')).toHaveAttribute('aria-pressed', 'false');
    // ioBroker uninstalled: the cookie dropped, the box lists it no more
    await page.context().clearCookies({name: 'stub-addon-more'});
    await page.reload();
    await expect(shownNames(page)).toHaveText(['RedMatic']);
    await button(page).click();
    // a pin changes no order: by name, as before, without ioBroker
    await expect(front(page)).toHaveText(['Homematic-Manager', 'RedMatic']);
    await expect(row(page, 'ioBroker')).toHaveCount(0);
    await expect(pin(page, 'RedMatic')).toHaveAttribute('aria-pressed', 'true');
});

test('the pinned tabs that do not fit beside the fixed tabs fold into the dropdown, which shows them pinned', async ({page, baseURL, isMobile}) => {
    test.skip(isMobile, 'a phone folds every pinned tab; the test below');
    await prepare(page, baseURL!, {more: true});
    await page.setViewportSize(WIDE);
    await page.goto('/');
    await button(page).click();
    for (const name of ['Homematic-Manager', 'ioBroker', 'RedMatic']) await pin(page, name).click();
    await page.keyboard.press('Escape');
    await expect(shownNames(page)).toHaveText(['Homematic-Manager', 'ioBroker', 'RedMatic']);
    const rowTop = async () => page.locator('nav.ol-nav .ol-menubtn').first().evaluate((el) => el.getBoundingClientRect().top);
    // at 845 px Homematic-Manager (the first, 157 px without the letter badge, D-84) does not fit
    // beside the fixed tabs - it does from 846 px - and neither does anything after it: the order is
    // the user's, not the tightest packing (it was 897 px until task 256 took the release text out
    // of the bar, 830 px until task 193 put Control - 67 px, de Bedienung - beside Status, 1280 px
    // before task 57 took Firmware and the System dropdown's
    // reserved width out of the bar, 1137 px while the tab carried the letter, 1110 px before task
    // 131 took Interfaces and Metadata out, and 940 px before the nav.d entry moved into the dropdown:
    // the room is the window's width minus 683 now, lib/addonorder.ts)
    await page.setViewportSize({width: 845, height: 800});
    await expect(shownNames(page)).toHaveText([]);
    await expect(page.locator('.ol-header')).toHaveCSS('height', '36px'); // one row
    await expect(button(page)).not.toHaveClass(/active/);
    // the dropdown still shows the three pinned
    await button(page).click();
    for (const name of ['Homematic-Manager', 'ioBroker', 'RedMatic']) await expect(pin(page, name)).toHaveAttribute('aria-pressed', 'true');
    // unpinning Homematic-Manager lets ioBroker (94 px) in, but not RedMatic after it (100 px more)
    await pin(page, 'Homematic-Manager').click();
    await expect(shownNames(page)).toHaveText(['ioBroker']);
    await page.keyboard.press('Escape');
    const top = await rowTop();
    for (const tab of await page.locator('nav.ol-nav > a:not(.ol-pintab-folded)').all()) expect(await tab.evaluate((el) => el.getBoundingClientRect().top)).toBe(top);
    // on a folded addon's page the Addons tab is the active one
    await page.goto('/nav/red');
    await expect(shownNames(page)).toHaveText(['ioBroker']);
    await expect(button(page)).toHaveClass(/active/);
    // room again: both stand in the bar, and the open one is marked
    await page.setViewportSize(WIDE);
    await expect(shownNames(page)).toHaveText(['ioBroker', 'RedMatic']);
    await expect(shownTabs(page).filter({hasText: 'RedMatic'})).toHaveClass(/active/);
    await expect(button(page)).not.toHaveClass(/active/);
});

// Task 191: on a phone the pinned tabs no longer fold - the nav is the bar's second row there and
// scrolls sideways, so every one of them stands in it and the bar stays two rows however many are
// pinned (test/e2e/topbar-phone.spec.ts has the row itself).
test('on a phone every pinned tab stands in the scrolling row, and the bar stays two rows', async ({page, baseURL}) => {
    await prepare(page, baseURL!);
    await page.setViewportSize({width: 412, height: 900});
    await page.goto('/');
    const height = (await page.locator('.ol-header').boundingBox())!.height;
    await button(page).click();
    await pin(page, 'RedMatic').click();
    await pin(page, 'Homematic-Manager').click();
    await expect(pin(page, 'RedMatic')).toHaveAttribute('aria-pressed', 'true');
    await page.keyboard.press('Escape');
    await expect(shownTabs(page)).toHaveCount(2);
    expect((await shownNames(page).allTextContents()).sort()).toEqual(['Homematic-Manager', 'RedMatic']);
    expect((await page.locator('.ol-header').boundingBox())!.height).toBe(height);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(412);
    // the pinned addon's page: its own tab is the active one, the Addons button is not
    await shownTabs(page).filter({hasText: 'RedMatic'}).click();
    await expect(page).toHaveURL(/\/nav\/red$/);
    await expect(shownTabs(page).filter({hasText: 'RedMatic'})).toHaveClass(/active/);
    await expect(button(page)).not.toHaveClass(/active/);
});
