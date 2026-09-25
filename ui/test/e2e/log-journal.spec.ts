import {expect, test, type Page, type Route} from '@playwright/test';

// task 85: the Journal panel on the Log page - RAM only, RAM copied to the userfs (ram-sync) or
// persistent on the userfs, the figures, the copies and when a switch applies. The stub is the VM:
// persistent by default and mounted. A card box, ram-sync and the answers of a save or a copy are the
// stub's answer changed per test, so the projects never share state.

const JOURNAL = '**/api/system/v1/journal';
const SYNC = '**/api/system/v1/journal/sync';

async function openJournal(page: Page) {
    page.on('dialog', (dlg) => {
        throw new Error(`native dialog: ${dlg.message()}`);
    });
    await page.goto('/system/log');
    // task 93: the panel is a tab of the settings sheet behind the gear
    await page.getByRole('button', {name: 'Log settings'}).click();
    await page.getByRole('dialog', {name: 'Log settings'}).getByRole('tab', {name: 'Journal'}).click();
    await expect(page.locator('#ol-jr-storage')).toBeVisible();
    return page.locator('.jr-panel');
}

/** the stub's GET answer with fields changed */
async function answer(page: Page, patch: Record<string, unknown>) {
    await page.route(JOURNAL, async (route: Route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const response = await route.fetch();
        await route.fulfill({response, json: {...(await response.json()), ...patch}});
    });
}

const CARD = {platform: 'rpi4', default_storage: 'ram', default_persistent: false, persistent: false, effective: 'ram', ram_usage: 5_300_000, target_usage: 0};
const RAM_SYNC = {
    ...CARD,
    storage: 'ram-sync',
    persist: '0',
    persistent: true,
    effective: 'ram-sync',
    ram_usage: 2_100_000,
    target_usage: 9_800_000,
    sync_interval: '1h',
    target_max_use: '128M',
    last_sync: '2026-09-12T18:00:00Z',
    last_sync_result: 'ok',
    last_sync_copied: 3,
    last_sync_reason: 'early',
    next_sync: '2026-09-12T19:00:00Z',
};

test('the modes, the target, the figures and when a switch applies', async ({page}) => {
    const panel = await openJournal(page);
    const storage = page.locator('#ol-jr-storage');
    await expect(storage.locator('option')).toHaveText(['Product default (Persistent)', 'RAM only', 'RAM, copied to the userfs', 'Persistent on the userfs']);
    expect(await storage.locator('option').evaluateAll((os) => os.map((o) => (o as HTMLOptionElement).value))).toEqual(['', 'ram', 'ram-sync', 'persistent']);
    await expect(storage).toHaveValue('');
    await expect(panel.locator('.jr-now')).toHaveText('The journal is on the userfs now · Archived and active journals take up 32.4M in the file system..');
    // task 216, task 228: the target is picked with the location picker - the userfs here, its folder fixed
    await expect(panel.locator('[data-location-button]')).toContainText('System storage (userfs)');
    await expect(panel.locator('[data-location-folder]')).toHaveValue('var/log/journal');
    await expect(panel.locator('[data-location-folder]')).toHaveAttribute('readonly', '');
    await expect(panel.locator('select')).toHaveCount(1);
    await expect(panel.locator('[data-figure="ram"]')).toHaveText('0 B');
    await expect(panel.locator('[data-figure="target"]')).toHaveText('23.4 MB');
    await expect(panel.locator('[data-figure="free"]')).toHaveText('27.1 GB');
    await expect(page.locator('#ol-jr-runtime')).toHaveAttribute('placeholder', '16M');
    await expect(panel.locator('.jr-tradeoff')).toHaveText(
        'RAM only is lost at every reboot and costs memory, up to the RAM limit. RAM, copied to the userfs, writes the card once per interval and at every shutdown: a reboot loses nothing, a power loss the time since the last copy. Persistent on the userfs survives a reboot and a power loss and costs almost no RAM, but writes the SD card continuously.',
    );
    await expect(panel.locator('.jr-when')).toHaveText(
        'A switch to persistent or to RAM with copies applies at once. A switch to RAM only applies at the next reboot, because the journal on the userfs is not unmounted while in use. The size limits and the copy interval apply at once.',
    );
    // what is not chosen is not shown: no sticks with the userfs, and the copies only with ram-sync
    await expect(panel.locator('[data-stick-target]')).toHaveCount(0);
    await expect(panel.locator('.jr-sync, #ol-jr-interval')).toHaveCount(0);
    await expect(panel.getByRole('button', {name: 'Copy now'})).toHaveCount(0);
    await expect(panel.locator('.jr-pending, .jr-target-warn, .jr-switch, .jr-fallback')).toHaveCount(0);

    // on the userfs now: RAM waits for the reboot, the product default (persistent) changes nothing
    await storage.selectOption('ram');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving keeps the journal on the userfs until the next reboot; from then on it is in RAM.');
    await storage.selectOption('persistent');
    await expect(panel.locator('.jr-switch')).toHaveCount(0);
    // ram-sync from persistent applies at once, and brings its three fields
    await storage.selectOption('ram-sync');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving moves the journal into RAM at once and starts the copies; what is on the userfs stays readable.');
    await expect(page.locator('#ol-jr-interval')).toHaveAttribute('placeholder', '6h');
    await expect(page.locator('#ol-jr-targetuse')).toHaveAttribute('placeholder', '64M');
    await expect(page.locator('#ol-jr-targetage')).toHaveAttribute('placeholder', '30d');
    expect(await page.locator('#ol-jr-intervals option').evaluateAll((os) => os.map((o) => (o as HTMLOptionElement).value))).toEqual(['1h', '6h', '12h', '24h']);
    await expect(panel.locator('[data-figure="last-sync"]')).toHaveText('none yet');
    // not copying yet: no next copy, no Copy now
    await expect(panel.locator('[data-figure="next-sync"]')).toHaveCount(0);
    await expect(panel.getByRole('button', {name: 'Copy now'})).toHaveCount(0);
});

test('on a card: RAM by default, and a switch onto the userfs or into ram-sync applies at once', async ({page}) => {
    await answer(page, {...CARD, target_ok: false, target_free: null});
    const panel = await openJournal(page);
    const storage = page.locator('#ol-jr-storage');
    await expect(storage.locator('option').first()).toHaveText('Product default (RAM)');
    await expect(panel).toContainText('The journal is in RAM now');
    await expect(panel.locator('[data-figure="ram"]')).toHaveText('5.3 MB');
    await expect(panel.locator('[data-figure="free"]')).toHaveText('—');
    await expect(panel.locator('.jr-target-warn')).toHaveText('The userfs cannot take the journal right now: /usr/local is not mounted or not writable.');
    await storage.selectOption('persistent');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving moves the journal onto the userfs at once.');
    await storage.selectOption('ram-sync');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving starts the copies to the userfs at once; the journal stays in RAM.');
    await storage.selectOption('');
    await expect(panel.locator('.jr-switch')).toHaveCount(0);
});

test('a pending reboot is noted when the panel opens', async ({page}) => {
    await answer(page, {storage: 'ram', persist: '0', persistent: true, reboot_pending: true});
    const panel = await openJournal(page);
    await expect(page.locator('#ol-jr-storage')).toHaveValue('ram');
    await expect(panel.locator('.jr-pending')).toHaveText('A reboot is pending: the journal stays on the userfs until the next boot; from then on it is in RAM.');
    // the saved choice is not a switch of its own
    await expect(panel.locator('.jr-switch')).toHaveCount(0);
});

test('ram-sync switched to RAM: the copies have stopped, and Copy now is not the saved mode', async ({page}) => {
    await answer(page, {...RAM_SYNC, storage: 'ram', reboot_pending: true});
    const panel = await openJournal(page);
    await expect(panel.locator('.jr-pending')).toHaveText('A reboot is pending: the copies have stopped and stay readable until the next boot; from then on the journal is in RAM only.');
    await expect(panel.locator('[data-figure="last-sync"]')).toContainText('3 files');
    // the fields are ram-sync's settings, which are not the choice any more
    await expect(page.locator('#ol-jr-interval')).toHaveCount(0);
    await expect(panel.getByRole('button', {name: 'Copy now'})).toBeDisabled();
});

// task 85 (D-67): the reason of the last copy - here the one at shutdown, which the box keeps beside
// the copies so it is still known after the reboot
test('in German: why the last copy ran', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await answer(page, {...RAM_SYNC, last_sync_reason: 'shutdown'});
    await page.goto('/system/log');
    await page.getByRole('button', {name: 'Protokoll-Einstellungen'}).click();
    await page.getByRole('dialog', {name: 'Protokoll-Einstellungen'}).getByRole('tab', {name: 'Journal'}).click();
    const panel = page.locator('.jr-panel');
    await expect(panel.locator('[data-sync-reason="shutdown"]')).toHaveText('· beim Herunterfahren');
    await expect(panel.locator('[data-figure="last-sync"]')).toContainText('3');
});

test('a copy without a reason shows none', async ({page}) => {
    const {last_sync_reason: _, ...withoutReason} = RAM_SYNC;
    await answer(page, {...withoutReason, last_sync_reason: undefined});
    const panel = await openJournal(page);
    await expect(panel.locator('[data-figure="last-sync"]')).toContainText('3 files');
    await expect(panel.locator('[data-sync-reason]')).toHaveCount(0);
});

test('ram-sync in effect: the copies, Copy now, and what a switch away does', async ({page}) => {
    await answer(page, RAM_SYNC);
    const posts: string[] = [];
    let fail = false;
    await page.route(SYNC, async (route: Route) => {
        posts.push(route.request().method());
        await route.fulfill({
            json: {...RAM_SYNC, last_sync: '2026-09-12T18:30:00Z', last_sync_copied: fail ? 0 : 5, last_sync_result: fail ? 'failed' : 'ok', last_sync_error: fail ? 'copying system@x.journal failed (the userfs full or read-only?)' : '', last_sync_reason: 'manual'},
        });
    });
    const panel = await openJournal(page);
    await expect(panel.locator('.jr-now')).toContainText('The journal is in RAM now and copied to the userfs');
    await expect(page.locator('#ol-jr-storage')).toHaveValue('ram-sync');
    await expect(page.locator('#ol-jr-interval')).toHaveValue('1h');
    await expect(page.locator('#ol-jr-targetuse')).toHaveValue('128M');
    await expect(page.locator('#ol-jr-targetage')).toHaveValue('');
    await expect(panel.locator('[data-figure="last-sync"]')).toContainText('3 files');
    // task 85 (D-67): why the last copy ran - here early, the RAM journal at 80 % of its limit
    await expect(panel.locator('[data-sync-reason="early"]')).toHaveText('· early: the RAM journal was nearly full');
    await expect(panel.locator('[data-figure="next-sync"]')).not.toHaveText('—');
    await expect(panel.locator('.jr-switch, .jr-sync-failed, .jr-pending')).toHaveCount(0);

    // a copy now keeps what is typed and not saved
    await page.locator('#ol-jr-interval').fill('30min');
    await panel.getByRole('button', {name: 'Copy now'}).click();
    await expect(panel).toContainText('Copied: 5 files.');
    expect(posts).toEqual(['POST']);
    await expect(panel.locator('[data-figure="last-sync"]')).toContainText('5 files');
    await expect(panel.locator('[data-sync-reason="manual"]')).toHaveText('· by hand');
    await expect(page.locator('#ol-jr-interval')).toHaveValue('30min');

    // a copy that fails says why, and no "Copied"
    fail = true;
    await panel.getByRole('button', {name: 'Copy now'}).click();
    await expect(panel.locator('.jr-sync-failed')).toHaveText('The last copy failed: copying system@x.journal failed (the userfs full or read-only?)');
    await expect(panel).not.toContainText('Copied:');

    // switching away
    await page.locator('#ol-jr-storage').selectOption('ram');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving stops the copies at once, after a last one; they stay readable until the next reboot, and from then on the journal is in RAM only.');
    await page.locator('#ol-jr-storage').selectOption('persistent');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving moves the journal onto the userfs at once.');
});

test('ram-sync that could not be set up says why', async ({page}) => {
    await answer(page, {...CARD, storage: 'ram-sync', persist: '0', fallback: 'the userfs target cannot be mounted, the journal stays in RAM (STORAGE=ram-sync)'});
    const panel = await openJournal(page);
    await expect(panel.locator('.jr-fallback')).toHaveText('The journal could not be set up on the userfs and is in RAM: the userfs target cannot be mounted, the journal stays in RAM (STORAGE=ram-sync)');
    await expect(panel.locator('.jr-now')).toContainText('The journal is in RAM now');
    await expect(page.locator('#ol-jr-interval')).toBeVisible();
    await expect(panel.getByRole('button', {name: 'Copy now'})).toHaveCount(0);
    await expect(panel.locator('.jr-switch')).toHaveCount(0);
});

test('a save sends storage, the copy settings and no persist, and the answer notes the pending reboot', async ({page}) => {
    const bodies: Record<string, unknown>[] = [];
    await page.route(JOURNAL, async (route: Route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        const body = route.request().postDataJSON() as Record<string, unknown>;
        bodies.push(body);
        await route.fulfill({
            json: {
                ...body,
                persist: '0',
                platform: 'ova',
                default_storage: 'persistent',
                default_persistent: true,
                persistent: true,
                effective: 'persistent',
                reboot_pending: true,
                ram_usage: 0,
                target_usage: 23_400_000,
                target_free: 27_100_000_000,
                target_ok: true,
                last_sync: null,
                last_sync_copied: 0,
                next_sync: null,
            },
        });
    });
    const panel = await openJournal(page);
    await page.locator('#ol-jr-storage').selectOption('ram');
    await page.locator('#ol-jr-runtime').fill('8M');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect.poll(() => bodies.length).toBe(1);
    expect(bodies[0]).toEqual({storage: 'ram', target: 'userfs', runtime_max_use: '8M', system_max_use: '', system_max_file: '', rate_limit_burst: '', sync_interval: '', target_max_use: '', target_max_age: ''});
    expect(bodies[0]).not.toHaveProperty('persist');
    await expect(panel).toContainText('Saved. The journal stays on the userfs until the next boot; from then on it is in RAM.');
    await expect(panel.locator('.jr-pending')).toBeVisible();
    await expect(panel.locator('.jr-switch')).toHaveCount(0);
});

test('a link with settings=journal opens the sheet on the Journal tab', async ({page}) => {
    await page.goto('/system/log?settings=journal');
    const sheet = page.getByRole('dialog', {name: 'Log settings'});
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole('tab', {name: 'Journal'})).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('#ol-jr-storage')).toBeVisible();
});

// task 216, task 228: a USB stick or a network share as the target - ram-sync only - picked with
// the location picker; the locations as GET /storage/locations lists them for the journal: a stick
// without a label and a read-only one cannot be picked, nor a read-only share
const LOCATIONS = {
    use: 'journal',
    locations: [
        {id: 'userfs', kind: 'userfs', name: 'userfs', detail: '/usr/local', state: 'present', free_bytes: 18e9, total_bytes: 29e9, uses: [], allowed: true, userfs_prefix: 'var/log/journal', fixed: true, default: 'var/log/journal'},
        {id: 'usb:', kind: 'usb', name: 'Intenso Rainbow', state: 'present', free_bytes: 4e9, total_bytes: 8e9, uses: [], allowed: false, code: 'no-label'},
        {id: 'usb:LOG_STICK', kind: 'usb', name: 'LOG STICK', detail: 'SanDisk Cruzer', state: 'present', free_bytes: 12_884_901_888, total_bytes: 16e9, uses: [], allowed: true, code: 'ram-sync-only', default: 'journal'},
        {id: 'usb:OLD', kind: 'usb', name: 'OLD', state: 'present', read_only: true, free_bytes: 1e9, total_bytes: 4e9, uses: [], allowed: false, code: 'read-only'},
        {id: 'share:nas', kind: 'share', name: 'nas', detail: '//nas.lan/logs', state: 'idle', uses: [], allowed: true, code: 'ram-sync-only', default: 'journal'},
        {id: 'share:media', kind: 'share', name: 'media', detail: 'nas:/media', state: 'idle', read_only: true, uses: [], allowed: false, code: 'read-only'},
    ],
};
const STICK_ON = {...RAM_SYNC, persistent: false, target: 'usb:LOG_STICK/journal', target_label: 'LOG_STICK', target_dir: 'journal', target_path: '/media/usb2/journal', target_usage: 7_300_000, target_free: 12_884_901_888, last_sync_reason: 'plug', usage: ''};
const SHARE_ON = {...RAM_SYNC, persistent: false, target: 'share:nas/ccu/journal', target_share: 'nas', target_dir: 'ccu/journal', target_path: '/media/net/nas/ccu/journal', target_usage: null, target_free: null, last_sync_reason: 'interval', usage: ''};

async function locations(page: Page, list = LOCATIONS) {
    await page.route('**/api/system/v1/storage/locations?use=journal', (route) => route.fulfill({json: list}));
}

test('a USB stick as the target: picked by its label, for ram-sync only, and saved', async ({page}) => {
    await answer(page, CARD);
    await locations(page);
    const bodies: Record<string, unknown>[] = [];
    await page.route(JOURNAL, async (route: Route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        const body = route.request().postDataJSON() as Record<string, unknown>;
        bodies.push(body);
        await route.fulfill({json: {...STICK_ON, ...body, target_dir: 'ccu/journal', target_path: '/media/usb2/ccu/journal'}});
    });
    const panel = await openJournal(page);
    const save = panel.getByRole('button', {name: 'Save'});
    await panel.locator('[data-location-button]').click();
    const list = panel.locator('[data-location-list]');
    await expect(list.locator('[data-location]')).toHaveCount(6);
    await expect(list.locator('[data-location="usb:LOG_STICK"]')).toContainText('LOG STICK');
    await expect(list.locator('[data-location="usb:LOG_STICK"]')).toContainText('12.9 GB free');
    await expect(list.locator('[data-location="usb:LOG_STICK"]')).toContainText('Takes the copies only');
    // no label, or read-only: not a target, and why
    await expect(list.locator('[data-location="usb:"]')).toHaveAttribute('aria-disabled', 'true');
    await expect(list.locator('[data-location="usb:"] [data-why]')).toContainText('It has no label');
    await expect(list.locator('[data-location="usb:OLD"]')).toHaveAttribute('aria-disabled', 'true');
    await list.locator('[data-location="usb:OLD"]').click({force: true});
    await expect(list).toBeVisible();
    // the keyboard: down and Enter pick
    await list.locator('[data-location="userfs"]').focus();
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await expect(list).toHaveCount(0);
    await expect(panel.locator('[data-location-button]')).toContainText('LOG STICK');
    await expect(panel.locator('[data-location-folder]')).toHaveValue('journal');
    // the card's default is RAM only: said
    await expect(panel.locator('[data-stick-mode]')).toHaveText('A USB stick takes only the copies: choose RAM, copied to the USB stick, as the storage.');
    // persistent is not offered for a stick; ram-sync is named after it
    const storage = page.locator('#ol-jr-storage');
    await expect(storage.locator('option[value="persistent"]')).toHaveAttribute('disabled', '');
    await expect(storage.locator('option[value="ram-sync"]')).toHaveText('RAM, copied to the USB stick');
    await storage.selectOption('ram-sync');
    await expect(panel.locator('[data-stick-mode]')).toHaveCount(0);
    await expect(panel.locator('.jr-switch')).toHaveText('Saving keeps the journal in RAM and starts the copies to the USB stick at once, while it is plugged in.');
    await panel.locator('[data-location-folder]').fill('ccu/journal');
    await expect(panel.locator('[data-stick-chosen]')).toHaveText('The copies go to ccu/journal on the USB stick labelled LOG_STICK; a stick with another label is never written.');
    await panel.locator('[data-location-folder]').fill('../x');
    await expect(save).toBeDisabled();
    await panel.locator('[data-location-folder]').fill('ccu/journal');
    await expect(save).toBeEnabled();
    await save.click();
    await expect.poll(() => bodies.length).toBe(1);
    expect(bodies[0]).toMatchObject({storage: 'ram-sync', target: 'usb:LOG_STICK/ccu/journal'});
    // the answer: in RAM, copied to the stick; its figures and the copy's reason
    await expect(panel.locator('.jr-now')).toHaveText('The journal is in RAM now and copied to the USB stick LOG_STICK.');
    await expect(panel.locator('.jr-figures dt').nth(1)).toHaveText('On the USB stick');
    await expect(panel.locator('.jr-figures dt').nth(2)).toHaveText('Free on the USB stick');
    await expect(panel.locator('[data-figure="target"]')).toHaveText('7.3 MB');
    await expect(panel.locator('[data-sync-reason="plug"]')).toHaveText('· when the USB stick was plugged in');
    await expect(panel.locator('[data-location-folder]')).toHaveValue('ccu/journal');
    await expect(panel.locator('.jr-switch')).toHaveCount(0);
});

test('the USB stick of the target is not plugged in: RAM, and said so', async ({page}) => {
    await answer(page, {...STICK_ON, effective: 'ram', target_path: undefined, target_ok: false, target_usage: null, target_free: null, next_sync: null, last_sync_reason: 'unplug', fallback: 'the USB stick LOG_STICK is not plugged in, the journal stays in RAM until it is'});
    await locations(page, {...LOCATIONS, locations: LOCATIONS.locations.filter((l) => l.id !== 'usb:LOG_STICK')});
    const panel = await openJournal(page);
    await expect(panel.locator('.jr-fallback')).toHaveText('The USB stick LOG_STICK is not plugged in: the journal is in RAM, and the copies go to the stick as soon as it is back.');
    await expect(panel.locator('.jr-now')).toHaveText('The journal is in RAM now.');
    await expect(panel.locator('.jr-target-warn')).toHaveCount(0);
    await expect(panel.locator('[data-location-button]')).toContainText('LOG_STICK');
    await expect(panel.locator('[data-location-button]')).toContainText('not plugged in');
    await expect(panel.locator('[data-location-missing]')).toHaveText('The USB stick LOG_STICK is not plugged in now; what goes there is skipped until it is.');
    await expect(panel.locator('[data-stick-chosen]')).toHaveText('The copies go to journal on the USB stick labelled LOG_STICK; a stick with another label is never written.');
    await expect(panel.locator('[data-sync-reason="unplug"]')).toHaveText('· before the USB stick was unmounted');
    await expect(panel.getByRole('button', {name: 'Copy now'})).toHaveCount(0);
    // back to the userfs: the next copies go there
    await panel.locator('[data-location-button]').click();
    await panel.locator('[data-location="userfs"]').click();
    await expect(panel.locator('[data-location-folder]')).toHaveValue('var/log/journal');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving sends the next copies to the userfs; the copies made so far stay where they are.');
});

test('a network share as the target: picked by its name, ram-sync only, and what it says', async ({page}) => {
    await answer(page, CARD);
    await locations(page);
    await page.route('**/api/system/v1/storage/dirs?*', (route) => route.fulfill({json: {dirs: ['ccu', 'ccu/journal'].filter((d) => d.startsWith(new URL(route.request().url()).searchParams.get('prefix') ?? '')), exists: false}}));
    const bodies: Record<string, unknown>[] = [];
    await page.route(JOURNAL, async (route: Route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        bodies.push(route.request().postDataJSON() as Record<string, unknown>);
        await route.fulfill({json: SHARE_ON});
    });
    const panel = await openJournal(page);
    await panel.locator('[data-location-button]').click();
    await expect(panel.locator('[data-location="share:media"]')).toHaveAttribute('aria-disabled', 'true');
    await expect(panel.locator('[data-location="share:media"] [data-why]')).toHaveText('It is mounted read-only.');
    await panel.locator('[data-location="share:nas"]').click();
    await expect(panel.locator('[data-location-button]')).toContainText('nas');
    await expect(panel.locator('[data-location-button]')).toContainText('Network share · mounted when used');
    const storage = page.locator('#ol-jr-storage');
    await expect(storage.locator('option[value="ram-sync"]')).toHaveText('RAM, copied to the network share');
    await expect(storage.locator('option[value="persistent"]')).toHaveAttribute('disabled', '');
    await storage.selectOption('ram-sync');
    await expect(panel.locator('.jr-switch')).toHaveText('Saving keeps the journal in RAM and starts the copies to the network share; the first one comes at the interval, or now with Copy now.');
    await panel.locator('[data-location-folder]').fill('cc');
    await expect(panel.locator('[data-location-picker] datalist option')).toHaveCount(2);
    await panel.locator('[data-location-folder]').fill('ccu/journal');
    await expect(panel.locator('[data-stick-chosen]')).toHaveText('The copies go to ccu/journal on the network share nas; when it cannot be reached, a copy is skipped and the journal stays in RAM.');
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect.poll(() => bodies.length).toBe(1);
    expect(bodies[0]).toMatchObject({storage: 'ram-sync', target: 'share:nas/ccu/journal'});
    await expect(panel.locator('.jr-now')).toHaveText('The journal is in RAM now and copied to the network share nas.');
    await expect(panel.locator('.jr-figures dt').nth(1)).toHaveText('On the network share');
});

// openccu-lite B-214 and B-213: a share whose folders cannot be listed says why at the folder field
// (not "a new folder"), and a save the system's write test refuses shows the reason
test('a share that cannot be read or reached: the reason at the folder, and a refused save', async ({page}) => {
    await answer(page, CARD);
    await locations(page);
    let dirsAnswer = {status: 502, json: {error: 'read-only', message: 'the system may not read this folder: permission denied', detail: {state: 'read-only'}}};
    await page.route('**/api/system/v1/storage/dirs?*', (route) => route.fulfill(dirsAnswer));
    await page.route(JOURNAL, async (route: Route) => {
        if (route.request().method() !== 'PUT') return route.fallback();
        await route.fulfill({status: 422, json: {error: 'read-only', message: 'the network share nas cannot take the journal\'s copies in ccu/journal: read-only (mkdir: permission denied)'}});
    });
    const panel = await openJournal(page);
    await panel.locator('[data-location-button]').click();
    await panel.locator('[data-location="share:nas"]').click();
    await page.locator('#ol-jr-storage').selectOption('ram-sync');
    await panel.locator('[data-location-folder]').fill('ccu/journal');
    await expect(panel.locator('[data-folder-error]')).toContainText('The system may not read this folder on the share (permission denied).');
    await expect(panel.locator('[data-folder-error]')).toContainText('root_squash');
    await expect(panel.locator('[data-folder-new]')).toHaveCount(0);
    await panel.getByRole('button', {name: 'Save'}).click();
    await expect(page.locator('.ol-warn').filter({hasText: 'cannot take the journal'})).toBeVisible();
    dirsAnswer = {status: 502, json: {error: 'auth-failed', message: 'the share nas could not be mounted: mount error(13)', detail: {state: 'auth-failed'}}};
    await panel.locator('[data-location-folder]').fill('ccu/journa');
    await expect(panel.locator('[data-folder-error]')).toHaveText('The share refused the sign-in: check the user and the password on System → Storage.');
    dirsAnswer = {status: 503, json: {error: 'stale', message: 'the share does not answer'}} as typeof dirsAnswer;
    await panel.locator('[data-location-folder]').fill('ccu/journ');
    await expect(panel.locator('[data-folder-error]')).toHaveText('The share does not answer.');
});

test('the share of the target could not be reached: RAM, and said so', async ({page}) => {
    await answer(page, {...SHARE_ON, effective: 'ram', last_sync_result: 'failed', last_sync_error: 'the network share nas cannot be reached', fallback: 'the network share nas cannot be reached, the journal stays in RAM until a copy reaches it'});
    await locations(page);
    const panel = await openJournal(page);
    await expect(panel.locator('[data-fallback="share"]')).toHaveText('The network share nas could not be reached: the journal is in RAM, and the copies go to it as soon as a copy reaches it.');
});

test('in German: the stick target', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await answer(page, STICK_ON);
    await locations(page);
    await page.goto('/system/log');
    await page.getByRole('button', {name: 'Protokoll-Einstellungen'}).click();
    await page.getByRole('dialog', {name: 'Protokoll-Einstellungen'}).getByRole('tab', {name: 'Journal'}).click();
    const panel = page.locator('.jr-panel');
    await expect(panel.locator('.jr-now')).toHaveText('Das Journal liegt jetzt im RAM und wird auf den USB-Stick LOG_STICK kopiert.');
    await expect(panel.locator('[data-sync-reason="plug"]')).toHaveText('· beim Einstecken des USB-Sticks');
    await expect(panel.locator('[data-stick-chosen]')).toHaveText('Die Kopien gehen nach journal auf den USB-Stick mit dem Label LOG_STICK; ein Stick mit anderem Label wird nie beschrieben.');
    await expect(panel.locator('[data-location-button]')).toContainText('USB-Stick');
});
