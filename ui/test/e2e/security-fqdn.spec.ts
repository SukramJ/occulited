import {expect, test} from '@playwright/test';

// System → Certificate (System → Security before task 132): the bare host name redirected to <host>.<domain>. The switch carries the real
// names, is greyed out with the reason the API gives, and saves with the other two switches - the
// field only when it changed, since a PUT without it leaves the redirect alone.
const base = {
    redirect_https: false,
    hsts: false,
    hsts_max_age_days: 365,
    certificate: {self_signed: false, managed: true, mode: 'acme'},
    redirect_fqdn: false,
    redirect_fqdn_host: 'ccu',
    redirect_fqdn_target: 'ccu.example.org' as string | null,
    redirect_fqdn_state: 'off',
    redirect_fqdn_reason: undefined as string | undefined,
};
const name = /Redirect https:\/\/ccu to https:\/\/ccu\.example\.org/;
const save = (page: import('@playwright/test').Page) => page.locator('button.primary', {hasText: 'Save'}).last().click();

test('the switch names the real names and is greyed out with the reason', async ({page}) => {
    // the stub: a self-signed certificate that does not cover the name
    await page.goto('/system/certificates');
    const sw = page.getByRole('checkbox', {name});
    await expect(sw).toBeDisabled();
    await expect(sw).not.toBeChecked();
    await expect(page.getByText('Not available: The certificate does not cover ccu.example.org, so the redirect would end in a certificate warning.')).toBeVisible();
    // the section is on the Certificate page itself: no link to it any more
    await expect(page.locator('[data-section="https"] a[href="/system/certificates"]')).toHaveCount(0);
});

test('without a domain the name is a placeholder and the reason says why', async ({page}) => {
    await page.route('**/api/system/v1/https', (r) => r.fulfill({json: {...base, redirect_fqdn_target: null, redirect_fqdn_state: 'unavailable', redirect_fqdn_reason: 'no-domain'}}));
    await page.goto('/system/certificates');
    const sw = page.getByRole('checkbox', {name: /Redirect https:\/\/ccu to https:\/\/ccu\.<domain>/});
    await expect(sw).toBeDisabled();
    await expect(page.getByText('Not available: The system knows no DNS domain')).toBeVisible();
});

test('on and off again: the field is sent only when the switch changed', async ({page}) => {
    let view = {...base};
    const puts: Record<string, unknown>[] = [];
    await page.route('**/api/system/v1/https', (r) => {
        if (r.request().method() === 'PUT') {
            const b = r.request().postDataJSON();
            puts.push(b);
            view = {...view, redirect_https: b.redirect_https, hsts: b.hsts, hsts_max_age_days: b.hsts_max_age_days};
            if (b.redirect_fqdn !== undefined) view = {...view, redirect_fqdn: b.redirect_fqdn, redirect_fqdn_state: b.redirect_fqdn ? 'active' : 'off'};
        }
        return r.fulfill({json: view});
    });
    await page.goto('/system/certificates');
    const sw = page.getByRole('checkbox', {name});
    await expect(sw).toBeEnabled();
    await expect(sw).not.toBeChecked();
    await sw.check();
    await save(page);
    await expect(page.getByText('Saved. lighttpd was reloaded.')).toBeVisible();
    expect(puts).toEqual([{redirect_https: false, hsts: false, hsts_max_age_days: 365, redirect_fqdn: true}]);
    await page.reload();
    await expect(page.getByRole('checkbox', {name})).toBeChecked();
    // the redirect to HTTPS alone: the field stays out of the PUT
    await page.getByRole('checkbox', {name: /Redirect HTTP to HTTPS/}).check();
    await save(page);
    await expect.poll(() => puts.length).toBe(2);
    expect(puts[1]).toEqual({redirect_https: true, hsts: false, hsts_max_age_days: 365});
    await page.getByRole('checkbox', {name}).uncheck();
    await save(page);
    await expect.poll(() => puts.length).toBe(3);
    expect(puts[2]).toEqual({redirect_https: true, hsts: false, hsts_max_age_days: 365, redirect_fqdn: false});
    await expect(page.getByRole('checkbox', {name})).not.toBeChecked();
});

test('suspended after a rename: still on, switchable, and the page says when it resumes', async ({page}) => {
    await page.route('**/api/system/v1/https', (r) => r.fulfill({json: {...base, redirect_fqdn: true, redirect_fqdn_host: 'lite', redirect_fqdn_target: 'lite.example.org', redirect_fqdn_state: 'suspended', redirect_fqdn_reason: 'not-covered'}}));
    await page.goto('/system/certificates');
    const sw = page.getByRole('checkbox', {name: /Redirect https:\/\/lite to https:\/\/lite\.example\.org/});
    await expect(sw).toBeChecked();
    await expect(sw).toBeEnabled();
    const warn = page.locator('.ol-warn', {hasText: 'Suspended:'});
    await expect(warn).toContainText('The certificate does not cover lite.example.org');
    await expect(warn).toContainText('It resumes by itself once a certificate for lite.example.org is installed');
});

test('a self-signed certificate that covers the name: allowed, with the warning', async ({page}) => {
    await page.route('**/api/system/v1/https', (r) => r.fulfill({json: {...base, certificate: {self_signed: true, managed: false, mode: 'self-signed'}}}));
    await page.goto('/system/certificates');
    const sw = page.getByRole('checkbox', {name});
    await expect(sw).toBeEnabled();
    await sw.check();
    await expect(page.getByText("every first visit then shows the browser's warning")).toBeVisible();
});

test('in German: the label with the real names and the reason', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/certificates');
    const sw = page.getByRole('checkbox', {name: /https:\/\/ccu auf https:\/\/ccu\.example\.org umleiten/});
    await expect(sw).toBeDisabled();
    await expect(page.getByText('Nicht verfügbar: Das Zertifikat gilt nicht für ccu.example.org')).toBeVisible();
});
