import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// The recovery system has no HTTPS, and a browser that remembers HSTS for the box's name refuses
// it by name: the power menu, a system update's install and the HSTS question send the browser to
// the box's IP address, resolved when the question opens. The stub's network: wlan0 with the
// default route at 192.0.2.119, eth0 at 10.10.0.2 with a global and a unique-local IPv6 address.
//
// The box by name: the projects are Chromium, which maps openccu.test to the stub's loopback.

const PORT = Number(process.env.PORT || 8799);
const NAME = `http://openccu.test:${PORT}`;
const ADDRESS = 'http://192.0.2.119/';

test.use({launchOptions: {args: ['--host-resolver-rules=MAP openccu.test 127.0.0.1']}});

async function german(page: Page) {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
}

async function hsts(page: Page, on: boolean) {
    await page.route('**/api/system/v1/https', (r) => {
        const certificate = {self_signed: false, managed: true, mode: 'acme'};
        if (r.request().method() === 'GET') return r.fulfill({json: {redirect_https: true, hsts: on, hsts_max_age_days: 365, certificate}});
        return r.fulfill({json: {...r.request().postDataJSON(), certificate}});
    });
}

/** GET /network with the stub's answer changed by `change`. */
async function network(page: Page, change: (n: {interfaces: {name: string; ipv4?: {address: string; prefix: number}[]}[]}) => void) {
    await page.route('**/api/system/v1/network', async (r) => {
        // route.fetch runs outside the browser, where openccu.test does not resolve: the loopback
        const response = await r.fetch({url: r.request().url().replace('//openccu.test:', '//127.0.0.1:')});
        const body = await response.json();
        change(body.network);
        await r.fulfill({response, json: body});
    });
}

async function openRecovery(page: Page, title = 'Reboot into the recovery system') {
    await page.locator('.ol-powerbtn').click();
    await page.locator('.ol-powerpop [data-action="recovery"]').click();
    const dialog = page.getByRole('dialog', {name: title, exact: true});
    await expect(dialog).toBeVisible();
    return dialog;
}

test.describe('the power menu', () => {
    test('by name: the question and the page after it link to the address of the default route, the name second while HSTS is off', async ({page}) => {
        await page.goto(`${NAME}/`);
        const dialog = await openRecovery(page);
        await expect(dialog).toContainText(`It answers with a page of its own at ${ADDRESS}; choose normal boot there`);
        await expect(dialog).toContainText('The link uses the address the system has now and not its name: the recovery system has no HTTPS');
        await expect(dialog).not.toContainText('openccu.test');
        await dialog.getByRole('button', {name: 'Reboot into the recovery system', exact: true}).click();
        const state = page.locator('.ol-powerstate');
        await expect(state).toContainText('Rebooting into the recovery system…');
        await expect(state.getByRole('link', {name: ADDRESS})).toHaveAttribute('href', ADDRESS);
        await expect(state.locator('.ol-powername')).toContainText('Also by name, unless this browser still remembers HSTS for it:');
        await expect(state.locator('.ol-powername a')).toHaveAttribute('href', 'http://openccu.test/');
    });

    test('with HSTS on, only the address is shown', async ({page}) => {
        await hsts(page, true);
        await page.goto(`${NAME}/`);
        const dialog = await openRecovery(page);
        await expect(dialog).toContainText(ADDRESS);
        await dialog.getByRole('button', {name: 'Reboot into the recovery system', exact: true}).click();
        const state = page.locator('.ol-powerstate');
        await expect(state.getByRole('link', {name: ADDRESS})).toHaveAttribute('href', ADDRESS);
        await expect(state.locator('a')).toHaveCount(1);
        await expect(state.locator('.ol-powername')).toHaveCount(0);
        await expect(state).not.toContainText('openccu.test');
    });

    test('by IP address: the host the browser uses is kept, and the network is not asked', async ({page}) => {
        const asked: string[] = [];
        await page.goto('/');
        page.on('request', (r) => {
            if (new URL(r.url()).pathname === '/api/system/v1/network') asked.push(r.url());
        });
        const dialog = await openRecovery(page);
        await expect(dialog).toContainText('at http://127.0.0.1/;');
        await dialog.getByRole('button', {name: 'Reboot into the recovery system', exact: true}).click();
        const state = page.locator('.ol-powerstate');
        await expect(state.getByRole('link', {name: 'http://127.0.0.1/'})).toHaveAttribute('href', 'http://127.0.0.1/');
        await expect(state.locator('.ol-powername')).toHaveCount(0);
        expect(asked).toEqual([]);
    });

    test('a box without IPv4: the IPv6 address in brackets', async ({page}) => {
        await network(page, (n) => n.interfaces.forEach((i) => (i.ipv4 = [])));
        await page.goto(`${NAME}/`);
        const dialog = await openRecovery(page);
        await expect(dialog).toContainText('at http://[2001:db8:1::119]/;');
        await dialog.getByRole('button', {name: 'Reboot into the recovery system', exact: true}).click();
        await expect(page.locator('.ol-powerstate').getByRole('link', {name: 'http://[2001:db8:1::119]/'})).toHaveAttribute('href', 'http://[2001:db8:1::119]/');
    });

    test('no address could be read: the name, with the warning', async ({page}) => {
        await page.route('**/api/system/v1/network', (r) => r.fulfill({status: 500, json: {error: 'internal', message: 'no'}}));
        await page.goto(`${NAME}/`);
        const dialog = await openRecovery(page);
        await expect(dialog).toContainText('at http://openccu.test/;');
        await expect(dialog).toContainText('No address of the system could be read, so the link uses its name');
        await expect(dialog).toContainText('open the system by its IP address then');
        await dialog.getByRole('button', {name: 'Reboot into the recovery system', exact: true}).click();
        const state = page.locator('.ol-powerstate');
        await expect(state.getByRole('link', {name: 'http://openccu.test/'})).toHaveAttribute('href', 'http://openccu.test/');
        await expect(state).toContainText('No address of the system could be read');
        await expect(state.locator('.ol-powername')).toHaveCount(0);
    });

    test('the address is resolved when the question opens, not at page load', async ({page}) => {
        await page.goto(`${NAME}/`);
        let dialog = await openRecovery(page);
        await expect(dialog).toContainText(ADDRESS);
        await dialog.getByRole('button', {name: 'Cancel'}).click();
        await expect(page.getByRole('dialog')).toBeHidden();
        // a new lease since
        await network(page, (n) => {
            const wlan = n.interfaces.find((i) => i.name === 'wlan0')!;
            wlan.ipv4 = [{address: '192.0.2.200', prefix: 24}];
        });
        dialog = await openRecovery(page);
        await expect(dialog).toContainText('at http://192.0.2.200/;');
    });

    test('a reboot with an update set to install names the installation and the address', async ({page}) => {
        await page.route('**/api/system/v1/system-update', async (r) => {
            // route.fetch runs outside the browser, where openccu.test does not resolve: the loopback
        const response = await r.fetch({url: r.request().url().replace('//openccu.test:', '//127.0.0.1:')});
            const body = await response.json();
            body.staged = {file: 'openccu-lite-rpi4-1.0.0.zip', size: 300_000_000, kind: 'zip', recovery_armed: true};
            await r.fulfill({response, json: body});
        });
        await page.route('**/api/system/v1/reboot', (r) => r.fulfill({json: {ok: true, message: 'rebooting'}}));
        await page.goto(`${NAME}/`);
        await page.locator('.ol-powerbtn').click();
        await page.waitForLoadState('networkidle');
        await page.locator('.ol-powerpop [data-action="reboot"]').click();
        const dialog = page.getByRole('dialog', {name: 'Reboot', exact: true});
        await expect(dialog).toContainText(`A system update is set to install at this boot: it stays in the recovery system for a few minutes, and the progress of the installation is shown at ${ADDRESS}.`);
        await dialog.getByRole('button', {name: 'Reboot', exact: true}).click();
        const state = page.locator('.ol-powerstate');
        await expect(state).toContainText('A system update installs at this boot. The installation runs in the recovery system; its progress is shown at');
        await expect(state.getByRole('link', {name: ADDRESS})).toHaveAttribute('href', ADDRESS);
        await expect(state).toContainText('This page comes back by itself when the installation is done.');
    });

    test('in German', async ({page}) => {
        await german(page);
        await page.goto(`${NAME}/`);
        const dialog = await openRecovery(page, 'In das Recovery-System neu starten');
        await expect(dialog).toContainText(`Es antwortet mit einer eigenen Seite unter ${ADDRESS}; dort „normal boot“ wählen`);
        await expect(dialog).toContainText('Der Link nutzt die Adresse, die das System jetzt hat, und nicht seinen Namen');
        await expect(dialog).toContainText('Die Schnittstellen und alle Addons sind währenddessen nicht erreichbar.');
        await dialog.getByRole('button', {name: 'In das Recovery-System neu starten', exact: true}).click();
        const state = page.locator('.ol-powerstate');
        await expect(state.getByRole('link', {name: ADDRESS})).toHaveAttribute('href', ADDRESS);
        await expect(state.locator('.ol-powername')).toContainText('Auch über den Namen, sofern sich dieser Browser nicht noch HSTS dafür merkt:');
    });
});

test.describe('the system update', () => {
    /** A staged update; the install answers that the box reboots, and the health route fails from then on while `down` says so. */
    async function stagedUpdate(page: Page, down: (polls: number) => boolean) {
        let installed = false;
        let polls = 0;
        await page.route('**/api/system/v1/system-update', async (r) => {
            // route.fetch runs outside the browser, where openccu.test does not resolve: the loopback
        const response = await r.fetch({url: r.request().url().replace('//openccu.test:', '//127.0.0.1:')});
            const body = await response.json();
            body.staged = {file: 'openccu-lite-rpi4-1.0.0.zip', size: 300_000_000, kind: 'zip', recovery_armed: false};
            await r.fulfill({response, json: body});
        });
        await page.route('**/api/system/v1/system-update/install', (r) => {
            installed = true;
            return r.fulfill({json: {armed: true, rebooting: true}});
        });
        await page.route('**/api/system/v1/health', (r) => {
            if (!installed) return r.continue();
            return down(++polls) ? r.abort('connectionrefused') : r.continue();
        });
    }

    async function install(page: Page, button = 'Reboot and install') {
        await page.getByRole('button', {name: button}).click();
        const dialog = page.getByRole('dialog');
        await expect(dialog).toBeVisible();
        await page.keyboard.press('Enter');
        await expect(dialog).toBeHidden();
    }

    test('the notice links to the progress by address and says the page comes back by itself', async ({page}) => {
        await stagedUpdate(page, () => true);
        await page.goto(`${NAME}/system/updates`);
        await install(page);
        const notice = page.locator('.ol-installnotice');
        await expect(notice).toContainText('Rebooting into the recovery system…');
        await expect(notice).toContainText(`The installation runs in the recovery system; its progress is shown at ${ADDRESS}`);
        await expect(notice.getByRole('link', {name: ADDRESS})).toHaveAttribute('href', ADDRESS);
        await expect(notice).toContainText('This page comes back by itself when the installation is done.');
        await expect(notice).not.toContainText('has not come back');
        await expect(notice).not.toContainText('openccu.test');
    });

    test('a box that does not come back: after ten minutes the notice points to the recovery system', async ({page}) => {
        await page.clock.install();
        await stagedUpdate(page, () => true);
        await page.goto(`${NAME}/system/updates`);
        await install(page);
        const notice = page.locator('.ol-installnotice');
        await expect(notice).toContainText(ADDRESS);
        await page.clock.fastForward('09:00');
        await expect(notice.locator('.ol-installlate')).toHaveCount(0);
        await page.clock.fastForward('01:05');
        await expect(notice.locator('.ol-installlate')).toContainText(`The system has not come back after 10 minutes. If the installation failed, it stays in the recovery system, which shows what happened at ${ADDRESS}`);
        await expect(notice.locator('.ol-installlate a')).toHaveAttribute('href', ADDRESS);
    });

    test('a box that comes back: the page reloads by itself', async ({page}) => {
        // the first poll after the install finds the box gone, the next one back
        await stagedUpdate(page, (n) => n === 1);
        await page.goto(`${NAME}/system/updates`);
        const reloaded = page.waitForEvent('load', {timeout: 25_000});
        await install(page);
        await expect(page.locator('.ol-installnotice')).toBeVisible();
        await reloaded;
        await expect(page.locator('main h1')).toContainText('Updates');
        await expect(page.locator('.ol-installnotice')).toHaveCount(0);
    });

    test('in German', async ({page}) => {
        await german(page);
        await stagedUpdate(page, () => true);
        await page.goto(`${NAME}/system/updates`);
        await install(page, 'Neu starten und installieren');
        const notice = page.locator('.ol-installnotice');
        await expect(notice).toContainText(`Die Installation läuft im Recovery-System; ihr Fortschritt wird angezeigt unter ${ADDRESS}`);
        await expect(notice).toContainText('Diese Seite ist von selbst wieder da, wenn die Installation fertig ist.');
    });
});

test.describe('the HSTS switch', () => {
    async function switchOn(page: Page, save = 'Save') {
        await page.getByRole('checkbox', {name: /^HSTS/}).check();
        await page.locator('[data-section="https"] button.primary', {hasText: save}).click();
        const dialog = page.getByRole('dialog');
        await expect(dialog).toBeVisible();
        return dialog;
    }

    test('the question names the recovery system, the address, the lock-out and that the address always works, before anything is sent', async ({page}) => {
        await hsts(page, false);
        let puts = 0;
        page.on('request', (r) => {
            if (r.method() === 'PUT' && r.url().includes('/api/system/v1/https')) puts++;
        });
        await page.goto(`${NAME}/system/certificates`);
        const dialog = await switchOn(page);
        await expect(dialog).toContainText('Every browser that sees the header will insist on https:// for 365 days');
        await expect(dialog).toContainText(`The recovery system and the installation of a system update have no HTTPS: while a browser remembers HSTS, it reaches them only by the system's IP address, ${ADDRESS}.`);
        await expect(dialog).toContainText('The way back to OpenCCU, a reset and a switch back to a self-signed certificate serve a certificate such a browser does not trust: that locks that browser out of the name for up to 365 days, with no way past.');
        await expect(dialog).toContainText('Switching HSTS off later sends max-age=0 for a while, which undoes that only in a browser that opens the system by its name in that time');
        await expect(dialog).toContainText('The IP address always works.');
        // one paragraph each
        await expect(dialog.locator('p')).toHaveCount(4);
        expect(puts).toBe(0);
        await dialog.getByRole('button', {name: 'Cancel'}).click();
        await expect(dialog).toBeHidden();
        expect(puts).toBe(0);
        // confirmed: now it is sent
        const again = await switchOn(page);
        const [req] = await Promise.all([
            page.waitForRequest((r) => r.method() === 'PUT' && r.url().includes('/api/system/v1/https')),
            again.getByRole('button', {name: 'Switch HSTS on'}).click(),
        ]);
        expect(req.postDataJSON()).toMatchObject({hsts: true, hsts_max_age_days: 365});
    });

    test('no address could be read: the question sends the reader to the Network page', async ({page}) => {
        await hsts(page, false);
        await page.route('**/api/system/v1/network', (r) => r.fulfill({status: 500, json: {error: 'internal', message: 'no'}}));
        await page.goto(`${NAME}/system/certificates`);
        const dialog = await switchOn(page);
        await expect(dialog).toContainText("only by the system's IP address, which could not be read just now; the Network page shows it.");
        await expect(dialog).toContainText('The IP address always works.');
    });

    test('in German', async ({page}) => {
        await german(page);
        await hsts(page, false);
        await page.goto(`${NAME}/system/certificates`);
        const dialog = await switchOn(page, 'Speichern');
        await expect(dialog).toContainText(`erreicht er sie nur über die IP-Adresse des Systems, ${ADDRESS}.`);
        await expect(dialog).toContainText('das sperrt diesen Browser bis zu 365 Tage lang unter dem Namen aus');
        await expect(dialog).toContainText('Die IP-Adresse funktioniert immer.');
        await expect(dialog.getByRole('button', {name: 'HSTS einschalten'})).toBeVisible();
    });
});
