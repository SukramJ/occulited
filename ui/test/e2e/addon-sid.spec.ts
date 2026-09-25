import {expect, test, type Page} from '@playwright/test';

// Task 88's postponed part (D-67): the shell leaves ?sid= off the URLs of an addon the box says reads
// the gate's X-Occulite-Session (`session_header` on GET /nav and GET /addons - occulited decides it
// from the catalogue's runtime.session.header_since and the installed version). Every other addon, and
// an older version of the same one, gets ?sid= - since task 125 (D-77) with the session's legacy
// alias the shell asks the box for (LeGaCy0001 in the stub), never with the session id (A1b2C3d4E5).
// The cookie `stub-session-header=1` makes the stub's RedMatic 9.7.3 with the header, without it
// RedMatic is 9.4.0. An addon's frontend proxied to its own server never gets ?sid= (B-133): the alias
// is for the CGIs lighttpd serves itself; a server behind the proxy has the gate's header.
const SID = 'sid=@LeGaCy0001@';
const menu = (page: Page) => page.locator('.ol-menu').first();
const row = (page: Page, name: string) => menu(page).getByRole('menu').locator('.ol-menurow').filter({has: page.locator('.ol-addonname', {hasText: new RegExp(`^${name}$`)})});
const kept = (page: Page, prefix: string) => page.locator(`iframe.ol-kept-frame[src^="${prefix}"]`);

test.describe('an addon that reads the session header, new enough', () => {
    test.beforeEach(async ({page, baseURL}) => {
        await page.context().addCookies([{name: 'stub-session-header', value: '1', url: baseURL}]);
    });

    test('its frame and its new tab have no ?sid=, another addon keeps it', async ({page}) => {
        await page.goto('/nav/red');
        await expect(kept(page, '/addons/red/')).toHaveAttribute('src', /^\/addons\/red\/\?theme=\w+&lang=\w+$/);
        await menu(page).locator('.ol-menubtn').click();
        await expect(row(page, 'RedMatic').locator('a.ol-newtab[target="_blank"]')).toHaveAttribute('href', '/addons/red/');
        await expect(row(page, 'Homematic-Manager').locator('a.ol-newtab[target="_blank"]')).toHaveAttribute('href', '/addons/mh/');
        await expect(row(page, 'Homematic-Manager').locator('a[href^="/addon-settings/"]')).toHaveAttribute('href', '/addon-settings/mh');
    });

    test('its settings page has no ?sid=, another addon keeps it', async ({page}) => {
        await page.goto('/addon-settings/redmatic');
        await expect(kept(page, '/addons/redmatic/settings.cgi')).toHaveAttribute('src', /^\/addons\/redmatic\/settings\.cgi\?theme=\w+&lang=\w+$/);
        await page.goto('/addon-settings/mosquitto');
        await expect(kept(page, '/addons/mosquitto/settings.cgi')).toHaveAttribute('src', new RegExp(`^/addons/mosquitto/settings\\.cgi\\?${SID}&theme=\\w+&lang=\\w+$`));
    });
});

test.describe('the same addon, an older version', () => {
    test('its settings page keeps ?sid=, with the alias and never the session; its proxied frontend never has one', async ({page}) => {
        const asked: string[] = [];
        page.on('request', (r) => {
            if (r.url().includes('/api/auth/v1/legacy-sid')) asked.push(r.method());
        });
        await page.goto('/addon-settings/redmatic');
        await expect(kept(page, '/addons/redmatic/settings.cgi')).toHaveAttribute('src', new RegExp(`^/addons/redmatic/settings\\.cgi\\?${SID}&theme=\\w+&lang=\\w+$`));
        expect(asked).toEqual(['POST']);
        expect(await kept(page, '/addons/redmatic/settings.cgi').getAttribute('src')).not.toContain('A1b2C3d4E5');
        // B-133: the frontend is a page its own server answers behind lighttpd's proxy: no ?sid=
        await page.goto('/nav/red');
        await expect(kept(page, '/addons/red/')).toHaveAttribute('src', /^\/addons\/red\/\?theme=\w+&lang=\w+$/);
        await menu(page).locator('.ol-menubtn').click();
        await expect(row(page, 'RedMatic').locator('a.ol-newtab[target="_blank"]')).toHaveAttribute('href', '/addons/red/');
    });
});
