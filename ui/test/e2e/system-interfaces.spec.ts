import {expect, test, type Page} from '@playwright/test';

/*
 * Task 131 (D-86): Interfaces is a System page - /system/interfaces, at the top of the System
 * menu - and its old tab path /radio stands for it, the query string and the anchor kept, on a
 * load, a link and Back/Forward. A warning that leads to the Interfaces page shows its dot on that
 * entry and on the System tab. The Metadata page that stood beside it went with task 193: its
 * paths open the App, and the menu has no such entry.
 */
const tab = (page: Page) => page.locator('.ol-systab');
const menu = (page: Page) => page.locator('.ol-sysmenu');
const rows = (page: Page) => menu(page).getByRole('menuitem');
const heading = (page: Page) => page.getByRole('heading', {level: 1});

async function open(page: Page) {
    await tab(page).click();
    await expect(menu(page)).toBeVisible();
    await expect.poll(() => menu(page).getAttribute('data-phase')).toBe('open');
}

test('the System menu lists Interfaces and Keys first, no Metadata, and opens them under /system', async ({page}) => {
    await page.goto('/');
    await open(page);
    await expect(rows(page).nth(0)).toHaveText('Interfaces');
    // openccu-lite task 222: the LAN devices beside Interfaces; task 183: the radio keys' own page after them
    await expect(rows(page).nth(1)).toHaveText('LAN devices');
    await expect(rows(page).nth(2)).toHaveText('Keys');
    await expect(rows(page).filter({hasText: 'Metadata'})).toHaveCount(0);
    await rows(page).filter({hasText: 'Interfaces'}).click();
    await expect(page).toHaveURL(/\/system\/interfaces$/);
    await expect(heading(page)).toHaveText(/^System ›\s*Interfaces/);
    await expect(page.locator('[data-process="rfd"]')).toBeVisible();
    await expect(tab(page)).toHaveClass(/active/);

    // the title switcher opens the same menu, with Interfaces checked
    await heading(page).getByRole('button').click();
    await expect(menu(page)).toBeVisible();
    await expect(rows(page).filter({hasText: 'Interfaces'})).toHaveAttribute('aria-current', 'page');
    await rows(page).filter({hasText: 'Keys'}).click();
    await expect(page).toHaveURL(/\/system\/keys$/);
    await expect(heading(page)).toHaveText(/^System ›\s*Keys/);
    await expect(tab(page)).toHaveClass(/active/);
});

test('in German: System › Schnittstellen and System › Schlüssel, found by their keywords', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/interfaces');
    await expect(heading(page)).toHaveText(/^System ›\s*Schnittstellen/);
    await open(page);
    const filter = menu(page).getByRole('textbox');
    await filter.fill('Duty');
    await expect(rows(page)).toHaveText(['Schnittstellen']);
    await filter.fill('gewerke');
    await expect(rows(page)).toHaveCount(0);
    await filter.fill('netzwerkschlüssel');
    await expect(rows(page)).toHaveText(['Schlüssel']);
    await filter.press('Enter');
    await expect(page).toHaveURL(/\/system\/keys$/);
    await expect(heading(page)).toHaveText(/^System ›\s*Schlüssel/);
});

for (const [from, to] of [
    ['/radio', '/system/interfaces'],
    ['/radio?x=1#security-key', '/system/keys?x=1#security-key'],
    ['/system/interfaces#local-key', '/system/keys#local-key'],
] as const) {
    test(`a load of ${from} lands on ${to}`, async ({page, baseURL}) => {
        await page.goto(from);
        await expect(page).toHaveURL(`${baseURL}${to}`);
        await expect(heading(page)).toHaveText(to.includes('interfaces') ? /Interfaces/ : /Keys/);
    });
}

// task 193: the Metadata page's paths open the App, the editor now
for (const from of ['/metadata', '/names', '/system/metadata', '/metadata?q=k%C3%BCche']) {
    test(`a load of ${from} lands on the App`, async ({page, baseURL}) => {
        await page.goto(from);
        await expect(page).toHaveURL(new RegExp(`^${baseURL}/app`));
        await expect(page.locator('[data-app-title]')).toBeVisible();
    });
}

// task 183: the section moved to the Keys page; the old anchors lead there
test('/radio#security-key from the Status page lands on the Keys page\'s section, with the anchor kept', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-key', value: 'default', url: baseURL!}]);
    await page.goto('/radio#security-key');
    await expect(page).toHaveURL(/\/system\/keys#security-key$/);
    await expect(page.locator('#ol-sec-key')).toBeFocused();
    await expect(page.locator('#security-key')).toBeInViewport();
});

test('old paths in the history land on the new ones on Back and Forward, query and anchor kept', async ({page, baseURL}) => {
    await page.goto('/');
    await expect(heading(page)).toHaveText('Status');
    // entries as an older shell or a bookmark left them
    await page.evaluate(() => {
        history.pushState(null, '', '/radio?from=old#security-key');
        history.pushState(null, '', '/services?x=1');
        history.pushState(null, '', '/');
    });
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/system/services?x=1`);
    await expect(heading(page)).toHaveText(/Services/);
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/system/keys?from=old#security-key`);
    await expect(heading(page)).toHaveText(/Keys/);
    await page.goForward();
    await expect(page).toHaveURL(`${baseURL}/system/services?x=1`);

    // a plain link to an old path (another page, a bookmark) is a load: it lands the same, and Back
    // returns to where it was
    await page.goto('/system/firewall');
    await page.evaluate(() => {
        const a = document.createElement('a');
        a.href = '/log?y=2';
        a.textContent = 'old link';
        a.id = 'old-link';
        document.querySelector('main')!.append(a);
    });
    await page.locator('#old-link').click();
    await expect(page).toHaveURL(`${baseURL}/system/log?y=2`);
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/system/firewall`);
});

test('a warning that leads to a moved section shows its dot on the page it moved to (Keys) and on the System tab', async ({page}) => {
    // only the security-key warning, so the tab's dot is its own
    await page.route('**/api/system/v1/warnings', (route) =>
        route.fulfill({json: {warnings: [{id: 'security-key', variant: 'default', severity: 'warning', href: '/radio#security-key'}], periods: [1, 7, 30]}}),
    );
    await page.goto('/');
    await expect(tab(page).locator('.ol-sysdot')).toHaveAttribute('aria-label', 'Warning');
    await open(page);
    await expect(rows(page).filter({hasText: 'Keys'}).locator('.ol-sysdot')).toHaveAttribute('aria-label', 'Warning');
    await expect(menu(page).locator('.ol-sysdot')).toHaveCount(1);
});

test('a phone opens Interfaces and Keys from the System sheet', async ({page, isMobile}) => {
    test.skip(!isMobile, 'the phone project');
    await page.goto('/');
    await open(page);
    await rows(page).filter({hasText: 'Keys'}).click();
    await expect(page).toHaveURL(/\/system\/keys$/);
    await expect(heading(page)).toHaveText(/Keys/);
    await open(page);
    await rows(page).filter({hasText: 'Interfaces'}).click();
    await expect(page).toHaveURL(/\/system\/interfaces$/);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width);
});

// task 183: the Keys page - its three sections, and its name in German
test('the Keys page holds the security key, the HmIP network key and the device keys; Schlüssel in German', async ({page}) => {
    await page.goto('/system/keys');
    await expect(heading(page)).toHaveText(/^System ›\s*Keys/);
    await expect(page.locator('h2#security-key')).toBeVisible();
    await expect(page.locator('h2#local-key')).toBeVisible();
    await expect(page.locator('h2#device-keys')).toBeVisible();
    await page.goto('/system/interfaces');
    await expect(page.locator('[data-process="rfd"]')).toBeVisible();
    await expect(page.locator('h2#security-key, h2#local-key, h2#device-keys')).toHaveCount(0);
    await page.evaluate(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/keys');
    await expect(heading(page)).toHaveText(/^System ›\s*Schlüssel/);
});
