import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';
import {CA} from '../stub/testcert.mjs';

// B-71: a user session opens the Interfaces and Certificate pages without calling the routes only an
// administrator may use. The stub answers every role alike, so the two routes are made to refuse
// here as the box does: a request to either would leave a 403 in the console, and the request
// itself is what the tests look for.
const USER = {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'};
const FORBIDDEN = {status: 403, json: {error: 'forbidden', message: 'administrator role required'}};

function watch(page: Page) {
    const errors: string[] = [];
    const calls: string[] = [];
    page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
    page.on('console', (m) => {
        if (m.type() === 'error') errors.push(`console: ${m.text()}`);
    });
    page.on('request', (r) => calls.push(`${r.method()} ${new URL(r.url()).pathname}`));
    return {errors, calls};
}

// the box on ACME with a custom CA, whose root the page previews under its field
async function customCA(page: Page) {
    await page.route('**/api/system/v1/certificate', async (route) => {
        const response = await route.fetch();
        const st = await response.json();
        st.settings = {...st.settings, mode: 'acme', directory: 'custom', directory_url: 'https://ca.lan:9000/acme/acme/directory', ca_root: CA};
        await route.fulfill({response, json: st});
    });
}

test('a user on Interfaces is not sent for the radio firmware', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: USER}));
    await page.route('**/api/system/v1/radio/firmware', (r) => r.fulfill(FORBIDDEN));
    const w = watch(page);
    await page.goto('/radio');
    await expect(page.getByRole('heading', {level: 2, name: 'Connections'})).toBeVisible();
    await expect(page.getByRole('heading', {level: 2, name: 'Radio firmware'})).toHaveCount(0);
    expect(w.calls).not.toContain('GET /api/system/v1/radio/firmware');
    expect(w.errors, w.errors.join('\n')).toEqual([]);
});

test('a user on Certificate is not sent for the preview of a custom CA root', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: USER}));
    await page.route('**/api/system/v1/certificate/inspect', (r) => r.fulfill(FORBIDDEN));
    await customCA(page);
    const w = watch(page);
    await page.goto('/system/certificates');
    await expect(page.getByRole('heading', {level: 2, name: 'Current certificate'})).toBeVisible();
    // the preview is asked for 350 ms after the fields settle: an absence needs the wait past that
    await page.waitForTimeout(1200);
    expect(w.calls.filter((c) => c.endsWith('/certificate/inspect'))).toEqual([]);
    expect(w.errors, w.errors.join('\n')).toEqual([]);
});

test('an administrator still gets the preview of a custom CA root', async ({page}) => {
    await customCA(page);
    const inspected = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/certificate/inspect'));
    await page.goto('/system/certificates');
    await inspected;
    await expect(page.locator('.hint [data-preview="certificate"]')).toContainText('Stub Test CA');
});
