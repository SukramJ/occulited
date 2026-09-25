import {expect, test, type Page} from '@playwright/test';

/*
 * Task 132 (D-87): there is no System → Security any more. Its login settings and API tokens are
 * sections of System → Users, its HTTPS switches a section of System → Certificate (its addon
 * sessions are the Addons page's since 2026-09-16: addon-sessions-moved.spec.ts).
 * The old paths keep working: /security and /system/security are the Users page, and their HTTPS
 * anchor (#https, an HSTS link) is the Certificate page's section - on a load, a link and
 * Back/Forward.
 */
const tab = (page: Page) => page.locator('.ol-systab');
const menu = (page: Page) => page.locator('.ol-sysmenu');
const rows = (page: Page) => menu(page).getByRole('menuitem');
const heading = (page: Page) => page.getByRole('heading', {level: 1});

test('the System menu has no Security entry; Users and Certificate carry its keywords', async ({page}) => {
    await page.goto('/');
    await tab(page).click();
    await expect.poll(() => menu(page).getAttribute('data-phase')).toBe('open');
    await expect(rows(page).filter({hasText: 'Security'})).toHaveCount(0);
    const filter = menu(page).getByRole('textbox');
    for (const [q, want] of [['oidc', 'Users'], ['token', 'Remote access'], ['password login', 'Users'], ['sessions', 'Users'], ['hsts', 'Certificate'], ['https redirect', 'Certificate']] as const) {
        await filter.fill(q);
        await expect(rows(page), q).toHaveText([want]);
    }
});

for (const [from, to, section] of [
    ['/security', '/system/users', 'Authentication'],
    ['/system/security', '/system/users', 'Authentication'],
    ['/system/security?x=1', '/system/users?x=1', 'Authentication'],
    // the API tokens moved on to the Remote access page (2026-09-19)
    ['/system/security#api-tokens', '/system/remote-access#api-tokens', /^API tokens/],
    ['/system/security#https', '/system/certificates#https', 'HTTPS'],
    ['/security#https', '/system/certificates#https', 'HTTPS'],
    ['/system/security#hsts', '/system/certificates#https', 'HTTPS'],
    // the query goes along (the Certificate page reads its mode from it)
    ['/system/security?mode=acme#https', '/system/certificates?mode=acme#https', 'HTTPS'],
] as const) {
    test(`a load of ${from} lands on ${to}`, async ({page, baseURL}) => {
        await page.goto(from);
        await expect(page).toHaveURL(`${baseURL}${to}`);
        await expect(heading(page)).toHaveText(to.includes('users') ? /Users/ : to.includes('remote-access') ? /Remote access/ : /Certificate/);
        // the API tokens are a part of lite-rpc's section on Remote access since task 223: an h3
        const h2 = page.getByRole('heading', {level: to.includes('api-tokens') ? 3 : 2, name: section, exact: typeof section === 'string'});
        await expect(h2).toBeVisible();
        // an anchor scrolls its section into view once it has loaded
        if (to.includes('#')) await expect(h2).toBeInViewport();
    });
}

test('old Security paths in the history land on the new pages on Back and Forward', async ({page, baseURL}) => {
    await page.goto('/');
    await expect(heading(page)).toHaveText('Status');
    await page.evaluate(() => {
        history.pushState(null, '', '/system/security#https');
        history.pushState(null, '', '/security');
        history.pushState(null, '', '/');
    });
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/system/users`);
    await expect(page.getByRole('heading', {level: 2, name: 'Authentication'})).toBeVisible();
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/system/certificates#https`);
    await expect(page.getByRole('heading', {level: 2, name: 'HTTPS'})).toBeVisible();
    await page.goForward();
    await expect(page).toHaveURL(`${baseURL}/system/users`);
});

test('an old HTTPS anchor reached through the history is replaced by the Certificate section', async ({page, baseURL}) => {
    await page.goto('/system/users');
    await expect(page.getByRole('heading', {level: 2, name: 'Authentication'})).toBeVisible();
    // an entry an older shell left, reached the way Back reaches it
    await page.evaluate(() => {
        history.pushState(null, '', '/system/security#hsts');
        dispatchEvent(new PopStateEvent('popstate'));
    });
    await expect(page).toHaveURL(`${baseURL}/system/certificates#https`);
    await expect(page.getByRole('heading', {level: 2, name: 'HTTPS'})).toBeVisible();
    await page.goBack();
    await expect(page).toHaveURL(`${baseURL}/system/users`);
});

test('in German: Benutzer with Anmeldung and API-Tokens, Zertifikat with HTTPS', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/users');
    await expect(heading(page)).toHaveText(/System ›\s*Benutzer/);
    await expect(page.getByRole('heading', {level: 2, name: /^Anmeldung$/})).toBeVisible();
    await expect(page.locator('[data-tokens-link] a')).toHaveText('API-Tokens: Fernzugriff');
    await expect(page.getByRole('heading', {level: 2, name: /^Sitzungen für Zusatzsoftware$/})).toHaveCount(0);
    await page.goto('/system/certificates');
    await expect(heading(page)).toHaveText(/System ›\s*Zertifikat/);
    await expect(page.getByRole('heading', {level: 2, name: 'HTTPS'})).toBeVisible();
    await expect(page.getByRole('checkbox', {name: /HTTP auf HTTPS umleiten/})).toBeVisible();
});
