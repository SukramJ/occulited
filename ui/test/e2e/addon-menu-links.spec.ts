import {expect, test, type Page} from '@playwright/test';
import {pageWidth} from './scroll';

// The maintainer's follow-up to task 131 (2026-09-16): a nav.d entry that is no addon - a link the box
// was configured with - no longer stands after System in the tab bar; the bar is strictly Status, the
// pinned addons, Addons and System, and such entries are a group of their own in the Addons dropdown.
// The stub has one that opens a new tab (CCU WebUI); stub-nav-page=1 adds one that opens in the
// shell's frame (Manual / Handbuch at /nav/manual).
const menu = (page: Page) => page.locator('.ol-menu').first();
const button = (page: Page) => menu(page).locator('.ol-menubtn');
const popup = (page: Page) => menu(page).getByRole('menu');
const links = (page: Page) => popup(page).getByRole('group', {name: /Links set up on this system|Auf diesem System eingerichtete Links/});
const linkRow = (page: Page, name: string) => links(page).locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const barLabels = (page: Page) =>
    page.evaluate(() =>
        Array.from(document.querySelectorAll<HTMLElement>('nav.ol-nav > a:not(.ol-pintab-folded), nav.ol-nav .ol-menubtn')).map((el) => (el.querySelector('.ol-pintab-name') ?? el.querySelector('.ol-menubtn-text') ?? el).textContent?.trim() ?? ''),
    );

async function withNavPage(page: Page, baseURL: string) {
    await page.context().addCookies([{name: 'stub-nav-page', value: '1', url: baseURL}]);
}

test('a nav.d link is not in the tab bar but in the dropdown, under the addons and above the footer', async ({page, baseURL}) => {
    await withNavPage(page, baseURL!);
    await page.goto('/');
    await expect(page.locator('nav.ol-nav .ol-systab')).toBeVisible();
    await expect.poll(() => barLabels(page)).toEqual(['Status', 'Control', 'Addons', 'System']);
    await expect(page.locator('nav.ol-nav > a[href="http://192.0.2.119/"], nav.ol-nav > a[href="/nav/manual"]')).toHaveCount(0);
    await button(page).click();
    // in the box's order (the frame page 800, the WebUI 900), after the two addon groups
    const groups = popup(page).getByRole('group');
    await expect(groups).toHaveCount(3);
    await expect(groups.nth(2).locator('.ol-addonname')).toHaveText(['Manual', 'CCU WebUI']);
    // the order in the popup: the addon groups, a divider, the links, a divider, the footer
    const order = await popup(page).evaluate((el) => Array.from(el.children).map((c) => c.getAttribute('role') === 'group' ? c.getAttribute('aria-label') : c.classList.contains('ol-menusep') ? '—' : c.textContent?.trim()));
    expect(order).toEqual(['Addons with a web interface of their own', '—', 'Addons without a web interface', '—', 'Links set up on this system', '—', 'Manage addons']);
    // no handle, no pin, no ⚙ on a link: it is no addon
    await expect(links(page).locator('button.ol-grip, button.ol-pin, a[aria-label^="Settings for"]')).toHaveCount(0);
    // the letter badge, as an addon without a favicon has
    await expect(linkRow(page, 'Manual').locator('.ol-addonmono')).toHaveText('M');
});

test('a link that opens a new tab: the name and ↗ lead there, ↗ is no second tab stop', async ({page, baseURL}) => {
    await page.goto('/');
    await button(page).click();
    const row = linkRow(page, 'CCU WebUI');
    const item = row.getByRole('menuitem');
    await expect(item).toHaveAttribute('href', 'http://192.0.2.119/');
    await expect(item).toHaveAttribute('target', '_blank');
    await expect(item).toHaveAttribute('rel', 'noopener');
    await expect(item).toContainText('opens in a new tab'); // for a screen reader, not seen
    const arrow = row.locator('a.ol-newtab');
    await expect(arrow).toHaveAttribute('href', 'http://192.0.2.119/');
    await expect(arrow).toHaveAttribute('target', '_blank');
    await expect(arrow).toHaveAttribute('tabindex', '-1');
    // a click opens the tab and closes the menu; the shell stays where it was
    const [popupPage] = await Promise.all([page.context().waitForEvent('page'), item.click()]);
    await popupPage.close();
    await expect(popup(page)).toHaveCount(0);
    await expect(page).toHaveURL(/\/$/);
});

test('a link that opens in the shell: /nav/<id> in the frame, its row active and the Addons tab marked', async ({page, baseURL}) => {
    await withNavPage(page, baseURL!);
    await page.goto('/');
    await expect(button(page)).not.toHaveClass(/active/);
    await button(page).click();
    const row = linkRow(page, 'Manual');
    await expect(row.getByRole('link', {name: 'Open in new tab'})).toHaveAttribute('href', '/stub-manual/');
    await row.getByRole('menuitem').click();
    await expect(page).toHaveURL(/\/nav\/manual$/);
    await expect(page.frameLocator('iframe[src^="/stub-manual/?theme="]').getByRole('heading', {name: 'Manual'})).toBeVisible();
    await expect(popup(page)).toHaveCount(0);
    await expect(button(page)).toHaveClass(/active/);
    await expect(page.locator('nav.ol-nav > a.active')).toHaveCount(0);
    await button(page).click();
    await expect(linkRow(page, 'Manual')).toHaveClass(/active/);
    await expect(linkRow(page, 'CCU WebUI')).not.toHaveClass(/active/);
    // a load of the route marks the same
    await page.goto('/nav/manual');
    await expect(button(page)).toHaveClass(/active/);
    // Status again: nothing of it is marked
    await page.goto('/');
    await expect(button(page)).not.toHaveClass(/active/);
});

test('German: the group and the label in German', async ({page, baseURL}) => {
    await withNavPage(page, baseURL!);
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/');
    await expect.poll(() => barLabels(page)).toEqual(['Status', 'Bedienung', 'Zusatzsoftware', 'System']);
    await button(page).click();
    await expect(popup(page).getByRole('group', {name: 'Auf diesem System eingerichtete Links'})).toBeVisible();
    await expect(links(page).locator('.ol-addonname')).toHaveText(['Handbuch', 'CCU WebUI']);
    await expect(linkRow(page, 'CCU WebUI').getByRole('menuitem')).toContainText('öffnet sich in einem neuen Tab');
    await expect(linkRow(page, 'Handbuch').getByRole('link', {name: 'In neuem Tab öffnen'})).toBeVisible();
});

test('on a phone the links fit the dropdown, their ↗ in the addons column', async ({page, baseURL}) => {
    await withNavPage(page, baseURL!);
    await page.setViewportSize({width: 360, height: 740});
    await page.goto('/');
    await button(page).click();
    const pop = (await popup(page).boundingBox())!;
    expect(pop.x).toBeGreaterThanOrEqual(0);
    expect(pop.x + pop.width).toBeLessThanOrEqual(360);
    const addonArrow = (await popup(page).locator('.ol-menurow-front').first().getByRole('link', {name: 'Open in new tab'}).boundingBox())!;
    for (const name of ['Manual', 'CCU WebUI']) {
        const arrow = (await linkRow(page, name).locator('a.ol-newtab').boundingBox())!;
        expect(Math.abs(arrow.x - addonArrow.x), name).toBeLessThan(1);
        expect(arrow.x + arrow.width, name).toBeLessThanOrEqual(pop.x + pop.width);
    }
    expect(await pageWidth(page)).toBeLessThanOrEqual(360);
});
