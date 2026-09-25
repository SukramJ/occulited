import {expect, test, type Page, type TestInfo} from '@playwright/test';
import {fitsWindow} from './scroll';

// task 95: System → Status LED against the stub (test/stub/led.mjs). The simple view: what the LED
// shows now, the switch that asks before it darkens the LED, the six rows and Save; the advanced
// view: rows moved by their handles (task 162), fixed rows without, Test and Locate; a box without a status
// LED; German on a phone; a user who reads but does not change. The stub keeps the configuration
// per browser (stub-led=<id>), so the three projects never meet.

const USER = {setup_required: false, authenticated: true, user: 'monitor', role: 'user', must_change_password: false, sid: 'U1'};

async function plant(page: Page, baseURL: string, info: TestInfo, cookies: Record<string, string> = {}) {
    const id = `${info.project.name}-${info.title}`.replace(/[^A-Za-z0-9-]/g, '_');
    await page.context().addCookies(Object.entries({...cookies, 'stub-led': id}).map(([name, value]) => ({name, value, url: baseURL})));
}

function watchErrors(page: Page): string[] {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
    page.on('console', (m) => {
        if (m.type() === 'error') errors.push(`console: ${m.text()}`);
    });
    return errors;
}

test('the page says what the LED shows, why, and offers the switches of the simple view', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info, {'stub-led-active': 'service-failed'});
    const errors = watchErrors(page);
    await page.goto('/system/led');
    await expect(page.getByRole('heading', {level: 1, name: 'Status LED'})).toBeVisible();
    const now = page.locator('[data-led-now]');
    await expect(now).toHaveAttribute('data-led-now', 'service-failed');
    await expect(now).toContainText('Red, slow blink');
    await expect(now).toContainText('A service has failed: ssdpd');
    await expect(now.getByRole('link', {name: 'Open page'})).toHaveAttribute('href', '/system/services');
    await expect(page.locator('[data-led-simple]')).toHaveCount(5);
    await expect(page.locator('[data-led-simple="no-network"]')).toContainText('Yellow, fast blink');
    await expect(page.locator('[data-led-advanced]')).not.toHaveAttribute('open', '');
    // the legend is generated from the configuration, the recovery system included
    await page.locator('[data-led-legend] summary').click();
    await expect(page.locator('[data-led-legend]')).toContainText('Recovery system active');
    await expect(page.locator('[data-led-legend]')).toContainText('Magenta, fast blink');
    expect(await fitsWindow(page)).toBe(true);
    expect(errors, errors.join('\n')).toEqual([]);
});

test('a switch and Save send the configuration', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info);
    await page.goto('/system/led');
    const save = page.getByRole('button', {name: 'Save'});
    await expect(save).toBeDisabled();
    await page.locator('[data-led-simple="no-network"] input[type="checkbox"]').uncheck();
    await page.getByRole('radio', {name: 'Green'}).click();
    await expect(save).toBeEnabled();
    const put = page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/api/system/v1/led'));
    await save.click();
    const body = (await put).postDataJSON();
    expect(body.states.find((s: {id: string}) => s.id === 'no-network').enabled).toBe(false);
    expect(body.normal).toEqual({color: 'green', pattern: 'solid'});
    await expect(save).toBeDisabled();
    await page.reload();
    await expect(page.locator('[data-led-simple="no-network"] input[type="checkbox"]')).not.toBeChecked();
});

test('switching the LED off asks first', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info);
    await page.goto('/system/led');
    const box = page.locator('[data-led="enabled"]');
    await box.uncheck();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('The LED will stay dark, errors included.');
    await dialog.getByRole('button', {name: 'Cancel'}).click();
    await expect(box).toBeChecked();
    await expect(page.getByRole('button', {name: 'Save'})).toBeDisabled();
    await box.uncheck();
    await page.getByRole('dialog').getByRole('button', {name: 'Switch off'}).click();
    await expect(box).not.toBeChecked();
    await expect(page.getByRole('button', {name: 'Save'})).toBeEnabled();
});

test('advanced: a row moves by its handle, fixed rows have none, Test plays a row', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info);
    await page.goto('/system/led');
    await page.locator('[data-led-advanced] summary').click();
    const order = () => page.locator('[data-led-row]').evaluateAll((els) => els.map((e) => e.getAttribute('data-led-row')));
    expect((await order()).slice(0, 3)).toEqual(['radio-down', 'no-network', 'service-failed']);
    await page.getByRole('button', {name: 'Move No network'}).press('ArrowDown');
    await expect.poll(async () => (await order()).slice(0, 3)).toEqual(['radio-down', 'service-failed', 'no-network']);
    // the first row goes no higher
    await page.getByRole('button', {name: 'Move Radio or interface down'}).press('ArrowUp');
    expect((await order()).slice(0, 3)).toEqual(['radio-down', 'service-failed', 'no-network']);
    for (const id of ['shutdown', 'booting', 'locate', 'normal']) {
        await expect(page.locator(`[data-led-fixed="${id}"] button`)).toHaveCount(0);
    }
    // a colour, a pattern that needs a second colour, and Test sends exactly that look
    const row = page.locator('[data-led-row="status-warning"]');
    await row.getByLabel('Colour of Status page warnings').selectOption('yellow');
    await row.getByLabel('Pattern of Status page warnings').selectOption('alternate');
    await expect(row.getByLabel('Second colour of Status page warnings')).toHaveValue('blue');
    const preview = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/led/preview'));
    await row.getByRole('button', {name: 'Test'}).click();
    expect((await preview).postDataJSON()).toEqual({color: 'yellow', pattern: 'alternate', color2: 'blue'});
    await expect(page.locator('[data-led-now]')).toHaveAttribute('data-led-now', 'preview');
    // the disclosure is remembered per browser; Reset brings the defaults back into the draft
    await page.reload();
    await expect(page.locator('[data-led-advanced]')).toHaveAttribute('open', '');
    await page.getByRole('button', {name: 'Move No network'}).press('ArrowDown');
    await expect(page.getByRole('button', {name: 'Save'})).toBeEnabled();
    await page.getByRole('button', {name: 'Reset to defaults'}).click();
    await expect(page.getByRole('button', {name: 'Save'})).toBeDisabled();
});

// task 134: a blinking state can blink over the normal colour instead of over dark, opt-in per row
test('advanced: blinking over the normal colour is offered for the four patterns, off by default but for interfaces-starting, saved and reloaded', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info);
    const errors = watchErrors(page);
    await page.goto('/system/led');
    await page.locator('[data-led-advanced] summary').click();
    const over = (id: string) => page.locator(`[data-led-over="${id}"]`);
    const box = (id: string) => over(id).getByRole('checkbox', {name: 'Blink over the normal colour'});
    // off by default wherever it is offered; not offered for solid, nor for the external row
    for (const id of ['no-network', 'service-failed', 'storage-replace', 'status-warning', 'system-update', 'addon-update']) {
        await expect(box(id)).not.toBeChecked();
    }
    // task 158: interfaces-starting is the one default that blinks over the normal colour -
    // double magenta over blue, below the errors and above the warnings
    await expect(box('interfaces-starting')).toBeChecked();
    const row = page.locator('[data-led-row="interfaces-starting"]');
    await expect(row).toContainText('Interfaces starting');
    await expect(row.getByLabel('Colour of Interfaces starting')).toHaveValue('magenta');
    await expect(row.getByLabel('Pattern of Interfaces starting')).toHaveValue('double');
    const ids = await page.locator('[data-led-row]').evaluateAll((els) => els.map((e) => e.getAttribute('data-led-row')));
    expect(ids.indexOf('interfaces-starting')).toBe(ids.indexOf('storage-replace') + 1);
    expect(ids.indexOf('interfaces-starting')).toBeLessThan(ids.indexOf('status-warning'));
    await expect(over('radio-down')).toHaveCount(0);
    await expect(over('external')).toHaveCount(0);
    // the four patterns only
    const radio = page.locator('[data-led-row="radio-down"]');
    for (const p of ['slow', 'fast', 'flash', 'double']) {
        await radio.getByLabel('Pattern of Radio or interface down').selectOption(p);
        await expect(box('radio-down')).toBeVisible();
    }
    await radio.getByLabel('Pattern of Radio or interface down').selectOption('alternate');
    await expect(over('radio-down')).toHaveCount(0);
    await radio.getByLabel('Pattern of Radio or interface down').selectOption('solid');
    await expect(over('radio-down')).toHaveCount(0);
    await expect(page.getByRole('button', {name: 'Save'})).toBeDisabled();
    // blue fast over blue would not be seen: the page says so instead of offering it
    await expect(over('no-internet')).toHaveText('Blinking over the normal colour is not offered: it is the same colour, the blink would not be seen.');
    await expect(over('no-internet').getByRole('checkbox')).toHaveCount(0);
    // on, the texts and Test say it
    await box('status-warning').check();
    await expect(page.locator('[data-led-simple="status-warning"]')).toContainText('Yellow, slow blink over blue');
    await expect(page.locator('[data-led-simple="status-warning"] .ol-led-swatch')).toHaveAttribute('data-background', 'blue');
    const preview = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/led/preview'));
    await page.locator('[data-led-row="status-warning"]').getByRole('button', {name: 'Test'}).click();
    expect((await preview).postDataJSON()).toEqual({color: 'yellow', pattern: 'slow', over_normal: true});
    await expect(page.locator('[data-led-now]')).toContainText('Yellow, slow blink over blue');
    // saved with the row alone, and there after a reload
    const put = page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/api/system/v1/led'));
    await page.getByRole('button', {name: 'Save'}).click();
    const states = (await put).postDataJSON().states as {id: string; over_normal?: boolean}[];
    expect(states.filter((s) => s.over_normal).map((s) => s.id)).toEqual(['interfaces-starting', 'status-warning']);
    await expect(page.getByRole('button', {name: 'Save'})).toBeDisabled();
    await page.reload();
    await expect(box('status-warning')).toBeChecked();
    await expect(box('system-update')).not.toBeChecked();
    // a pattern without a dark phase drops it; switched off, the draft equals the defaults again
    await page.locator('[data-led-row="status-warning"]').getByLabel('Pattern of Status page warnings').selectOption('solid');
    await expect(over('status-warning')).toHaveCount(0);
    await page.locator('[data-led-row="status-warning"]').getByLabel('Pattern of Status page warnings').selectOption('slow');
    await expect(box('status-warning')).not.toBeChecked();
    await page.getByRole('button', {name: 'Save'}).click();
    await expect(page.getByRole('button', {name: 'Save'})).toBeDisabled();
    await page.getByRole('button', {name: 'Reset to defaults'}).click();
    await expect(page.getByRole('button', {name: 'Save'})).toBeDisabled();
    // normal off: still offered (it takes effect once normal lights again), and the text says plain blinking
    await page.getByRole('radio', {name: 'Off'}).click();
    await box('system-update').check();
    await expect(page.locator('[data-led-simple="system-update"]')).toContainText('Cyan, slow blink ·');
    expect(await fitsWindow(page)).toBe(true);
    expect(errors, errors.join('\n')).toEqual([]);
});

// task 95 (D-67): the no-internet state connects to a host the box talks to anyway, and says which
test('the no-internet row names the host its check connects to', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info);
    await page.goto('/system/led');
    await page.locator('[data-led-advanced] summary').click();
    const line = page.locator('[data-led-row="no-internet"] [data-led-internet]');
    await expect(line).toHaveText('the system has an address but reaches none of the hosts it talks to anyway: catalogue.example.org');
    await expect(page.locator('[data-led-advanced]')).not.toContainText('google');
    expect(await fitsWindow(page)).toBe(true);
});

test('in German: no host to check against', async ({page, baseURL}, info) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await plant(page, baseURL!, info, {'stub-led-internet': 'none'});
    await page.goto('/system/led');
    await page.locator('[data-led-advanced] summary').click();
    await expect(page.locator('[data-led-row="no-internet"] [data-led-internet]')).toHaveText('nichts zum Prüfen: der Addon-Katalog ist ausgeschaltet oder lokal, und ACME ist nicht eingerichtet');
});

test('Locate calls locate, and stops', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info);
    await page.goto('/system/led');
    const post = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/system/v1/led/locate'));
    await page.getByRole('button', {name: 'Locate'}).click();
    await post;
    await expect(page.locator('[data-led-now]')).toHaveAttribute('data-led-now', 'locate');
    await expect(page.locator('[data-led-now]')).toContainText('White, fast blink');
    const del = page.waitForRequest((r) => r.method() === 'DELETE' && r.url().endsWith('/api/system/v1/led/locate'));
    await page.getByRole('button', {name: 'Stop locating'}).click();
    await del;
    await expect(page.locator('[data-led-now]')).toHaveAttribute('data-led-now', 'normal');
});

test('a box without a status LED says so and offers the power LED and the board LEDs', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info, {'stub-led-hw': 'none'});
    await page.goto('/system/led');
    await expect(page.locator('[data-notice="no-led"]')).toContainText('This system has no status LED. The radio module has no status LED.');
    // task 203: the notice is a panel like the rest of the UI, and a plain one has no left edge
    const plain = await page.locator('[data-notice="no-led"]').evaluate((el) => {
        const cs = getComputedStyle(el);
        return {panel: el.classList.contains('ol-panel'), severity: el.getAttribute('data-severity'), widths: [cs.borderTopWidth, cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth]};
    });
    expect(plain.panel).toBe(true);
    expect(plain.severity).toBe(null);
    expect(plain.widths).toEqual(['1px', '1px', '1px', '1px']);
    await expect(page.locator('[data-led-now]')).toHaveCount(0);
    const pwr = page.locator('[data-led="pwr"]');
    await pwr.check();
    const put = page.waitForRequest((r) => r.method() === 'PUT' && r.url().endsWith('/api/system/v1/led'));
    await page.getByRole('button', {name: 'Save'}).click();
    expect((await put).postDataJSON().pwr_error_light).toBe(true);
    await expect(page.getByRole('heading', {level: 2, name: 'Board LEDs'})).toBeVisible();
    await expect(page.getByRole('button', {name: 'Switch the onboard LEDs off'})).toBeVisible();
});

test('German on a phone: every text translated, nothing wider than the screen', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info, {'stub-led-active': 'radio-down'});
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.setViewportSize({width: 390, height: 844});
    await page.goto('/system/led');
    await expect(page.getByRole('heading', {level: 1, name: 'Statusleuchte'})).toBeVisible();
    await expect(page.locator('[data-led-now]')).toContainText('Rot, dauerhaft');
    await page.locator('[data-led-advanced] summary').click();
    await page.locator('[data-led-legend] summary').click();
    await expect(page.getByRole('button', {name: 'Finden'})).toBeVisible();
    await expect(page.locator('[data-led-over="status-warning"]')).toContainText('Über der Normalfarbe blinken');
    expect(await fitsWindow(page)).toBe(true);
});

test('a user reads the page and changes nothing', async ({page, baseURL}, info) => {
    await plant(page, baseURL!, info);
    await page.route('**/api/auth/v1/state', (r) => r.fulfill({json: USER}));
    await page.goto('/system/led');
    await expect(page.locator('[data-led-now]')).toBeVisible();
    await expect(page.locator('[data-led="enabled"]')).toBeDisabled();
    await expect(page.getByRole('button', {name: 'Locate'})).toHaveCount(0);
    await expect(page.getByRole('button', {name: 'Save'})).toHaveCount(0);
});
