import {expect, test, type Locator, type TestInfo} from '@playwright/test';
import {fitsWindow} from './scroll';

// A plain click in every project. While the Installed addons table was wider than a phone (B-105)
// the phone's hit test landed on a neighbouring cell and the click was dispatched on the element.
async function press(el: Locator, _info: TestInfo): Promise<void> {
    await el.click();
}

// task 56: the update on the Installed addons page - old → new from the catalogue without a
// click, the Update button running the catalogue's install with its progress on this page (and
// on the Catalogue), the list reloaded afterwards; an addon the catalogue does not know keeps
// "Check for update" and gets a Download link when its check names one.

test('a catalogue update shows old → new and installs with the progress on the page', async ({page, context, baseURL}, info) => {
    // the install state of this browser only: the projects share the stub
    await context.addCookies([{name: 'stub-session', value: `update-${info.project.name}-${Date.now()}`, url: baseURL!}]);
    const posts: string[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().includes('/catalog/')) posts.push(new URL(r.url()).pathname);
    });
    await page.goto('/addons');
    const row = page.locator('#addon-row-redmatic');
    const version = row.locator('.ad-sub');
    await expect(version).toContainText(/9\.4\.0\s*→\s*9\.4\.1/);
    // the catalogue knows RedMatic's newest release: no "Check for update" beside the Update
    await expect(row.getByRole('button', {name: 'Check for update'})).toHaveCount(0);
    await press(row.locator('[data-addon-update]'), info);
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Update RedMatic from 9.4.0 to 9.4.1?');
    await press(dialog.getByRole('button', {name: 'Update', exact: true}), info);
    await expect.poll(() => posts).toEqual(['/api/system/v1/catalog/redmatic/install']);
    const progress = page.locator('.ol-install-progress');
    await expect(progress).toContainText('redmatic');

    // another window of the page shows the same run
    const other = await context.newPage();
    await other.goto('/addons');
    await expect(other.locator('.ol-install-progress')).toContainText('redmatic');
    await other.close();

    await expect(progress).toHaveAttribute('data-phase', 'done', {timeout: 15_000});
    // the list reloaded: the new version, and the button is gone
    await expect(version).toContainText('9.4.1');
    await expect(version).not.toContainText('→');
    await expect(row.locator('[data-addon-update]')).toHaveCount(0);
});

// B-98, decided in D-67: an install that asks for a reboot or fails starts nothing, and an addon that ran
// before and is stopped now is the user's question - Start is the normal unit start, Leave stopped leaves it
test('a catalogue update that asks for a reboot asks whether to start the addon again', async ({page, context, baseURL}, info) => {
    await context.addCookies([
        {name: 'stub-session', value: `stopped-${info.project.name}-${Date.now()}`, url: baseURL!},
        {name: 'stub-install-stopped', value: 'reboot', url: baseURL!},
    ]);
    const started: string[] = [];
    await page.route('**/api/system/v1/services/*/start', (route) => {
        started.push(new URL(route.request().url()).pathname);
        return route.fulfill({json: {output: ''}});
    });
    await page.goto('/addons');
    const row = page.locator('#addon-row-redmatic');
    await press(row.locator('[data-addon-update]'), info);
    await press(page.getByRole('dialog').getByRole('button', {name: 'Update', exact: true}), info);
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('RedMatic was running before the update. Start it again?', {timeout: 15_000});
    // enabled, and the box says the boot starts it: the question says so
    await expect(dialog).toContainText('It is enabled, so the reboot the installation asks for starts it anyway.');
    await expect(dialog.getByRole('button', {name: 'Leave stopped'})).toBeVisible();
    await press(dialog.getByRole('button', {name: 'Start', exact: true}), info);
    await expect(page.getByRole('dialog')).toHaveCount(0);
    expect(started).toEqual(['/api/system/v1/services/addon-redmatic/start']);
    expect(await fitsWindow(page)).toBe(true);
});

test('an uploaded install that failed asks, and Leave stopped starts nothing', async ({page, context, baseURL}, info) => {
    await context.addCookies([{name: 'stub-install-stopped', value: 'failed', url: baseURL!}]);
    const started: string[] = [];
    await page.route('**/api/system/v1/services/*/start', (route) => {
        started.push(new URL(route.request().url()).pathname);
        return route.fulfill({json: {output: ''}});
    });
    await page.goto('/addons');
    await page.locator('input[type="file"]').setInputFiles({name: 'mosquitto.tar.gz', mimeType: 'application/gzip', buffer: Buffer.from('x')});
    await press(page.locator('form').getByRole('button', {name: 'Install', exact: true}), info);
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Mosquitto was running before the update. Start it again?');
    // no reboot asked for: nothing about the boot
    await expect(dialog).not.toContainText('reboot');
    await press(dialog.getByRole('button', {name: 'Leave stopped'}), info);
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.locator('.ol-notice.error')).toContainText('exit 1');
    expect(started).toEqual([]);
});

test('in German: a reboot whose boot would not start the addon', async ({page, context, baseURL}, info) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await context.addCookies([{name: 'stub-install-stopped', value: 'reboot', url: baseURL!}]);
    await page.goto('/addons');
    await page.locator('input[type="file"]').setInputFiles({name: 'mosquitto.tar.gz', mimeType: 'application/gzip', buffer: Buffer.from('x')});
    await press(page.locator('form').getByRole('button', {name: 'Installieren', exact: true}), info);
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Mosquitto lief vor dem Update. Wieder starten?');
    // switched off on this system: the boot does not start it, so the question does not claim it
    await expect(dialog).not.toContainText('ohnehin');
    await expect(dialog.getByRole('button', {name: 'Starten', exact: true})).toBeVisible();
    await press(dialog.getByRole('button', {name: 'Gestoppt lassen'}), info);
    await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('an addon the catalogue does not know keeps its own check and gets a Download link', async ({page}, info) => {
    await page.goto('/addons');
    const row = page.locator('#addon-row-hm2mqtt');
    await press(row.getByRole('button', {name: 'Check for update'}), info);
    await expect(row.locator('.ad-check')).toContainText('→ 3.6.1');
    const download = row.getByRole('link', {name: 'Download'});
    await expect(download).toHaveAttribute('href', 'https://github.com/hobbyquaker/hm2mqtt.js/releases/tag/v3.6.1');
    await expect(download).toHaveAttribute('target', '_blank');
    // a catalogue addon that is current has neither the check nor an Update
    const mosquitto = page.locator('#addon-row-mosquitto');
    await expect(mosquitto.getByRole('button', {name: 'Check for update'})).toHaveCount(0);
    await expect(mosquitto.locator('[data-addon-update]')).toHaveCount(0);
});

// B-4: an uploaded archive is a job the page follows; a poll the web server's reload cuts is
// asked again, and the result arrives when the job ends
test('an uploaded install follows its job through a cut poll', async ({page}, info) => {
    let polls = 0;
    await page.route('**/api/system/v1/addons/install?job=*', (route) => {
        polls += 1;
        return polls === 1 ? route.abort('connectionreset') : route.fallback();
    });
    await page.goto('/addons');
    await page.locator('input[type="file"]').setInputFiles({name: 'mosquitto.tar.gz', mimeType: 'application/gzip', buffer: Buffer.from('x')});
    await press(page.locator('form').getByRole('button', {name: 'Install', exact: true}), info);
    await expect(page.locator('.ol-notice').filter({hasText: 'Install result'})).toContainText('installed');
    expect(polls).toBeGreaterThanOrEqual(3);
    await expect(page.locator('form').getByRole('button', {name: 'Install', exact: true})).toBeVisible();
});
