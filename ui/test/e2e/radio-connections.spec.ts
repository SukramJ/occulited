import {expect, test, type Page} from '@playwright/test';

// task 129 phase 3 (D-81, D-82, D-98): the connection of each interface process on the Interfaces
// page - what runs on what, the choices the detected hardware allows, the preview in the shell's
// dialog, the paired-BidCos warning before BidCos-RF loses its local radio, and the change itself.
// task 150: the panels on the modules' track with the dropdowns at their foot, "Automatic: …" as
// what each uses now, one HmIP entry per path to a module, multimacd below the two it serves.

async function ownConn(page: Page, baseURL: string | undefined) {
    await page.context().addCookies([{name: 'stub-conn', value: `conn-${Math.random().toString(36).slice(2)}`, url: baseURL!}]);
}

test('the panels say what each process runs on, and the choices are the detected modules and paths', async ({page, baseURL}) => {
    await ownConn(page, baseURL);
    await page.goto('/radio');
    await expect(page.locator('h2#connections')).toHaveText('Connections');
    const hmip = page.locator('[data-process="hmipserver"]');
    const rfd = page.locator('[data-process="rfd"]');
    const mmd = page.locator('[data-process="multimacd"]');
    await expect(hmip.locator('.conn-now')).toHaveText('Automatic: HMIP-RFUSB 0000000A02, shared with BidCos-RF through multimacd');
    await expect(hmip).toContainText('HmIP-HAPs and DRAPs can route through this module.');
    await expect(rfd.locator('.conn-now')).toHaveText('Automatic: HMIP-RFUSB 0000000A02 through multimacd + LAN gateways');
    // multimacd: the nodes it provides on top, the one it opens at the foot
    await expect(mmd.locator('.conn-mmd-provides dt')).toHaveText(['/dev/mmd_bidcos', '/dev/mmd_hmip']);
    await expect(mmd.locator('.conn-mmd-provides dd')).toHaveText(['↑ rfd (BidCos-RF)', '↑ hmipserver (HmIP-RF)']);
    await expect(mmd.locator('.conn-mmd-uses')).toHaveText('connected to /dev/raw-uart · HMIP-RFUSB 0000000A02');
    // HmIP is never offered as off (D-98); one entry per path; BidCos-RF has "none"
    await expect(page.getByLabel('Module for HmIP-RF').locator('option')).toHaveText(['Automatic', 'HMIP-RFUSB · 0000000A02 · /dev/raw-uart', 'HMIP-RFUSB · 0000000A02 · /dev/mmd_hmip through multimacd on /dev/raw-uart']);
    await expect(page.getByLabel('Module for BidCos-RF').locator('option')).toHaveText(['Automatic', 'HMIP-RFUSB · 0000000A02 · /dev/mmd_bidcos through multimacd on /dev/raw-uart', 'No local radio (LAN gateways only)']);
    await expect(page.getByRole('button', {name: 'Apply changes'})).toBeDisabled();
    // the module card names its USB device
    await expect(page.locator('.ol-module-usb').first()).toHaveText('1b1f:c020 · eQ-3 HmIP-RFUSB (Silicon Labs)');
});

test('the layout: two panels side by side, multimacd spanning them below, the dropdowns in line', async ({page, baseURL}) => {
    await ownConn(page, baseURL);
    await page.setViewportSize({width: 1280, height: 900});
    await page.goto('/radio');
    const box = async (sel: string) => (await page.locator(sel).boundingBox())!;
    const h = await box('[data-process="hmipserver"]');
    const r = await box('[data-process="rfd"]');
    const m = await box('[data-process="multimacd"]');
    // three would fit in a row: each at least 340 px
    expect(h.width).toBeGreaterThanOrEqual(340);
    expect(Math.abs(h.y - r.y)).toBeLessThan(1);
    // each panel under its module card: BidCos-RF left, as the stub's modules are ordered
    expect(h.x).toBeGreaterThan(r.x + r.width);
    const mb = await box('#ol-module-BidCos-RF');
    const mh = await box('#ol-module-HmIP-RF');
    expect(Math.abs(mb.x - r.x)).toBeLessThan(1);
    expect(Math.abs(mh.x - h.x)).toBeLessThan(1);
    expect(m.y).toBeGreaterThan(h.y + h.height);
    expect(Math.abs(m.x - r.x)).toBeLessThan(1);
    expect(Math.abs(m.x + m.width - (h.x + h.width))).toBeLessThan(1);
    // the dropdowns at the panels' feet, on one line
    const s1 = (await page.getByLabel('Module for HmIP-RF').boundingBox())!;
    const s2 = (await page.getByLabel('Module for BidCos-RF').boundingBox())!;
    expect(Math.abs(s1.y - s2.y)).toBeLessThan(1);
    // the button keeps its distance from the panels
    const b = (await page.getByRole('button', {name: 'Apply changes'}).boundingBox())!;
    expect(b.y - (m.y + m.height)).toBeGreaterThanOrEqual(12);
    // at phone width, one column, in the same order
    await page.setViewportSize({width: 390, height: 844});
    const h2 = await box('[data-process="hmipserver"]');
    const r2 = await box('[data-process="rfd"]');
    expect(h2.y).toBeGreaterThan(r2.y + r2.height);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test('HmIP-direct: the dialog lists the paired BidCos devices, then the change runs', async ({page, baseURL}) => {
    await ownConn(page, baseURL);
    const puts: string[] = [];
    page.on('request', (r) => {
        if (r.method() === 'PUT' && r.url().endsWith('/radio/connections')) puts.push(r.postData() ?? '');
    });
    await page.goto('/radio');
    // the LAN gateway shortcut picks "none"
    await page.locator('[data-process="rfd"]').getByRole('button', {name: 'Use the LAN gateways only'}).click();
    await expect(page.getByLabel('Module for BidCos-RF')).toHaveValue('none');
    await page.getByRole('button', {name: 'Apply changes'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Change the radio connections?');
    await expect(dialog).toContainText('HmIP-RF: Automatic');
    await expect(dialog).toContainText('BidCos-RF: No local radio (LAN gateways only)');
    await expect(dialog).toContainText('hmipserver: HMIP-RFUSB 0000000A02, directly on /dev/raw-uart');
    await expect(dialog).toContainText('multimacd: not needed');
    await expect(dialog).toContainText('multimacd, rfd, hmipserver are stopped and started again');
    await expect(dialog).toContainText('BidCos-RF loses its local radio.');
    await expect(dialog).toContainText('Paired BidCos-RF devices (2):');
    await expect(dialog).toContainText('JEQ9000001 · HM-CC-TC');
    // a warning: the cancel button has the focus, cancel sends nothing
    await expect(dialog.getByRole('button', {name: 'Cancel'})).toBeFocused();
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    expect(puts).toHaveLength(0);
    await page.getByRole('button', {name: 'Apply changes'}).click();
    await dialog.getByRole('button', {name: 'Change'}).click();
    await expect(dialog).toBeHidden();
    expect(puts).toHaveLength(1);
    expect(JSON.parse(puts[0]!)).toEqual({hmip: '', hmip_path: '', bidcos: 'none', confirm: true});
    await expect(page.locator('[data-process="hmipserver"] .conn-now')).toHaveText('Automatic: HMIP-RFUSB 0000000A02, directly on /dev/raw-uart');
    await expect(page.locator('[data-process="rfd"] .conn-now')).toHaveText('Chosen: LAN gateways');
    await expect(page.locator('[data-process="multimacd"] .conn-now')).toHaveText('not needed');
    await expect(page.locator('.conn-last summary')).toContainText('succeeded');
    await expect(page.getByRole('button', {name: 'Apply changes'})).toBeDisabled();
});

test('a box whose module cards list HmIP-RF first puts the HmIP-RF panel first', async ({page, baseURL}) => {
    await ownConn(page, baseURL);
    await page.route('**/api/system/v1/radio', async (route) => {
        const response = await route.fetch();
        const body = (await response.json()) as {modules: unknown[]};
        body.modules.reverse();
        await route.fulfill({response, json: body});
    });
    await page.goto('/radio');
    await expect(page.locator('.conn-grid > .ol-card').first()).toHaveAttribute('data-process', 'hmipserver');
    await expect(page.locator('.ol-cards-radio .ol-card').first()).toHaveAttribute('id', 'ol-module-HmIP-RF');
});

test('HmIP through multimacd by choice, and a path the box refuses', async ({page, baseURL}) => {
    await ownConn(page, baseURL);
    const puts: string[] = [];
    page.on('request', (r) => {
        if (r.method() === 'PUT' && r.url().endsWith('/radio/connections')) puts.push(r.postData() ?? '');
    });
    await page.goto('/radio');
    const pick = page.getByLabel('Module for HmIP-RF');
    // directly while BidCos-RF shares the module through multimacd: the box says why not
    await pick.selectOption('0000000A02|direct');
    await page.getByRole('button', {name: 'Apply changes'}).click();
    // the connections' own error line (the device descriptions below carry an .ol-warn of their own, task 97)
    await expect(page.locator('div.ol-warn')).toContainText('HmIP cannot open the module directly while BidCos-RF uses it through the multiplexer');
    await expect(page.getByRole('dialog')).toBeHidden();
    // BidCos-RF off, HmIP through multimacd
    await pick.selectOption('0000000A02|multimacd');
    await page.getByLabel('Module for BidCos-RF').selectOption('none');
    await page.getByRole('button', {name: 'Apply changes'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('HmIP-RF: HMIP-RFUSB · 0000000A02 · /dev/mmd_hmip through multimacd on /dev/raw-uart');
    await expect(dialog).toContainText('hmipserver: HMIP-RFUSB 0000000A02, through multimacd on /dev/raw-uart');
    await expect(dialog).toContainText('multimacd: runs on /dev/raw-uart');
    await dialog.getByRole('button', {name: 'Change'}).click();
    await expect(dialog).toBeHidden();
    expect(JSON.parse(puts[0]!)).toEqual({hmip: '0000000A02', hmip_path: 'multimacd', bidcos: 'none', confirm: true});
    const mmd = page.locator('[data-process="multimacd"]');
    await expect(mmd.locator('.conn-mmd-provides dd')).toHaveText(['not used', '↑ hmipserver (HmIP-RF)']);
    await expect(page.locator('[data-process="hmipserver"] .conn-now')).toHaveText('Chosen: HMIP-RFUSB 0000000A02, through multimacd on /dev/raw-uart');
    await expect(pick).toHaveValue('0000000A02|multimacd');
});

test('a pinned module that is missing is named on the Interfaces and the Status page', async ({page, baseURL}) => {
    await page.context().addCookies([
        {name: 'stub-conn', value: `conn-${Math.random().toString(36).slice(2)}`, url: baseURL!},
        {name: 'stub-conn-missing', value: '0001TK0001', url: baseURL!},
    ]);
    await page.goto('/radio');
    await expect(page.locator('[data-notice="missing-hmip"]')).toContainText('The module chosen for HmIP-RF (0001TK0001) is missing.');
    await expect(page.locator('[data-process="hmipserver"] .conn-now')).toHaveText('Chosen: the chosen module 0001TK0001 is missing: virtual devices only');
    // the pin stays selectable, so it can be set back to Automatic
    await expect(page.getByLabel('Module for HmIP-RF').locator('option')).toHaveText(['Automatic', '0001TK0001 · missing']);
    await page.goto('/');
    const card = page.locator('[data-component="HmIP-RF-module"]');
    await expect(card).toContainText('chosen module missing');
    await expect(card).toContainText('0001TK0001');
    await card.click();
    await expect(page).toHaveURL(/\/system\/interfaces#connections$/);
});

// task 155 (D-106): the one rejection line, two causes; the retry for a key server that was not
// reached, the guided fresh start for a module it refused, and the marker from before the causes
test('the key server was not reached: the notice names the network, and Try again restarts HmIP-RF', async ({page, baseURL}) => {
    const id = `conn-${Math.random().toString(36).slice(2)}`;
    await page.context().addCookies([{name: 'stub-conn', value: id, url: baseURL!}, {name: 'stub-conn-fatal', value: 'unreachable', url: baseURL!}]);
    const posts: string[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().includes('/radio/hmip/exchange/')) posts.push(r.url().split('/exchange/')[1]!);
    });
    await page.goto('/radio');
    const notice = page.locator('[data-notice="hmip-fatal"]');
    await expect(notice).toHaveAttribute('data-cause', 'unreachable');
    await expect(notice).toContainText("could not reach eQ-3's key server");
    await expect(notice).toContainText('Check the network connection and the DNS settings');
    await expect(notice).not.toContainText('Start fresh');
    await notice.getByRole('button', {name: 'Try again'}).click();
    expect(posts).toEqual(['retry']);
    await expect(page.locator('[data-notice="hmip-exchange-done"]')).toContainText('tries the move to this module again');
    await expect(notice).toBeHidden();
});

test('the module was refused: the fresh start asks for the host name, moves the identity aside and switches to local key mode', async ({page, baseURL}) => {
    const id = `conn-${Math.random().toString(36).slice(2)}`;
    await page.context().addCookies([{name: 'stub-conn', value: id, url: baseURL!}, {name: 'stub-conn-fatal', value: 'refused', url: baseURL!}, {name: 'stub-lk', value: id, url: baseURL!}]);
    const posts: {path: string; body: unknown}[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().includes('/radio/hmip/exchange/')) posts.push({path: r.url().split('/exchange/')[1]!, body: JSON.parse(r.postData() ?? '{}')});
    });
    await page.goto('/radio');
    const notice = page.locator('[data-notice="hmip-fatal"]');
    await expect(notice).toHaveAttribute('data-cause', 'refused');
    await expect(notice).toContainText("eQ-3's key server does not know the module 3014F711A000040000000A02");
    await expect(notice).toContainText('The network belongs to the previous module 3014F711A0001F0000000A04.');
    await expect(notice).toContainText('put the previous module back, or start fresh with this module');
    await expect(notice).not.toContainText('Try again');
    const local = notice.getByLabel(/Switch to local key mode in the same step/);
    await expect(local).toBeChecked();
    await notice.getByRole('button', {name: 'Start fresh with this module…'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Start fresh with this module?');
    await expect(dialog).toContainText('The HmIP identity of the previous module 3014F711A0001F0000000A04 is moved aside and kept, nothing is deleted.');
    await expect(dialog).toContainText('every HmIP device has to be reset and paired again');
    await expect(dialog).toContainText('Local key mode is switched on in the same step');
    await expect(dialog).toContainText('Type the host name lab-ccu to confirm.');
    // the input has the focus (the dialog's rule with a field); a wrong name sends nothing
    await expect(dialog.getByLabel('Host name')).toBeFocused();
    await dialog.getByLabel('Host name').fill('other-ccu');
    await dialog.getByRole('button', {name: 'Start fresh'}).click();
    await expect(notice.locator('.ol-warn')).toContainText("The name typed is not this system's host name; nothing happened.");
    expect(posts).toHaveLength(0);
    await notice.getByRole('button', {name: 'Start fresh with this module…'}).click();
    await dialog.getByLabel('Host name').fill(' LAB-CCU ');
    await dialog.getByRole('button', {name: 'Start fresh'}).click();
    await expect(dialog).toBeHidden();
    // the dialog trims what was typed; the case is the server's to ignore
    expect(posts).toEqual([{path: 'fresh-start', body: {confirm: true, hostname: 'LAB-CCU', local_key: true}}]);
    const done = page.locator('[data-notice="hmip-exchange-done"]');
    await expect(done).toContainText('HmIP-RF is starting with an empty network.');
    await done.getByRole('link', {name: 'HmIP device keys'}).click();
    await expect(page).toHaveURL(/\/system\/keys#device-keys$/);
    // the Keys page: local key mode on, and the previous module's identity kept as a fresh-start snapshot
    await expect(page.locator('[data-state="on"]')).toContainText('the HmIP network key is kept on this system');
    const snap = page.locator('.lk-snapshots li[data-sgtin="3014F711A0001F0000000A04"]');
    await expect(snap).toHaveAttribute('data-kind', 'fresh-start');
    await expect(snap).toContainText('the previous module, moved aside by the fresh start');
});

test('the fresh start without local key mode, and a marker from before the causes', async ({page, baseURL}) => {
    const id = `conn-${Math.random().toString(36).slice(2)}`;
    await page.context().addCookies([{name: 'stub-conn', value: id, url: baseURL!}, {name: 'stub-conn-fatal', value: 'refused', url: baseURL!}, {name: 'stub-lk', value: id, url: baseURL!}]);
    const posts: unknown[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().endsWith('/exchange/fresh-start')) posts.push(JSON.parse(r.postData() ?? '{}'));
    });
    await page.goto('/radio');
    const notice = page.locator('[data-notice="hmip-fatal"]');
    await notice.getByLabel(/Switch to local key mode in the same step/).uncheck();
    await notice.getByRole('button', {name: 'Start fresh with this module…'}).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).not.toContainText('Local key mode is switched on');
    await dialog.getByLabel('Host name').fill('lab-ccu');
    await dialog.getByRole('button', {name: 'Start fresh'}).click();
    expect(posts).toEqual([{confirm: true, hostname: 'lab-ccu', local_key: false}]);

    // a marker without a cause: the sentence that names both ways, no action
    await page.context().addCookies([{name: 'stub-conn', value: `${id}-2`, url: baseURL!}, {name: 'stub-conn-fatal', value: 'plain', url: baseURL!}]);
    await page.goto('/radio');
    await expect(notice).toHaveAttribute('data-cause', 'unknown');
    await expect(notice).toContainText("eQ-3's key server rejected the adapter exchange to 3014F711A000040000000A02");
    await expect(notice.getByRole('button')).toHaveCount(0);
});

test('a user sees the diagnosis, not the actions, and the view is not asked for', async ({page, baseURL}) => {
    await page.context().addCookies([{name: 'stub-conn', value: `conn-${Math.random().toString(36).slice(2)}`, url: baseURL!}, {name: 'stub-conn-fatal', value: 'refused', url: baseURL!}]);
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'}}));
    await page.route('**/api/system/v1/radio/firmware', (r) => r.fulfill({status: 403, json: {error: 'forbidden', message: 'administrator role required'}}));
    const calls: string[] = [];
    page.on('request', (r) => calls.push(new URL(r.url()).pathname));
    await page.goto('/radio');
    const notice = page.locator('[data-notice="hmip-fatal"]');
    await expect(notice).toContainText("eQ-3's key server does not know the module");
    await expect(notice).toContainText('put the previous module back, or start fresh with this module');
    await expect(notice.getByRole('button')).toHaveCount(0);
    await expect(notice.getByRole('checkbox')).toHaveCount(0);
    expect(calls).not.toContain('/api/system/v1/radio/hmip/exchange');
});
