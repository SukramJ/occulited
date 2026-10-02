import {type Page, type Route} from '@playwright/test';
import {expect, test} from './fixtures';

// task 214: where the radio interfaces' history is kept - occulited's database file, the third tab
// of the Log settings. The stub is the VM: persistent by default, the file open.

const STORE = '**/api/system/v1/datastore';

async function openHistory(page: Page, url = '/system/log') {
    page.on('dialog', (dlg) => {
        throw new Error(`native dialog: ${dlg.message()}`);
    });
    await page.goto(url);
    if (!url.includes('settings=')) {
        await page.getByRole('button', {name: 'Log settings'}).click();
        await page.getByRole('dialog', {name: 'Log settings'}).getByRole('tab', {name: 'History'}).click();
    }
    await expect(page.locator('#ol-ds-mode')).toBeVisible();
    return page.locator('.ds-panel');
}

async function answer(page: Page, patch: Record<string, unknown>) {
    await page.route(STORE, async (route: Route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        await route.fulfill({response, json: {...(await response.json()), ...patch}});
    });
}

test('the modes, the figures and a save that applies at once', async ({page}) => {
    const panel = await openHistory(page);
    const mode = page.locator('#ol-ds-mode');
    await expect(mode.locator('option')).toHaveText(['Product default (Persistent)', 'RAM only', 'RAM, written to the userfs', 'Persistent on the userfs']);
    expect(await mode.locator('option').evaluateAll((os) => os.map((o) => (o as HTMLOptionElement).value))).toEqual(['', 'ram', 'ram-sync', 'persistent']);
    await expect(mode).toHaveValue('');
    await expect(panel.locator('.ds-now')).toHaveText('The history is written to the userfs as it is taken.');
    await expect(panel.locator('[data-figure="rows"]')).toHaveText('500 samples');
    await expect(panel.locator('[data-figure="size"]')).toHaveText('65.5 kB');
    // task 228's shape: a location id and a folder, the default on the userfs
    await expect(panel.locator('[data-figure="location"]')).toHaveText('userfs · etc/occulite/data');
    // task 194: the state store's datapoints; persistent writes the unchanged reports' times at the
    // interval, so it has a next write and the interval too
    await expect(panel.locator('[data-figure="state"]')).toHaveText('34 datapoints');
    await expect(panel.locator('[data-figure="next-sync"]')).toBeVisible();
    await expect(page.locator('#ol-ds-interval')).toHaveAttribute('placeholder', '1h');
    await page.locator('label[for="ol-ds-interval"]').getByRole('button').click();
    await expect(page.getByText(/the times of device values reported again unchanged are written/)).toBeVisible();
    await page.keyboard.press('Escape');
    await mode.selectOption('ram-sync');
    await expect(page.locator('#ol-ds-interval')).toHaveAttribute('placeholder', '1h');
    await page.locator('#ol-ds-interval').fill('6h');
    let body: unknown;
    await page.route(STORE, async (route) => {
        if (route.request().method() === 'PUT') body = route.request().postDataJSON();
        await route.fallback();
    });
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect(panel.getByText('Saved and applied.')).toBeVisible();
    expect(body).toEqual({mode: 'ram-sync', sync_interval: '6h', location: ''});
    await expect(panel.locator('.ds-now')).toHaveText('The history is in RAM now and written to the userfs at the interval.');
    await expect(panel.locator('[data-figure="next-sync"]')).toBeVisible();
    // RAM only: no file, no dates
    await mode.selectOption('ram');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect(panel.locator('.ds-now')).toHaveText('The history is in RAM now.');
    await expect(panel.locator('[data-figure="size"]')).toHaveText('—');
    await expect(panel.locator('[data-figure="last-sync"]')).toHaveCount(0);
    await expect(panel.locator('[data-figure="next-sync"]')).toHaveCount(0);
    await expect(page.locator('#ol-ds-interval')).toHaveCount(0);
});

test('a refused interval is said, and an unsaved change is asked about', async ({page}) => {
    const panel = await openHistory(page);
    await page.locator('#ol-ds-mode').selectOption('ram-sync');
    await page.locator('#ol-ds-interval').fill('5min');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect(page.locator('.ls-panel .ol-warn')).toHaveText('the write interval is 15min to 1d');
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog').last()).toContainText(/unsaved|not saved/i);
});

test('a card system, a file that could not be opened, and the link to the tab', async ({page}) => {
    await answer(page, {platform: 'rpi4', default_mode: 'ram-sync', effective: 'ram-sync', open: false, size: 0, error: 'open /usr/local/etc/occulite/data/occulited.db: read-only file system'});
    const panel = await openHistory(page, '/system/log?settings=history');
    await expect(page.locator('#ol-ds-mode option').first()).toHaveText('Product default (RAM, written to the userfs)');
    await expect(panel.locator('.ds-now')).toHaveText('The history is in RAM now.');
    await expect(panel.locator('.ds-error')).toHaveText('The database could not be opened, the history is in RAM: open /usr/local/etc/occulite/data/occulited.db: read-only file system');
    await expect(page.locator('#ol-ds-interval')).toHaveAttribute('placeholder', '1h');
});

// task 195: the history's list - added and removed names, applied at once; a bad name is said
test('the recorded datapoints', async ({page}) => {
    await openHistory(page, '/system/log?settings=history');
    const list = page.locator('.ds-history');
    await expect(list.locator('[data-figure="series"]')).toHaveText('7');
    await expect(list.locator('[data-figure="datapoints"]')).toHaveText('ACTUAL_TEMPERATURE, HUMIDITY, LEVEL, MOTION, PRESS_SHORT, STATE');
    let body: unknown;
    await page.route('**/api/system/v1/datastore/history', async (route) => {
        if (route.request().method() === 'PUT') body = route.request().postDataJSON();
        await route.fallback();
    });
    await page.locator('#ol-hist-add').fill('door_state, CARBON_DIOXIDE_CONCENTRATION');
    await page.locator('#ol-hist-remove').fill('PRESS_SHORT');
    await list.getByRole('button', {name: 'Save'}).click();
    await expect(list.getByText('Saved and applied.')).toBeVisible();
    expect(body).toEqual({add: ['DOOR_STATE', 'CARBON_DIOXIDE_CONCENTRATION'], remove: ['PRESS_SHORT']});
    await expect(list.locator('[data-figure="datapoints"]')).toHaveText('ACTUAL_TEMPERATURE, CARBON_DIOXIDE_CONCENTRATION, DOOR_STATE, HUMIDITY, LEVEL, MOTION, STATE');
    await page.locator('#ol-hist-add').fill('bad-name');
    await list.getByRole('button', {name: 'Save'}).click();
    await expect(list.locator('.ds-error')).toHaveText('"BAD-NAME" is not a datapoint name (A-Z, 0-9, _)');
});

test('German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    const panel = await openHistory(page, '/system/log?settings=history');
    await expect(page.getByRole('tab', {name: 'Verlauf'})).toHaveAttribute('aria-selected', 'true');
    await expect(panel.locator('.ds-now')).toHaveText('Der Verlauf wird beim Erfassen auf das Userfs geschrieben.');
    await expect(page.locator('#ol-ds-mode option').nth(2)).toHaveText('RAM, auf das Userfs geschrieben');
    await expect(panel.locator('[data-figure="state"]')).toHaveText('34 Datenpunkte');
    await expect(panel.getByText('Gerätewerte')).toBeVisible();
    await expect(page.getByRole('heading', {name: 'Aufgezeichnete Datenpunkte'})).toBeVisible();
});

// task 228, phase 4: where the file is, with the location picker - the system storage only, in a
// folder inside etc/occulite; a share never, said why (a stick takes a copy: task 229, below)
test('where the file is: the location picker, local only', async ({page}) => {
    const bodies: Record<string, unknown>[] = [];
    await page.route(STORE, async (route: Route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        const body = route.request().postDataJSON() as Record<string, unknown>;
        bodies.push(body);
        const response = await route.fetch();
        await route.fulfill({response});
    });
    const panel = await openHistory(page);
    const where = panel.locator('[data-store-location]');
    await expect(where.locator('[data-location-button]')).toContainText('System storage (userfs)');
    await expect(where.locator('[data-location-folder]')).toHaveValue('etc/occulite/data');
    await where.locator('[data-location-button]').click();
    await expect(where.locator('[data-location="usb:BACKUP"]')).toContainText('Takes a copy only: the database stays on the system storage and a copy goes to the stick at every write.');
    await expect(where.locator('[data-location="share:nas"]')).toHaveAttribute('aria-disabled', 'true');
    await expect(where.locator('[data-location="share:nas"] [data-why]')).toContainText('Never on a network share: the database maps its file into memory');
    await where.locator('[data-location="share:nas"]').click({force: true});
    await expect(where.locator('[data-location-button]')).toContainText('System storage (userfs)');
    await page.keyboard.press('Escape');
    await expect(where.locator('[data-location-list]')).toHaveCount(0);
    // a folder outside etc/occulite: said, and Save waits
    await where.locator('[data-location-folder]').fill('var/db');
    await expect(where.locator('[data-folder-problem]')).toHaveText('On the system storage the folder is etc/occulite or below it.');
    await expect(panel.getByRole('button', {name: 'Save'})).toBeDisabled();
    await where.locator('[data-location-folder]').fill('etc/occulite/db2');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect.poll(() => bodies.length).toBe(1);
    expect(bodies[0]).toMatchObject({location: 'userfs:etc/occulite/db2'});
    // back to the default folder: the empty setting, as before
    await where.locator('[data-location-folder]').fill('etc/occulite/data');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect.poll(() => bodies.length).toBe(2);
    expect(bodies[1]).toMatchObject({location: ''});
});

// task 229: a USB stick takes a copy of ram-sync's file - choosing it switches the mode to ram-sync
// (the others are not offered), the answer shows the last copy, and a stick that is not plugged in
// is said
test('a USB stick takes a copy: ram-sync only, the last copy, a missing stick', async ({page}) => {
    const bodies: Record<string, unknown>[] = [];
    await page.route(STORE, async (route: Route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        bodies.push(route.request().postDataJSON() as Record<string, unknown>);
        await route.fallback();
    });
    const panel = await openHistory(page);
    const where = panel.locator('[data-store-location]');
    await where.locator('[data-location-button]').click();
    await where.locator('[data-location="usb:BACKUP"]').click();
    await expect(where.locator('[data-location-folder]')).toHaveValue('occulited');
    await expect(page.locator('#ol-ds-mode')).toHaveValue('ram-sync');
    await expect(page.locator('#ol-ds-mode option[value="persistent"]')).toHaveAttribute('disabled', '');
    await expect(page.locator('#ol-ds-mode option[value="ram"]')).toHaveAttribute('disabled', '');
    await expect(page.locator('#ol-ds-mode option[value=""]')).toHaveAttribute('disabled', '');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect.poll(() => bodies.length).toBe(1);
    expect(bodies[0]).toMatchObject({mode: 'ram-sync', location: 'usb:BACKUP/occulited'});
    await expect(panel.locator('[data-store-copy] dt')).toHaveText('Copy on the stick BACKUP');
    await expect(panel.locator('[data-figure="last-copy"]')).not.toHaveText('none yet');
    await expect(panel.locator('[data-copy-missing]')).toHaveCount(0);
});

test('the stick of the copy is not plugged in, and a copy that failed', async ({page}) => {
    await answer(page, {mode: 'ram-sync', effective: 'ram-sync', location: 'usb:LOGSTICK/occulited', copy: {label: 'LOGSTICK', folder: 'occulited', present: false, last_copy: null, last_error: 'the stick is read-only'}});
    const panel = await openHistory(page);
    await expect(panel.locator('[data-copy-missing]')).toHaveText('The USB stick LOGSTICK is not plugged in: the history stays on the userfs, and the copy goes to the stick once it is back.');
    await expect(panel.locator('[data-copy-error]')).toHaveText('The last copy to the USB stick failed: the stick is read-only');
    await expect(panel.locator('[data-figure="last-copy"]')).toHaveText('none yet');
});
