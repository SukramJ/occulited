import {expect, test, type Page} from '@playwright/test';

// Task 191: on a phone the bar is two rows, and which is which is said outright - the wordmark and
// the icon group (licences, GitHub, account, settings, power) in the first, the whole nav in the
// second. Before it the bar was one flex row with `flex-wrap: wrap`, so it broke wherever the
// widths fell: the icons landed on a second line and the tabs on a third. The nav's row is one line
// that scrolls sideways when it does not fit, with every pinned addon in it - they folded into the
// dropdown on a phone until now - and the version text is gone below 700 px, not only below 480.
const PHONE = {width: 412, height: 915};
const SMALL = {width: 360, height: 640};

const nav = (page: Page) => page.locator('nav.ol-nav');
const icons = (page: Page) => page.locator('.ol-header > .ol-iconlink, .ol-header > .ol-power');

/** which row each part of the bar sits on: its vertical middle, rounded - the parts of one row are
    centred on it and have different heights, so their tops differ by a pixel or three */
async function rows(page: Page) {
    return page.evaluate(() => {
        const mid = (el: Element) => {
            const r = el.getBoundingClientRect();
            return Math.round(r.top + r.height / 2);
        };
        const header = document.querySelector('.ol-header')!;
        return {
            brand: mid(header.querySelector('.ol-brand')!),
            icons: Array.from(header.querySelectorAll(':scope > .ol-iconlink, :scope > .ol-power')).map(mid),
            nav: mid(header.querySelector('nav.ol-nav')!),
            height: Math.round(header.getBoundingClientRect().height),
        };
    });
}
/** the two rows are apart when the nav's middle is below the wordmark's by more than a line */
const sameRow = (a: number, b: number) => Math.abs(a - b) <= 3;

let n = 0;
/** the stub keeps pins per browser under the cookie; `more` adds a third frontend addon */
async function prepare(page: Page, baseURL: string, more = false) {
    const cookies = [{name: 'stub-prefs', value: `topbar-${process.pid}-${Date.now()}-${n++}`, url: baseURL}];
    if (more) cookies.push({name: 'stub-addon-more', value: '1', url: baseURL});
    await page.context().addCookies(cookies);
}

/** pin every addon the dropdown offers, so the nav row has to scroll */
async function pinAll(page: Page) {
    const button = page.locator('.ol-menu .ol-menubtn').first();
    await button.click();
    const popup = page.locator('.ol-menu').first().getByRole('menu');
    const pins = popup.locator('.ol-menurow-front .ol-pin');
    for (let i = 0; i < (await pins.count()); i++) {
        const pin = pins.nth(i);
        if ((await pin.getAttribute('aria-pressed')) !== 'true') await pin.click();
    }
    await page.keyboard.press('Escape');
    await expect(popup).toHaveCount(0);
}

for (const size of [PHONE, SMALL]) {
    test(`the bar is two rows at ${size.width} px: the wordmark and the icons, then the nav`, async ({page, baseURL}) => {
        await prepare(page, baseURL!, true);
        await page.setViewportSize(size);
        await page.goto('/');
        await expect(nav(page)).toBeVisible();
        const r = await rows(page);
        expect(r.icons.length).toBeGreaterThan(2);
        for (const top of r.icons) expect(sameRow(top, r.brand)).toBe(true);
        expect(r.nav).toBeGreaterThan(r.brand + 3);
        // task 99: two rows, and the bar still leaves the page most of the screen
        expect(r.height).toBeLessThan(size.height / 5);
        // the icon group ends at the bar's right edge, the wordmark begins at its left
        const box = await page.locator('.ol-header').boundingBox();
        const last = await icons(page).last().boundingBox();
        const brand = await page.locator('.ol-brand').boundingBox();
        expect(last!.x + last!.width).toBeGreaterThan(box!.x + box!.width - 16);
        expect(brand!.x).toBeLessThan(box!.x + 16);
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(size.width);
    });
}

test('every pinned addon stands in the nav row, which scrolls sideways when it does not fit', async ({page, baseURL}) => {
    await prepare(page, baseURL!, true);
    await page.setViewportSize(SMALL);
    await page.goto('/');
    await pinAll(page);
    const tabs = nav(page).locator('a.ol-pintab:not(.ol-pintab-folded)');
    expect(await tabs.count()).toBeGreaterThan(1);
    // the row scrolls, the page does not
    const room = () => page.evaluate(() => {
        const el = document.querySelector('nav.ol-nav')!;
        return {over: el.scrollWidth - el.clientWidth, left: el.scrollLeft, page: document.documentElement.scrollWidth};
    });
    const before = await room();
    expect(before.over).toBeGreaterThan(1);
    expect(before.page).toBeLessThanOrEqual(SMALL.width);
    // the fade says which edge still has something to show
    const row = page.locator('.ol-navrow');
    await expect(row).not.toHaveAttribute('data-more-left', /.*/);
    await expect(row).toHaveAttribute('data-more-right', '');
    await page.evaluate(() => document.querySelector('nav.ol-nav')!.scrollBy({left: 9999}));
    await expect(row).toHaveAttribute('data-more-left', '');
    await expect(row).not.toHaveAttribute('data-more-right', /.*/);
    const after = await room();
    expect(after.left).toBeGreaterThan(before.left);
    expect(after.page).toBeLessThanOrEqual(SMALL.width);
    // every part of the bar is still on its own row
    const r = await rows(page);
    for (const top of r.icons) expect(sameRow(top, r.brand)).toBe(true);
    expect(r.nav).toBeGreaterThan(r.brand + 3);
});

test('the active tab is scrolled into view on a phone', async ({page, baseURL}) => {
    await prepare(page, baseURL!, true);
    await page.setViewportSize(SMALL);
    await page.goto('/');
    await pinAll(page);
    const last = nav(page).locator('a.ol-pintab:not(.ol-pintab-folded)').last();
    const id = await last.getAttribute('data-addon');
    await last.click();
    await expect(page.locator(`nav.ol-nav a[data-addon="${id}"]`)).toHaveClass(/active/);
    // after a reload the row starts at its left end; the active tab is brought back into view
    await page.reload();
    const active = page.locator('nav.ol-nav a.active');
    await expect(active).toHaveCount(1);
    await expect.poll(async () => {
        const box = await nav(page).boundingBox();
        const tab = await active.boundingBox();
        return tab!.x >= box!.x - 1 && tab!.x + tab!.width <= box!.x + box!.width + 1;
    }).toBe(true);
});

test('the menus open from a scrolled nav row', async ({page, baseURL}) => {
    await prepare(page, baseURL!, true);
    await page.setViewportSize(SMALL);
    await page.goto('/');
    await pinAll(page);
    await page.evaluate(() => document.querySelector('nav.ol-nav')!.scrollBy({left: 9999}));
    // the System button is the row's last: its sheet hangs below the bar and is not clipped
    const system = page.locator('.ol-systab');
    await system.click();
    const sheet = page.locator('.ol-sysmenu');
    await expect(sheet).toBeVisible();
    const bar = await page.locator('.ol-header').boundingBox();
    const box = await sheet.boundingBox();
    expect(box!.y).toBeGreaterThanOrEqual(bar!.y + bar!.height - 1);
    await page.keyboard.press('Escape');
    await expect(sheet).toBeHidden();
    // the Addons dropdown: a sheet under the bar too, because the scrolling row would clip it
    await page.locator('.ol-menu .ol-menubtn').first().click();
    const popup = page.locator('.ol-menupop');
    await expect(popup).toBeVisible();
    const pop = await popup.boundingBox();
    expect(pop!.y).toBeGreaterThanOrEqual(bar!.y + bar!.height - 1);
    expect(pop!.x).toBeGreaterThanOrEqual(0);
    expect(pop!.x + pop!.width).toBeLessThanOrEqual(SMALL.width);
});

test('before the login the bar is one row', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {authenticated: false, setup_required: false}}));
    await page.setViewportSize(SMALL);
    await page.goto('/');
    await expect(page.locator('form.ol-card')).toBeVisible();
    const r = await rows(page);
    expect(r.icons.length).toBe(2); // the licences § and GitHub, the ones shown before the login
    for (const top of r.icons) expect(sameRow(top, r.brand)).toBe(true);
    expect(r.height).toBeLessThan(48);
});
