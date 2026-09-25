import {expect, test, type Page} from '@playwright/test';

// The power button of the top bar: far right, administrators only; a menu with reboot, halt and a
// reboot into the recovery system, each asking again in the shell's dialog; the full-page states
// after the box answered; the recovery refused while an update is staged and left out on a
// container; and the Status page without its old Reboot button.

const USER = {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'};

async function openMenu(page: Page) {
    await page.locator('.ol-powerbtn').click();
    const menu = page.locator('.ol-powerpop');
    await expect(menu).toBeVisible();
    return menu;
}

/** Counts the POSTs of the three power routes. */
async function countPosts(page: Page) {
    const posts: string[] = [];
    page.on('request', (r) => {
        const path = new URL(r.url()).pathname;
        if (r.method() === 'POST' && /\/api\/system\/v1\/(reboot|halt|reboot\/recovery)$/.test(path)) posts.push(path);
    });
    return posts;
}

test('the power button is the last control of the top bar, for an administrator', async ({page}) => {
    await page.goto('/');
    const button = page.locator('.ol-powerbtn');
    await expect(button).toBeVisible();
    expect(await page.locator('.ol-header > :last-child').evaluate((e) => e.classList.contains('ol-power'))).toBe(true);
    const gear = (await page.locator('.ol-header a[title="Settings"]').boundingBox())!;
    const power = (await button.boundingBox())!;
    if (Math.abs(power.y - gear.y) < 4) expect(power.x).toBeGreaterThan(gear.x + gear.width);
});

test('a user session has no power button', async ({page}) => {
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: USER}));
    await page.goto('/');
    await expect(page.locator('.ol-header a[title="Settings"]')).toBeVisible();
    await expect(page.locator('.ol-powerbtn')).toHaveCount(0);
});

test('the menu lists reboot, halt and recovery with a hint each, and closes on Escape and outside', async ({page}) => {
    await page.goto('/');
    const menu = await openMenu(page);
    await expect(menu.locator('[data-action]')).toHaveCount(3);
    await expect(menu.locator('[data-action="reboot"]')).toContainText('Back in about a minute');
    await expect(menu.locator('[data-action="halt"]')).toContainText('Stays off until it is powered on again');
    await expect(menu.locator('[data-action="recovery"]')).toContainText('For repairs and firmware images; leave it from its own page');
    // inside the viewport, on a phone as well
    const box = (await menu.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width);
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    const again = await openMenu(page);
    // a click outside: beside the menu, below it. On a phone the menu hangs over the page's heading since the
    // top bar is compact and sticky (task 99), so a click on the heading lands on the menu's Reboot instead
    const open = (await again.boundingBox())!;
    const vp = page.viewportSize()!;
    await page.mouse.click(4, Math.min(open.y + open.height + 16, vp.height - 4));
    await expect(page.locator('.ol-powerpop')).toBeHidden();
});

for (const entry of [
    {action: 'reboot', title: 'Reboot', text: 'Reboot this system now?'},
    {action: 'halt', title: 'Halt', text: 'It does not come back by itself'},
    {action: 'recovery', title: 'Reboot into the recovery system', text: 'with a page of its own'},
]) {
    test(`${entry.action} asks in the shell dialog, and Cancel sends nothing`, async ({page}) => {
        page.on('dialog', (d) => {
            throw new Error(`native dialog: ${d.message()}`);
        });
        const posts = await countPosts(page);
        await page.goto('/');
        const menu = await openMenu(page);
        await menu.locator(`[data-action="${entry.action}"]`).click();
        const dialog = page.getByRole('dialog', {name: entry.title, exact: true});
        await expect(dialog).toBeVisible();
        await expect(dialog).toContainText(entry.text);
        // the confirming button repeats the action's name
        await expect(dialog.getByRole('button', {name: entry.title, exact: true})).toBeVisible();
        await dialog.getByRole('button', {name: 'Cancel'}).click();
        await expect(page.getByRole('dialog')).toBeHidden();
        await page.waitForTimeout(300);
        expect(posts).toEqual([]);
        await expect(page.locator('.ol-powerstate')).toHaveCount(0);
    });
}

// the halt question and the shut-down page name what starts the box again, by /VERSION's PLATFORM
for (const box of [
    {name: 'a Raspberry Pi 4', platform: 'rpi4', container: '', question: 'has no power button: to start it again, unplug its power supply and plug it in again', after: 'It has no power button: unplug its power supply and plug it in again to start it.'},
    {name: 'a CCU3-class board', platform: 'rpi3', container: '', question: 'has no power button', after: 'It has no power button'},
    {name: 'a Raspberry Pi 5', platform: 'rpi5', container: '', question: 'press the power button on its board', after: 'press the power button on its board'},
    {name: 'the virtual machine', platform: 'ova', container: '', question: 'Shut this virtual machine down now? It does not come back by itself: start it again on its host.', after: 'The virtual machine stays off until it is started again on its host.'},
    {name: 'a container', platform: 'lxc', container: 'lxc', question: 'Stop this container now?', after: 'The container stays stopped until it is started again on its host.'},
    {name: 'an unknown product', platform: 'generic-x86_64', container: '', question: 'a Raspberry Pi or a CCU3 needs its power supply unplugged', after: 'or start the virtual machine or the container on its host'},
]) {
    test(`halt on ${box.name}: the question and the shut-down page say how it starts again`, async ({page}) => {
        await page.route('**/api/system/v1/system-update', async (r) => {
            const response = await r.fetch();
            const body = await response.json();
            body.running = {...body.running, platform: box.platform};
            body.container = box.container;
            await r.fulfill({response, json: body});
        });
        // the box stops answering once it halts
        let halted = false;
        await page.route('**/api/system/v1/halt', (r) => {
            halted = true;
            return r.fulfill({json: {ok: true, halting: true}});
        });
        await page.route('**/api/system/v1/health', (r) => (halted ? r.abort('connectionrefused') : r.continue()));
        await page.goto('/');
        const menu = await openMenu(page);
        // the menu has asked the box what it is by the time it is open
        await page.waitForLoadState('networkidle');
        await menu.locator('[data-action="halt"]').click();
        const dialog = page.getByRole('dialog', {name: 'Halt', exact: true});
        await expect(dialog).toContainText(box.question);
        if (box.platform !== 'rpi4' && box.platform !== 'rpi3') await expect(dialog).not.toContainText('has no power button');
        await dialog.getByRole('button', {name: 'Halt', exact: true}).click();
        await expect(page.locator('.ol-powerstate')).toContainText('The system is shut down');
        await expect(page.locator('.ol-powerstate')).toContainText(box.after);
    });
}

test('reboot: the waiting state, then a reload once the box answers with a new uptime', async ({page}) => {
    let rebooted = false;
    await page.route('**/api/system/v1/reboot', (r) => {
        rebooted = true;
        return r.fulfill({json: {ok: true, message: 'rebooting'}});
    });
    await page.route('**/api/system/v1/health', async (r) => {
        const response = await r.fetch();
        const h = await response.json();
        if (rebooted) h.uptime_s = 42;
        await r.fulfill({response, json: h});
    });
    await page.goto('/');
    await (await openMenu(page)).locator('[data-action="reboot"]').click();
    const dialog = page.getByRole('dialog', {name: 'Reboot', exact: true});
    const reloaded = page.waitForEvent('load', {timeout: 15_000});
    await dialog.getByRole('button', {name: 'Reboot', exact: true}).click();
    await expect(page.locator('.ol-powerstate')).toContainText('Rebooting…');
    await reloaded;
    await expect(page.locator('main h1')).toHaveText('Status');
    await expect(page.locator('.ol-powerstate')).toHaveCount(0);
});

test('halt: shutting down while the box answers, the shut-down state once it stops, and nothing polled after', async ({page}) => {
    const posts = await countPosts(page);
    let health = 0;
    let down = false;
    let counting = false;
    page.on('request', (r) => {
        if (counting && new URL(r.url()).pathname === '/api/system/v1/health') health++;
    });
    await page.route('**/api/system/v1/health', (r) => (down ? r.abort('connectionrefused') : r.continue()));
    await page.goto('/');
    await (await openMenu(page)).locator('[data-action="halt"]').click();
    const dialog = page.getByRole('dialog', {name: 'Halt', exact: true});
    await dialog.getByRole('button', {name: 'Halt', exact: true}).click();
    const state = page.locator('.ol-powerstate');
    // not yet: the box still answers, and nobody should unplug it now
    await expect(state).toContainText('Shutting down…');
    await expect(state).toContainText('Wait until the system has stopped answering before you unplug it.');
    await expect(state.getByRole('progressbar')).toHaveCount(0);
    expect(posts).toEqual(['/api/system/v1/halt']);
    down = true;
    await expect(state).toContainText('The system is shut down');
    counting = true;
    await page.waitForTimeout(4500);
    expect(health).toBe(0);
    await expect(state).toBeVisible();
});

test('recovery: its own state with a link to the box, and no reload', async ({page}) => {
    const posts = await countPosts(page);
    await page.goto('/');
    await (await openMenu(page)).locator('[data-action="recovery"]').click();
    const dialog = page.getByRole('dialog', {name: 'Reboot into the recovery system', exact: true});
    await expect(dialog).toContainText('http://127.0.0.1/');
    await dialog.getByRole('button', {name: 'Reboot into the recovery system', exact: true}).click();
    const state = page.locator('.ol-powerstate');
    await expect(state).toContainText('Rebooting into the recovery system…');
    await expect(state.getByRole('link', {name: 'http://127.0.0.1/'})).toHaveAttribute('href', 'http://127.0.0.1/');
    expect(posts).toEqual(['/api/system/v1/reboot/recovery']);
    let navigated = 0;
    page.on('framenavigated', () => navigated++);
    await page.waitForTimeout(4500);
    expect(navigated).toBe(0);
});

test('recovery is refused while a system update is staged', async ({page}) => {
    const posts = await countPosts(page);
    await page.route('**/api/system/v1/system-update', async (r) => {
        const response = await r.fetch();
        const body = await response.json();
        body.staged = {file: 'openccu-lite-x86_64-ova-1.0.0.zip', size: 300_000_000, kind: 'zip', recovery_armed: false};
        await r.fulfill({response, json: body});
    });
    await page.goto('/');
    await (await openMenu(page)).locator('[data-action="recovery"]').click();
    const dialog = page.getByRole('dialog', {name: 'Reboot into the recovery system', exact: true});
    await expect(dialog).toContainText('A system update is staged');
    await expect(dialog).toContainText('openccu-lite-x86_64-ova-1.0.0.zip');
    await expect(dialog.getByRole('button', {name: 'Reboot into the recovery system', exact: true})).toHaveCount(0);
    await dialog.getByRole('button', {name: 'Close'}).last().click();
    await expect(page.getByRole('dialog')).toBeHidden();
    expect(posts).toEqual([]);
});

test("the box's refusal of a recovery boot is shown and the page stays", async ({page}) => {
    await page.route('**/api/system/v1/reboot/recovery', (r) =>
        r.fulfill({status: 409, json: {error: 'update-staged', message: 'a system update is staged and the recovery system would install it: install or discard it first'}}),
    );
    await page.goto('/');
    await (await openMenu(page)).locator('[data-action="recovery"]').click();
    await page.getByRole('dialog').getByRole('button', {name: 'Reboot into the recovery system', exact: true}).click();
    await expect(page.getByRole('dialog', {name: 'Reboot into the recovery system', exact: true})).toContainText('A system update is staged');
    await expect(page.locator('.ol-powerstate')).toHaveCount(0);
});

test('a container has no recovery entry', async ({page}) => {
    await page.route('**/api/system/v1/system-update', async (r) => {
        const response = await r.fetch();
        await r.fulfill({response, json: {...(await response.json()), container: 'lxc'}});
    });
    await page.goto('/');
    const menu = await openMenu(page);
    await expect(menu.locator('[data-action="recovery"]')).toHaveCount(0);
    await expect(menu.locator('[data-action]')).toHaveCount(2);
});

test('the Status page has no Reboot button any more', async ({page}) => {
    await page.goto('/');
    await expect(page.locator('main h1')).toHaveText('Status');
    await expect(page.locator('main').getByRole('button', {name: 'Reboot'})).toHaveCount(0);
});
