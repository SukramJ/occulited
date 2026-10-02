import {type Locator} from '@playwright/test';
import {expect, test} from './fixtures';
import {PORT, fitsWindow} from './scroll';

// The Firmware page: a bundle whose info says eQ-3's 0.0.0 (the API sends an empty version) shows
// a dash, and a bundle's text files open in the shell's modal - the file name as its title, the
// text in a monospaced block - closing with × and with Escape.

function bundles(page: import('@playwright/test').Page): Locator {
    return page.locator('table.ol-bundles tbody tr');
}

/** The relative luminance of a computed CSS colour, 0 black to 1 white. */
function luminance(css: string): number {
    const [r, g, b] = (css.match(/[\d.]+/g) ?? ['0', '0', '0']).slice(0, 3).map(Number);
    return (0.2126 * r! + 0.7152 * g! + 0.0722 * b!) / 255;
}

test("an unstated version is the file name's, muted and marked; a dash when the name has none; a real one is shown", async ({page}) => {
    await page.goto('/system/updates');
    const drs8 = bundles(page).filter({hasText: 'HmIPW-DRS8'});
    const pdt = bundles(page).filter({hasText: 'HmIP-PDT'});
    const xyz = bundles(page).filter({hasText: 'HmIP-XYZ'});
    const fromName = drs8.locator('td').nth(2).locator('.ol-fromname');
    await expect(drs8.locator('td').nth(2)).toHaveText('1.2.6 (2022-09-28) from the file name');
    await expect(fromName).toHaveClass(/ol-muted/);
    await expect(fromName.locator('em')).toHaveText('from the file name');
    await expect(fromName).toHaveAttribute('title', 'The bundle states no version; this one is read from the name of its update file.');
    await expect(xyz.locator('td').nth(2)).toHaveText('–');
    await expect(pdt.locator('td').nth(2)).toHaveText('2.2.4');
    await expect(pdt.locator('.ol-fromname')).toHaveCount(0);
    const results = page.locator('table').filter({hasText: 'Result'});
    await expect(results.locator('tr', {hasText: 'HmIPW-DRS8'}).locator('td').nth(1)).toHaveText('1.2.6 (2022-09-28) from the file name');
    await expect(results.locator('tr', {hasText: 'HmIP-XYZ'}).locator('td').nth(1)).toHaveText('–');
    await expect(page.locator('body')).not.toContainText('0.0.0');
});

test('changelog.txt and info are links, the firmware image is not', async ({page}) => {
    await page.goto('/system/updates');
    const drs8 = bundles(page).filter({hasText: 'HmIPW-DRS8'});
    await expect(drs8.getByRole('button', {name: 'changelog.txt'})).toBeVisible();
    // task 246: the texts are icon buttons of their own column; the file column is the firmware alone
    await expect(drs8.locator('td.ol-files')).toHaveText('HmIPW-DRS8_update_V1_2_6_220928.efw');
    await expect(drs8.getByRole('button', {name: 'info', exact: true})).toBeVisible();
    await expect(drs8).toContainText('HmIPW-DRS8_update_V1_2_6_220928.efw');
    await expect(drs8.getByRole('button', {name: /\.efw$/})).toHaveCount(0);
});

test('a text file opens in the modal and closes with × and with Escape', async ({page}, info) => {
    await page.goto('/system/updates');
    const drs8 = bundles(page).filter({hasText: 'HmIPW-DRS8'});
    const opener = drs8.getByRole('button', {name: 'changelog.txt'});
    await opener.click();
    const dialog = page.getByRole('dialog', {name: 'changelog.txt'});
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('.ol-modal-sub')).toHaveText('HmIPW-DRS8');
    const text = dialog.locator('pre');
    await expect(text).toContainText('Version 1.2.6 (2022-09-28)');
    const style = await text.evaluate((e) => ({font: getComputedStyle(e).fontFamily, ws: getComputedStyle(e).whiteSpace}));
    expect(style.font).toMatch(/mono/i);
    expect(style.ws).toBe('pre-wrap');
    // the page behind does not scroll while the dialog is open
    expect(await page.evaluate((sel) => document.querySelector<HTMLElement>(sel)!.style.overflow, PORT)).toBe('hidden');
    // the dialog's surface follows the theme
    const bg = luminance(await dialog.evaluate((e) => getComputedStyle(e).backgroundColor));
    if (info.project.name.includes('dark')) expect(bg).toBeLessThan(0.4);
    else expect(bg).toBeGreaterThan(0.6);
    // the long line wraps inside the dialog: nothing runs off the viewport, not on a phone either
    const box = (await dialog.boundingBox())!;
    const viewport = page.viewportSize()!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(viewport.width);
    expect(await text.evaluate((e) => e.scrollWidth <= e.clientWidth + 1)).toBe(true);

    await dialog.getByRole('button', {name: 'Close'}).click();
    await expect(dialog).toBeHidden();
    await expect(opener).toBeFocused();

    await drs8.getByRole('button', {name: 'info', exact: true}).click();
    const infoDialog = page.getByRole('dialog', {name: 'info'});
    await expect(infoDialog.locator('pre')).toContainText('FirmwareVersion=0.0.0');
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();
    expect(await page.evaluate((sel) => document.querySelector<HTMLElement>(sel)!.style.overflow, PORT)).toBe('');
});

test('a refused file shows the answer of the box', async ({page}) => {
    await page.route('**/api/system/v1/firmware/bundles/310/files/changelog.txt', (r) =>
        r.fulfill({status: 413, json: {error: 'too-large', message: 'the file is 300000 bytes, more than the 262144 bytes shown here', detail: {size: 300000, limit: 262144}}}),
    );
    await page.goto('/system/updates');
    await bundles(page).filter({hasText: 'HmIP-PDT'}).getByRole('button', {name: 'changelog.txt'}).click();
    const dialog = page.getByRole('dialog', {name: 'changelog.txt'});
    await expect(dialog).toContainText('the file is 300000 bytes');
    await expect(dialog.locator('pre')).toHaveCount(0);
});

test('the page does not scroll sideways', async ({page}) => {
    await page.goto('/system/updates');
    await expect(bundles(page)).toHaveCount(3);
    expect(await fitsWindow(page)).toBe(true);
});

// B-195: eQ-3's version per paired device - marked when newer than what the device runs, muted when
// current, and "not in eQ-3's list" for a type the index does not name.
test("the device table shows eQ-3's latest version per device", async ({page}) => {
    await page.goto('/system/updates');
    const table = page.locator('table').filter({hasText: 'Latest from eQ-3'});
    const cell = (hasText: string) => table.locator('tbody tr', {hasText}).locator('td.ol-fw-latest');
    await expect(cell('HMIP-WRC2')).toHaveText('1.18.2');
    await expect(cell('HMIP-WRC2').locator('.ol-fw-newer')).toHaveCount(1);
    await expect(cell('HmIP-PDT')).toHaveText('2.2.4');
    await expect(cell('HmIP-PDT').locator('.ol-muted')).toHaveCount(1);
    await expect(cell('HM-CC-TC')).toHaveText("not in eQ-3's list");
    // the interface's 0.0.0 is not an update
    await expect(table.locator('tbody tr', {hasText: 'HMIP-WRC2'}).locator('td').nth(4).locator('.ol-fw-newer')).toHaveCount(0);
    expect(await fitsWindow(page)).toBe(true);
});

// B-225: a device the interface calls not updatable (rfd's UPDATABLE 0) says so instead of "not in
// eQ-3's list"; a second bundle of a shared TypeCode lies in <TypeCode>-<Name>, and its files are
// read from that directory.
test('not updatable over the air; a bundle in <TypeCode>-<Name>', async ({page}) => {
    await page.route('**/api/system/v1/firmware', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const st = await response.json();
        st.devices = st.devices.map((d: {type: string}) => (d.type === 'HM-CC-TC' ? {...d, not_listed: false, updatable: false} : d));
        st.deployed = [...st.deployed, {type_code: '261', dir: '261-HM-LC-Dim1T-DR', name: 'HM-LC-Dim1T-DR', version: '1.1.0', files: ['HM-LC-Dim1T-DR_update_V1_1_0_171122.eq3', 'changelog.txt', 'info'], info: {TypeCode: '261', Name: 'HM-LC-Dim1T-DR', FirmwareVersion: '1.1.0'}}];
        await route.fulfill({response, json: st});
    });
    let asked = '';
    await page.route('**/api/system/v1/firmware/bundles/*/files/*', (route) => {
        asked = new URL(route.request().url()).pathname;
        return route.fulfill({contentType: 'text/plain', body: 'V1.1.0\n- dimmer'});
    });
    await page.goto('/system/updates');
    const table = page.locator('table').filter({hasText: 'Latest from eQ-3'});
    await expect(table.locator('tbody tr', {hasText: 'HM-CC-TC'}).locator('td.ol-fw-latest')).toHaveText('not updatable over the air');
    await bundles(page).filter({hasText: 'HM-LC-Dim1T-DR'}).getByRole('button', {name: 'changelog.txt'}).click();
    await expect(page.getByRole('dialog', {name: 'changelog.txt'})).toContainText('dimmer');
    expect(asked).toBe('/api/system/v1/firmware/bundles/261-HM-LC-Dim1T-DR/files/changelog.txt');
});

test('not updatable over the air, in German', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.route('**/api/system/v1/firmware', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const st = await response.json();
        st.devices = st.devices.map((d: {type: string}) => (d.type === 'HM-CC-TC' ? {...d, not_listed: false, updatable: false} : d));
        await route.fulfill({response, json: st});
    });
    await page.goto('/system/updates');
    await expect(page.locator('[data-not-updatable]')).toHaveText('nicht über Funk aktualisierbar');
});

// task 246 (the maintainer): the table says what it is (the path behind its ?), changelog and info
// are icon buttons left of the firmware file - only where the bundle has them - the file column
// lists the firmware alone, each row names the interface that reads it, and a filter narrows it.
test('deployed device firmware: heading, icon buttons, interface, filter', async ({page}) => {
    await page.goto('/system/updates');
    const h = page.getByRole('heading', {name: 'Deployed device firmware'});
    await expect(h).toBeVisible();
    await h.getByRole('button', {name: 'Help'}).click();
    await expect(h.locator('.ol-help-pop')).toContainText('read it from /etc/config/firmware; BidCos-Wired devices get firmware only with the system image.');
    const pdt = bundles(page).filter({hasText: 'HmIP-PDT'});
    const icons = pdt.locator('td.fw-icons');
    await expect(icons.getByRole('button')).toHaveCount(2);
    await expect(icons.getByRole('button', {name: 'changelog.txt'})).toBeVisible();
    await expect(icons.getByRole('button', {name: 'info', exact: true})).toBeVisible();
    // the icons stand left of the firmware file, and the file column holds the firmware alone
    const ib = (await icons.boundingBox())!;
    const fileCell = pdt.locator('td.ol-files');
    const fb = (await fileCell.boundingBox())!;
    // on a phone the stacked row wraps: the file then follows on a line below
    expect(fb.x > ib.x + ib.width - 1 || fb.y >= ib.y + ib.height - 1).toBe(true);
    await expect(fileCell).toHaveText('HmIP-PDT_update_V2_2_4_231123.efw');
    await expect(pdt).toContainText('HmIP-RF');
    // a bundle without a changelog: only its info icon
    const xyz = bundles(page).filter({hasText: 'HmIP-XYZ'});
    await expect(xyz.locator('td.fw-icons').getByRole('button')).toHaveCount(1);
    await expect(xyz.locator('td.fw-icons').getByRole('button', {name: 'info', exact: true})).toBeVisible();
    // the viewer from the icon
    await icons.getByRole('button', {name: 'changelog.txt'}).click();
    await expect(page.getByRole('dialog', {name: 'changelog.txt'})).toContainText('improved the battery reading');
    await page.keyboard.press('Escape');
    // the filter: by type, by version, by file name; the count
    const filter = page.getByRole('textbox', {name: 'Filter the deployed firmware'});
    await expect(page.locator('[data-bundle-count]')).toHaveText('3 of 3');
    await filter.fill('drs8');
    await expect(bundles(page)).toHaveCount(1);
    await expect(page.locator('[data-bundle-count]')).toHaveText('1 of 3');
    await filter.fill('2.2.4');
    await expect(bundles(page)).toHaveCount(1);
    await filter.fill('firmware.efw');
    await expect(bundles(page).filter({hasText: 'HmIP-XYZ'})).toHaveCount(1);
    await filter.fill('nothing like this');
    await expect(bundles(page)).toHaveCount(0);
    await expect(page.locator('[data-bundles-none]')).toBeVisible();
    await filter.fill('');
    await expect(bundles(page)).toHaveCount(3);
    expect(await fitsWindow(page)).toBe(true);
});

test('deployed device firmware in German, a BidCos bundle read by rfd', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.route('**/api/system/v1/firmware', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        const st = await response.json();
        st.deployed = [...st.deployed, {type_code: '149', dir: '149', name: 'HM-CC-RT-DN', version: '1.5.3', files: ['HM-CC-RT-DN_update_V1_5_3.eq3', 'info'], info: {}}];
        await route.fulfill({response, json: st});
    });
    await page.goto('/system/updates');
    await expect(page.getByRole('heading', {name: 'Bereitgestellte Gerätefirmware'})).toBeVisible();
    await expect(bundles(page).filter({hasText: 'HM-CC-RT-DN'})).toContainText('BidCos-RF');
    await expect(page.locator('[data-bundle-count]')).toHaveText('4 von 4');
});
