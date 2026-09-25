import {expect, test, type Page} from '@playwright/test';

/*
 * The maintainer's follow-up to task 132 (2026-09-16): the *Addon sessions* section - task 125's global
 * switch that passes the session in addon URLs - is on the Addons page, next to the per-addon switches
 * in the list's ⋯ menus, for an administrator only; System → Users no longer has it. Old anchors
 * (/system/users#addon-sessions, and the Security page's and /users') go to /addons#addon-sessions.
 * The stub keeps the switches per browser under the cookie stub-legacy=<id>.
 */
const own = async (page: Page, baseURL: string | undefined, info: {project: {name: string}; title: string}) => {
    await page.context().addCookies([{name: 'stub-legacy', value: `moved-${info.project.name}-${info.title}`.replace(/[^\w-]/g, '_'), url: baseURL}]);
};
const section = (page: Page) => page.locator('main section[data-section="addon-sessions"]');
const row = (page: Page, name: string) => page.locator('.ad-card').filter({has: page.locator('.ad-name', {hasText: new RegExp(`^\\W*${name}$`)})}).first();

test('the Addons page has the section under the list and above Install addon; Users has it no more', async ({page}) => {
    await page.goto('/addons');
    await expect(page.getByRole('heading', {level: 1, name: 'Addons'})).toBeVisible();
    await expect(section(page).getByRole('heading', {level: 2, name: 'Addon sessions'})).toBeVisible();
    await expect(section(page).getByRole('checkbox', {name: /Pass the session in addon URLs/})).toBeChecked();
    // the order on the page: the list, the section, the install form
    const order = await page.evaluate(() => {
        const at = (el: Element | null) => (el ? Array.from(document.querySelectorAll('main *')).indexOf(el) : -1);
        return {
            table: at(document.querySelector('main .ad-cards')),
            section: at(document.querySelector('main section[data-section="addon-sessions"]')),
            install: at(Array.from(document.querySelectorAll('main h2')).find((h) => h.textContent?.startsWith('Install addon')) ?? null),
        };
    });
    expect(order.table).toBeGreaterThanOrEqual(0);
    expect(order.section).toBeGreaterThan(order.table);
    expect(order.install).toBeGreaterThan(order.section);
    // the help names the list above and its ⋯ menu, not another page
    await section(page).getByRole('button', {name: 'Help'}).click();
    await expect(page.getByRole('tooltip')).toContainText('the list above marks them, and its ⋯ menu switches it off per addon');
    await page.keyboard.press('Escape');
    await page.goto('/system/users');
    await expect(page.getByRole('heading', {level: 2, name: 'Authentication'})).toBeVisible();
    await expect(page.getByRole('heading', {level: 2, name: 'Addon sessions'})).toHaveCount(0);
    await expect(page.getByRole('checkbox', {name: /Pass the session in addon URLs/})).toHaveCount(0);
});

for (const from of ['/system/users#addon-sessions', '/users#addon-sessions', '/system/security#addon-sessions', '/security#addon-sessions']) {
    test(`a load of ${from} lands on the Addons page's section`, async ({page, baseURL}) => {
        await page.goto(from);
        await expect(page).toHaveURL(`${baseURL}/addons#addon-sessions`);
        await expect(page.getByRole('heading', {level: 1, name: 'Addons'})).toBeVisible();
        const h2 = page.getByRole('heading', {level: 2, name: 'Addon sessions'});
        await expect(h2).toBeVisible();
        await expect(h2).toBeInViewport();
    });
}

test('the old anchor in the history lands on the Addons page on Back and Forward; the other Users anchors stay', async ({page, baseURL}) => {
    await page.goto('/');
    await expect(page.getByRole('heading', {level: 1})).toHaveText('Status');
    await page.evaluate(() => {
        history.pushState(null, '', '/system/users#addon-sessions');
        history.pushState(null, '', '/system/users#authentication');
        history.pushState(null, '', '/');
    });
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/system/users#authentication`);
    await expect(page.getByRole('heading', {level: 2, name: 'Authentication'})).toBeVisible();
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/addons#addon-sessions`);
    await expect(page.getByRole('heading', {level: 2, name: 'Addon sessions'})).toBeVisible();
    await page.goForward();
    await expect(page).toHaveURL(`${baseURL}/system/users#authentication`);
});

test('the global switch changes the list on the same page: the badges and the ⋯ items follow at once', async ({page, baseURL}, info) => {
    await own(page, baseURL, info);
    await page.goto('/addons');
    await expect(row(page, 'Mosquitto').locator('.ad-legacy')).toHaveText('session in URL');
    const sw = section(page).getByRole('checkbox', {name: /Pass the session in addon URLs/});
    const save = section(page).getByRole('button', {name: 'Save'});
    await expect(save).toBeDisabled();
    await sw.uncheck();
    await expect(section(page).getByText('will refuse to open')).toBeVisible();
    await save.click();
    await expect(section(page).locator('.ol-notice', {hasText: 'Saved.'})).toBeVisible();
    // no reload: the badge says off, and the per-addon item is off with the reason
    await expect(row(page, 'Mosquitto').locator('.ad-legacy-off')).toHaveText('no session in URL');
    await expect(row(page, 'Mosquitto').locator('.ad-legacy')).toHaveCount(0);
    await row(page, 'Mosquitto').getByRole('button', {name: 'More actions'}).click();
    const item = page.getByRole('menuitemcheckbox', {name: /Pass the session in the URL/});
    await expect(item).toBeDisabled();
    await expect(item).toContainText('off for all addons, see Addon sessions below');
    await page.keyboard.press('Escape');
    // on again; then a per-addon switch shows in the section's list of exceptions
    await sw.check();
    await save.click();
    await expect(row(page, 'Mosquitto').locator('.ad-legacy')).toHaveText('session in URL');
    await row(page, 'Mosquitto').getByRole('button', {name: 'More actions'}).click();
    await page.getByRole('menuitemcheckbox', {name: /Pass the session in the URL/}).click();
    await expect(row(page, 'Mosquitto').locator('.ad-legacy-off')).toHaveText('no session in URL');
    await expect(section(page)).toContainText('Switched off for: mosquitto');
    await expect(section(page).getByRole('link')).toHaveCount(0);
});

test('a user account sees no Addon sessions on the Addons page and asks nothing about them', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    const asked: string[] = [];
    page.on('request', (r) => {
        const u = new URL(r.url());
        if (u.pathname.startsWith('/api/system/v1/legacy-session')) asked.push(u.pathname);
    });
    await page.goto('/addons');
    await expect(page.locator('main .ad-cards')).toBeVisible();
    await expect(page.getByRole('heading', {level: 2, name: 'Addon sessions'})).toHaveCount(0);
    await expect(page.getByRole('heading', {level: 2, name: /Install addon/})).toHaveCount(0);
    expect(asked).toEqual([]);
});

test('in German: Sitzungen für Zusatzsoftware on Zusatzsoftware', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/addons');
    await expect(section(page).getByRole('heading', {level: 2, name: 'Sitzungen für Zusatzsoftware'})).toBeVisible();
    await expect(section(page).getByRole('checkbox', {name: /Sitzung in den URLs der Zusatzsoftware übergeben/})).toBeVisible();
    await section(page).getByRole('button', {name: 'Hilfe'}).click();
    await expect(page.getByRole('tooltip')).toContainText('die Liste oben markiert sie');
});

test('on a phone the section stays inside the screen', async ({page, isMobile}) => {
    test.skip(!isMobile, 'the phone project');
    await page.goto('/addons#addon-sessions');
    await expect(section(page).getByRole('checkbox')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width);
    const box = (await section(page).boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width);
});
