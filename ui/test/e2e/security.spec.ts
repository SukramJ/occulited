import {expect, test} from '@playwright/test';

// What the Security page held (2026-09-10 until task 132, D-87): authentication (from Settings) and
// the API tokens (from Metadata) are on System → Users now, the HTTP → HTTPS redirect and HSTS
// (task 36) with their guard rails on System → Certificate. The addon sessions, on Users in task 132,
// are on the Addons page since the maintainer's follow-up (2026-09-16; addon-sessions-moved.spec.ts).
test('Users shows the authentication and the tokens, Certificate the HTTPS switches', async ({page}) => {
    await page.goto('/system/users');
    await expect(page.getByRole('heading', {level: 1, name: 'Users'})).toBeVisible();
    await expect(page.getByRole('heading', {level: 2, name: 'Authentication'})).toBeVisible();
    // the API tokens moved to the Remote access page (2026-09-19); a link says so
    await expect(page.getByRole('heading', {level: 2, name: 'API tokens'})).toHaveCount(0);
    await expect(page.locator('[data-tokens-link] a')).toHaveText('API tokens: Remote access');
    await expect(page.getByRole('heading', {level: 2, name: 'HTTPS'})).toHaveCount(0);
    await expect(page.getByRole('heading', {level: 2, name: 'Addon sessions'})).toHaveCount(0);
    // in this order, below the accounts and the sessions
    expect(await page.locator('main section[data-section]').evaluateAll((els) => els.map((e) => e.getAttribute('data-section')))).toEqual(['accounts', 'sessions', 'authentication']);
    // the stub's auth config: OIDC stored, local running - the restart notice and the fields
    await expect(page.getByRole('radio', {name: 'OpenID Connect'})).toBeChecked();
    await expect(page.getByText('restart occulited to apply it')).toBeVisible();
    await expect(page.getByPlaceholder('https://auth.example.org/application/o/openccu-lite/')).toHaveValue('https://auth.example.org/application/o/openccu-lite/');
    // task 51: the guide's title stays on the page and opens its four steps as a popup
    await page.getByRole('button', {name: 'Setting it up in authentik'}).click();
    await expect(page.getByRole('tooltip').locator('li')).toHaveCount(4);
    await expect(page.getByRole('tooltip')).toContainText('/api/auth/v1/oidc/callback');
    await page.keyboard.press('Escape');
    await expect(page.getByRole('tooltip')).toHaveCount(0);
    // the HTTPS section is the Certificate page's last, and nothing of the login is there
    await page.goto('/system/certificates');
    await expect(page.getByRole('heading', {level: 2, name: 'HTTPS'})).toBeVisible();
    await expect(page.locator('main h2').last()).toHaveAttribute('id', 'https');
    await expect(page.getByRole('heading', {level: 2, name: 'Authentication'})).toHaveCount(0);
    await expect(page.getByRole('heading', {level: 2, name: 'API tokens'})).toHaveCount(0);
    // the redirect is offered on any certificate; HSTS not on the self-signed one, with the reason
    const redirect = page.getByRole('checkbox', {name: /Redirect HTTP to HTTPS/});
    await expect(redirect).toBeEnabled();
    await expect(redirect).not.toBeChecked();
    await expect(page.getByRole('checkbox', {name: /^HSTS/})).toBeDisabled();
    await expect(page.getByText('Not offered while the certificate is self-signed')).toBeVisible();
    // no menu entry for it on Settings any more: Settings keeps the appearance only
    await page.goto('/settings');
    await expect(page.getByRole('heading', {level: 2, name: 'Appearance'})).toBeVisible();
    await expect(page.getByRole('heading', {level: 2, name: 'Authentication'})).toHaveCount(0);
});

test('the API tokens are listed with their scopes, expiry and ranges, with revoke and create; Metadata no longer has them', async ({page}) => {
    await page.goto('/system/remote-access');
    await expect(page.getByRole('heading', {level: 3, name: 'API tokens'})).toBeVisible();
    const rows = page.locator('table.ol-tokens tbody tr');
    await expect(rows).toHaveCount(2);
    await expect(rows.first()).toContainText('hm2mqtt');
    await expect(rows.first()).toContainText('olt_7f3ac1');
    // task 66: the scopes as badges, Full access by name, the expiry and the allowed ranges
    await expect(rows.first().locator('.ol-badge')).toHaveText(['meta:read']);
    await expect(rows.nth(1)).toContainText('ci-nightly');
    await expect(rows.nth(1).locator('.ol-badge')).toHaveText(['Full access']);
    await expect(rows.nth(1)).toContainText('192.168.0.0/24');
    await expect(rows.nth(1)).toContainText('2027');
    // revoke asks through the shell dialog; cancel sends nothing
    let deletes = 0;
    page.on('request', (r) => { if (r.method() === 'DELETE') deletes++; });
    await rows.first().getByRole('button', {name: 'Revoke'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Revoke token hm2mqtt?');
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    await expect(dialog).toBeHidden();
    expect(deletes).toBe(0);
    // the create form is behind its disclosure
    await expect(page.getByPlaceholder('Token name (e.g. hm2mqtt)')).toHaveCount(0);
    await page.getByRole('button', {name: 'Create token'}).click();
    await expect(page.getByPlaceholder('Token name (e.g. hm2mqtt)')).toBeVisible();
    await page.goto('/system/backup');
    await expect(page.getByRole('heading', {level: 1, name: 'Backup'})).toBeVisible();
    await expect(page.getByRole('heading', {level: 2, name: 'API tokens'})).toHaveCount(0);
});

test('the token dialog picks scopes, offers Full access with its hint, and posts what was chosen', async ({page}) => {
    await page.goto('/system/remote-access');
    await page.getByRole('button', {name: 'Create token'}).click();
    const create = page.locator('.ol-tokform').getByRole('button', {name: 'Create token'});
    // the box's fourteen scopes, each with its label and name, and the one installed addon's
    // ingress scope (openccu-lite task 307) below them; nothing chosen yet
    const boxes = page.locator('.ol-scope-grid input[type=checkbox]');
    await expect(boxes).toHaveCount(15);
    await expect(page.locator('.ol-scope-grid').first()).toContainText('Names and rooms: read');
    await expect(page.locator('.ol-scope-grid').first()).toContainText('rpc:admin');
    await expect(page.locator('[data-addon-scopes]')).toContainText('Addon RedMatic: its pages through the system');
    await expect(page.locator('[data-addon-scopes]')).toContainText('addon:redmatic');
    await expect(page.locator('[data-addon-scopes-hint]')).toContainText('Authorization: Bearer');
    await page.getByPlaceholder('Token name (e.g. hm2mqtt)').fill('hass');
    await expect(create).toBeDisabled();
    // Full access covers the explicit ones and says why explicit is safer
    const full = page.getByRole('checkbox', {name: /Full access/});
    await full.check();
    await expect(page.getByText('Explicit scopes are safer for programs')).toBeVisible();
    await expect(boxes.first()).toBeDisabled();
    await expect(boxes.first()).toBeChecked();
    await expect(create).toBeEnabled();
    await full.uncheck();
    await expect(boxes.first()).toBeEnabled();
    await expect(create).toBeDisabled();
    // two explicit scopes, an expiry and a range
    await page.getByRole('checkbox', {name: /Names and rooms: read/}).check();
    await page.getByRole('checkbox', {name: /^Status LED/}).check();
    await page.locator('#ol-tok-expires').fill('2027-06-30');
    await page.locator('#ol-tok-ips').fill('192.168.1.0/24, 10.0.0.5');
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/auth/v1/tokens')),
        create.click(),
    ]);
    const body = req.postDataJSON();
    expect(body.name).toBe('hass');
    expect(body.scopes).toEqual(['meta:read', 'led']);
    expect(body.ips).toEqual(['192.168.1.0/24', '10.0.0.5']);
    expect(body.expires).toMatch(/^2027-06-30T|^2027-07-01T/);
    // the secret is shown once, the form closed
    await expect(page.getByText('olt_0123456789abcdef0123456789abcdef')).toBeVisible();
    await expect(page.getByText('copy it now, it is not shown again')).toBeVisible();
    await expect(page.getByPlaceholder('Token name (e.g. hm2mqtt)')).toHaveCount(0);
});

test('the Addons page shows the scopes of an addon\'s own API token', async ({page}) => {
    await page.goto('/addons');
    const row = page.locator('#addon-row-mh');
    await expect(row).toContainText('API token: meta:write');
    await expect(page.locator('#addon-row-mosquitto')).not.toContainText('API token');
});

test('the redirect saves as one PUT and warns about the self-signed certificate', async ({page}) => {
    await page.goto('/system/certificates');
    const redirect = page.getByRole('checkbox', {name: /Redirect HTTP to HTTPS/});
    await redirect.check();
    await expect(page.getByText("every first visit then shows the browser's warning")).toBeVisible();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'PUT' && r.url().includes('/api/system/v1/https')),
        page.locator('button.primary', {hasText: 'Save'}).last().click(),
    ]);
    expect(req.postDataJSON()).toEqual({redirect_https: true, hsts: false, hsts_max_age_days: 7});
    await expect(page.getByText('Saved. lighttpd was reloaded.')).toBeVisible();
    await expect(redirect).toBeChecked();
});

test('HSTS on a CA certificate asks through the shell dialog and names the lock-out', async ({page}) => {
    await page.route('**/api/system/v1/https', (r) => {
        if (r.request().method() === 'GET') return r.fulfill({json: {redirect_https: true, hsts: false, hsts_max_age_days: 365, certificate: {self_signed: false, managed: true, mode: 'acme'}}});
        const b = r.request().postDataJSON();
        return r.fulfill({json: {...b, certificate: {self_signed: false, managed: true, mode: 'acme'}}});
    });
    await page.goto('/system/certificates');
    const hsts = page.getByRole('checkbox', {name: /^HSTS/});
    await expect(hsts).toBeEnabled();
    await hsts.check();
    // the period appears with HSTS
    const days = page.locator('[data-section="https"] input[type="number"]');
    await expect(days).toHaveValue('365');
    await days.fill('180');
    await page.locator('button.primary', {hasText: 'Save'}).last().click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('for 180 days');
    await expect(dialog).toContainText('locks that browser out');
    // Cancel: nothing sent
    let puts = 0;
    page.on('request', (r) => { if (r.method() === 'PUT' && r.url().includes('/api/system/v1/https')) puts++; });
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    await expect(dialog).toBeHidden();
    expect(puts).toBe(0);
    // confirm: the PUT carries HSTS with the period
    await page.locator('button.primary', {hasText: 'Save'}).last().click();
    const [req] = await Promise.all([
        page.waitForRequest((r) => r.method() === 'PUT' && r.url().includes('/api/system/v1/https')),
        page.getByRole('dialog').getByRole('button', {name: 'Switch HSTS on'}).click(),
    ]);
    expect(req.postDataJSON()).toEqual({redirect_https: true, hsts: true, hsts_max_age_days: 180});
    await expect(page.getByText('Saved. lighttpd was reloaded.')).toBeVisible();
});

test('an unmanaged CA certificate needs the trust tick before HSTS is offered', async ({page}) => {
    await page.route('**/api/system/v1/https', (r) => r.fulfill({json: {redirect_https: false, hsts: false, hsts_max_age_days: 365, certificate: {self_signed: false, managed: false, mode: 'self-signed'}}}));
    await page.goto('/system/certificates');
    const hsts = page.getByRole('checkbox', {name: /^HSTS/});
    await expect(hsts).toBeDisabled();
    await page.getByRole('checkbox', {name: /from a CA my browsers trust/}).check();
    await expect(hsts).toBeEnabled();
});

test('HSTS on while the box is back on self-signed is an error notice', async ({page}) => {
    await page.route('**/api/system/v1/https', (r) => r.fulfill({json: {redirect_https: true, hsts: true, hsts_max_age_days: 365, certificate: {self_signed: true, managed: false, mode: 'self-signed'}}}));
    await page.goto('/system/certificates');
    await expect(page.locator('[data-section="https"] .ol-notice.error')).toContainText('HSTS is on, but the system serves a self-signed certificate - a browser that has seen the header refuses it. Switch HSTS off below, or install a certificate from a CA above.');
});

test('a user account sees its sessions on Users and nothing of the login settings, tokens or HTTPS', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    const asked: string[] = [];
    page.on('request', (r) => {
        const u = new URL(r.url());
        if (/^\/api\/(auth\/v1\/(config|tokens|users)|system\/v1\/(https|legacy-session))/.test(u.pathname)) asked.push(u.pathname);
    });
    await page.goto('/system/users');
    await expect(page.getByRole('heading', {level: 2, name: 'Sessions'})).toBeVisible();
    await expect(page.locator('[data-section="sessions"] tbody tr').first()).toBeVisible();
    for (const name of ['Authentication', 'Addon sessions', 'API tokens']) await expect(page.getByRole('heading', {level: 2, name})).toHaveCount(0);
    await expect(page.locator('[data-section="accounts"]')).toHaveCount(0);
    await page.goto('/system/certificates');
    await expect(page.getByRole('heading', {level: 2, name: 'Current certificate'})).toBeVisible();
    await expect(page.getByRole('heading', {level: 2, name: 'HTTPS'})).toHaveCount(0);
    expect(asked).toEqual([]);
});

test('on a phone the Users page with its new sections and the Certificate page with HTTPS stay inside the screen', async ({page, isMobile}) => {
    test.skip(!isMobile, 'the phone project');
    for (const [path, ready] of [['/system/users', '[data-section="sessions"] table'], ['/system/remote-access', '[data-section="api-tokens"] table'], ['/system/certificates', '[data-section="https"] input[type="checkbox"]']] as const) {
        await page.goto(path);
        await expect(page.locator(ready).first()).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth), path).toBeLessThanOrEqual(page.viewportSize()!.width);
    }
});
