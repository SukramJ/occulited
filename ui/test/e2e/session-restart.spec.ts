import {expect, test, type Page} from '@playwright/test';
import fs from 'node:fs';
import {Box, buildBinary, removeBinary} from './realbox';

// B-102 (D-67): a login survives a restart of occulited and a reboot, and so does a logout. Unlike
// the other specs this one runs the real occulited - built from this checkout with the web
// interface `npm run build` embedded - over a state directory of its own, and restarts it the way
// a box does: stopped (SIGTERM) or killed (a crash), and started again over the same state with the
// tmpfs session mirror emptied as a reboot empties it. The daemon and its box are realbox.ts's.

test.beforeAll(async ({}, workerInfo) => buildBinary(workerInfo));
test.afterAll(removeBinary);

let box: Box;

test.beforeEach(async () => {
    box = new Box();
    await box.start();
    const setup = await fetch(`${box.url}/api/auth/v1/setup`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({username: 'admin', password: 'restart-pass-1'})});
    const body = await setup.text();
    expect(setup.status, body).toBe(200);
    // the setup logs in too; that session ends, so the browser's is the only one
    // (task 259: a state-changing call on the cookie alone carries the header credential)
    const out = await fetch(`${box.url}/api/auth/v1/logout`, {method: 'POST', headers: {Cookie: `occulite_session=${JSON.parse(body).sid}`, 'X-Occulite-Request': '1'}});
    expect(out.status).toBe(200);
});

test.afterEach(async () => {
    await box.stop('SIGKILL');
    fs.rmSync(box.dir, {recursive: true, force: true});
});

async function login(page: Page) {
    await page.goto(`${box.url}/`);
    const form = page.locator('form.ol-card');
    await expect(form).toBeVisible();
    await page.locator('input:not([type=password])').first().fill('admin');
    await page.locator('input[type=password]').fill('restart-pass-1');
    await page.getByRole('button', {name: 'Login'}).click();
    await expectLoggedIn(page);
    // task 259: the session cookie is scoped to /api, the gate cookie (the same id) to /addons/
    const cookie = (await page.context().cookies(`${box.url}/api/`)).find((c) => c.name === 'occulite_session');
    expect(cookie?.value).toMatch(/^[A-Z2-7]{26}$/); // task 125: 26 characters of base32
    expect(cookie?.path).toBe('/api');
    const gate = (await page.context().cookies(`${box.url}/addons/`)).find((c) => c.name === 'occulite_gate');
    expect(gate?.value).toBe(cookie!.value);
    expect((await page.context().cookies(`${box.url}/addons/`)).find((c) => c.name === 'occulite_session'), 'the API cookie never reaches /addons/').toBeUndefined();
    return cookie!.value;
}

async function expectLoggedIn(page: Page) {
    await expect(page.locator('header.ol-header a[href="/account"]')).toBeAttached();
    await expect(page.locator('form.ol-card')).toHaveCount(0);
}

test('a login survives a crash and a reboot of occulited', async ({page}) => {
    test.setTimeout(90_000);
    const sid = await login(page);

    // killed, not stopped: the login was written when it happened, not at a shutdown
    await box.reboot('SIGKILL');
    await page.reload();
    await expectLoggedIn(page);
    expect(await box.authenticated(sid)).toBe(true);

    // and a clean restart on top
    await box.reboot('SIGTERM');
    await page.reload();
    await expectLoggedIn(page);

    // neither the state directory nor the mirror holds the session id
    expect(box.holding(sid)).toEqual([]);
    expect(fs.readdirSync(box.mirror)).toHaveLength(1);
});

test('a logout survives a reboot of occulited', async ({page}) => {
    test.setTimeout(90_000);
    const sid = await login(page);
    await box.reboot('SIGTERM');
    await page.reload();
    await expectLoggedIn(page);

    await page.goto(`${box.url}/account`);
    await page.getByRole('button', {name: 'Logout'}).click();
    await expect(page.locator('form.ol-card')).toBeVisible();

    await box.reboot('SIGKILL');
    await page.reload();
    await expect(page.locator('form.ol-card')).toBeVisible();
    // the id itself, sent again as a cookie, names no session either
    expect(await box.authenticated(sid)).toBe(false);
    expect(fs.readdirSync(box.mirror)).toHaveLength(0);
    expect(box.holding(sid)).toEqual([]);
});
