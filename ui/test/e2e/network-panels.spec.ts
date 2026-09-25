import {expect, test} from '@playwright/test';

// The maintainer, 2026-09-20: the Network page's name resolution and time are two panels of their
// own - the hostname and the DNS in one, the clock in the other with the browser's time in one
// click - and the LED line is gone.

test('the name resolution panel: the full name, the hostname field and the DNS in use', async ({page}) => {
    const panel = page.locator('[data-panel="name-resolution"]');
    await page.goto('/system/network');
    await expect(panel.getByRole('heading', {name: 'Name resolution'})).toBeVisible();
    await expect(panel.locator('[data-fqdn]')).toHaveText('openccu.home.arpa');
    await expect(panel.getByLabel('Hostname')).toHaveValue('openccu');
    await expect(panel.locator('[data-dns-current]')).toContainText('192.0.2.1');
    await expect(panel.getByLabel('DNS servers')).toBeVisible();
    // the old panel and the old sections are gone
    await expect(page.getByRole('heading', {name: 'Name resolution and time'})).toHaveCount(0);
    await expect(page.getByRole('heading', {name: 'LEDs'})).toHaveCount(0);
});

test('the time panel syncs the clock to the browser', async ({page}) => {
    const posts: string[] = [];
    page.on('request', (r) => {
        if (r.method() === 'POST' && r.url().includes('/api/system/v1/time/clock')) posts.push(String(r.postData()));
    });
    await page.goto('/system/network');
    const panel = page.locator('[data-panel="time"]');
    await expect(panel.getByRole('heading', {name: 'Time'})).toBeVisible();
    await expect(panel.locator('[data-system-time]')).not.toBeEmpty();
    await panel.locator('[data-action="sync-browser"]').click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText("Set the system clock to this browser's time");
    await dialog.getByRole('button', {name: 'Set'}).click();
    await expect.poll(() => posts.length).toBe(1);
    expect(JSON.parse(posts[0]!).time).toMatch(/^\d{4}-\d\d-\d\dT/);
    await expect(page.locator('.ol-notice', {hasText: 'System clock set.'})).toBeVisible();
});

// openccu-lite task 221 (the maintainer, 2026-09-24): an IPv4 and an IPv6 panel per interface, the
// width of the interface panel and directly below it; on a phone eth0, IPv4 (eth0), IPv6 (eth0),
// wlan0, IPv4 (wlan0), IPv6 (wlan0). The name resolution and time panels in the same tracks, their
// labels, values and inputs on one grid.
const box = async (page: import('@playwright/test').Page, sel: string) => (await page.locator(sel).first().boundingBox())!;

test('the columns: each interface, its IPv4, its IPv6 - aligned, the same width, in the stated order', async ({page}, info) => {
    await page.goto('/system/network');
    await expect(page.locator('[data-panel="ipv6"][data-of="wlan0"]')).toBeVisible();
    const order = await page.locator('.ol-ifcol > .ol-card').evaluateAll((els) => els.map((e) => e.getAttribute('data-panel') ? `${e.getAttribute('data-panel')}:${e.getAttribute('data-of')}` : e.getAttribute('data-iface')));
    // task 226: eth1 (no cable, no address) and veth0 (virtual, no address) are their panels alone
    expect(order).toEqual(['eth0', 'ipv4:eth0', 'ipv6:eth0', 'wlan0', 'ipv4:wlan0', 'ipv6:wlan0', 'eth1', 'veth0']);
    for (const name of ['eth0', 'wlan0']) {
        const i = await box(page, `.ol-ifcol > [data-iface="${name}"]`);
        const v4 = await box(page, `[data-panel="ipv4"][data-of="${name}"]`);
        const v6 = await box(page, `[data-panel="ipv6"][data-of="${name}"]`);
        for (const p of [v4, v6]) {
            expect(Math.abs(p.x - i.x), `${name}: left edge`).toBeLessThan(1);
            expect(Math.abs(p.width - i.width), `${name}: width`).toBeLessThan(1);
        }
        expect(v4.y, `${name}: IPv4 below`).toBeGreaterThan(i.y + i.height);
        expect(v6.y, `${name}: IPv6 below IPv4`).toBeGreaterThan(v4.y + v4.height);
    }
    const eth0 = await box(page, '.ol-ifcol > [data-iface="eth0"]');
    const wlan0 = await box(page, '.ol-ifcol > [data-iface="wlan0"]');
    if (info.project.name === 'phone') {
        expect(Math.abs(wlan0.x - eth0.x)).toBeLessThan(1);
    } else {
        // two columns, and their IPv4 panels start at one height although the Wi-Fi panel is taller
        expect(Math.abs(wlan0.y - eth0.y)).toBeLessThan(1);
        expect(wlan0.x).toBeGreaterThan(eth0.x + eth0.width);
        const a = await box(page, '[data-panel="ipv4"][data-of="eth0"]');
        const b = await box(page, '[data-panel="ipv4"][data-of="wlan0"]');
        expect(Math.abs(a.y - b.y)).toBeLessThan(1);
    }
});

test('name resolution and time: the interface column width, inputs on one edge per panel', async ({page}) => {
    await page.goto('/system/network');
    const col = await box(page, '.ol-ifcol > [data-iface="eth0"]');
    for (const panel of ['name-resolution', 'time']) {
        const p = await box(page, `[data-panel="${panel}"]`);
        expect(Math.abs(p.width - col.width), `${panel}: width`).toBeLessThan(1);
        // every field and every read-only value starts at one x
        const lefts = await page.locator(`[data-panel="${panel}"] .ol-field-value`).evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().left)));
        expect(new Set(lefts).size, `${panel}: ${lefts.join(', ')}`).toBe(1);
        const inputs = await page.locator(`[data-panel="${panel}"] .ol-field-row > :is(input, select)`).evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().left)));
        expect(new Set(inputs).size, `${panel} inputs: ${inputs.join(', ')}`).toBe(1);
    }
    // an explanation keeps its distance from the field above it
    const field = await box(page, '#net-dns-override');
    const hint = await box(page, '[data-panel="name-resolution"] .ol-field-hint');
    expect(hint.y - (field.y + field.height)).toBeGreaterThanOrEqual(5);
});

test('in German the longer labels keep the grid', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    await page.goto('/system/network');
    await expect(page.locator('[data-panel="time"]')).toContainText('Zeitzone');
    for (const panel of ['name-resolution', 'time']) {
        const lefts = await page.locator(`[data-panel="${panel}"] .ol-field-value`).evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().left)));
        expect(new Set(lefts).size, `${panel}: ${lefts.join(', ')}`).toBe(1);
    }
    await expect(page.locator('[data-iface="eth1"] .ol-pill')).toHaveText('kein Kabel');
});
