import {expect, test, type Page} from '@playwright/test';
import {fitsWindow} from './scroll';

// The Network page's panels: one per interface with its link, its addresses and a state pill -
// since task 89 (the maintainer, 2026-09-19) the Ethernet interface netconfig configures first, the
// Wi-Fi panel beside it, then the rest; an IPv6 panel; a pulled cable showing while the page is
// open; a container's veth; and the phone layout.

function collectErrors(page: Page): string[] {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
    page.on('console', (m) => {
        if (m.type() === 'error') errors.push(`console: ${m.text()}`);
    });
    return errors;
}

test('one panel per interface, Ethernet and Wi-Fi first, with link, addresses and state', async ({page}) => {
    const errors = collectErrors(page);
    await page.goto('/system/network');
    const panels = page.locator('.ol-ifcol > [data-iface]');
    await expect(panels).toHaveCount(4);
    expect(await panels.evaluateAll((els) => els.map((e) => e.getAttribute('data-iface')))).toEqual(['eth0', 'wlan0', 'eth1', 'veth0']);

    const eth0 = page.locator('[data-iface="eth0"]');
    await expect(eth0.locator('.ol-pill')).toHaveText('up');
    await expect(eth0.locator('.ol-linkline')).toHaveText('1000 Mbit/s · full duplex');
    await expect(eth0).toContainText('dc:a6:32:01:02:03');
    await expect(eth0).toContainText('bcmgenet');
    await expect(eth0.locator('.ol-traffic')).toHaveText('received 1.2 GB · sent 98.8 MB');
    // task 221: the addresses are the IPv4 and IPv6 panels' below the interface's
    await expect(eth0).not.toContainText('10.10.0.2/24');
    const v4 = page.locator('[data-panel="ipv4"][data-of="eth0"]');
    await expect(v4.locator('[data-addrs="ipv4"]')).toHaveText('10.10.0.2/24');
    const v6eth0 = page.locator('[data-panel="ipv6"][data-of="eth0"]');
    // IPv6 grouped by scope, the temporary address marked and muted
    expect(await v6eth0.locator('dt[data-scope]').evaluateAll((els) => els.map((e) => e.getAttribute('data-scope')))).toEqual(['global', 'unique-local', 'link-local']);
    const temporary = v6eth0.locator('[data-addr="2001:db8:1:0:1234:5678:abcd:ef01"]');
    await expect(temporary).toContainText('temporary');
    await expect(temporary).toContainText('deprecated');
    await expect(temporary).toHaveClass(/ol-muted/);
    await expect(v6eth0.locator('[data-addr="2001:db8:1::119"]')).not.toHaveClass(/ol-muted/);

    // the domain from resolv.conf beside the host name, which comes first
    await expect(page.locator('[data-panel="name-resolution"] [data-fqdn]')).toContainText('home.arpa');
    const wlan0 = page.locator('[data-iface="wlan0"]');
    await expect(wlan0.locator('.ol-card-sub')).toHaveText('Wi-Fi · default route');
    // the Wi-Fi panel (task 89) with the interface's own details beside its state
    await expect(wlan0.locator('.ol-pill')).toHaveText('off');
    await expect(wlan0).not.toContainText('IPv4');
    await expect(page.locator('[data-panel="ipv4"][data-of="wlan0"] [data-addrs="ipv4"]')).toHaveText('192.0.2.119/24');
    await expect(page.locator('[data-iface="eth1"] .ol-pill')).toHaveText('no cable');
    await expect(page.locator('[data-iface="eth1"] .ol-linkline')).toHaveText('no link');
    await expect(page.locator('[data-iface="veth0"] .ol-linkline')).toHaveText('virtual');

    // the system-wide IPv6 panel went (task 221): the gateway and the DNS on the interface they
    // leave by, SLAAC per interface, the switch in every IPv6 panel's pill
    await expect(v6eth0.locator('.ol-pill')).toHaveText('on');
    await expect(v6eth0.locator('[data-v6-gateway]')).toHaveText('fe80::1');
    await expect(v6eth0).toContainText('fd00::1');
    await expect(v6eth0.locator('[data-slaac]')).toHaveText('router advertisements accepted');
    await expect(page.locator('[data-panel="ipv6"][data-of="wlan0"] [data-v6-gateway]')).toHaveCount(0);
    await expect(page.locator('[data-panel="ipv6"]:not([data-of])')).toHaveCount(0);
    // task 226: an inactive interface (no cable, no address) and a virtual one without addresses
    // are their panels alone
    for (const name of ['eth1', 'veth0']) await expect(page.locator(`[data-panel^="ipv"][data-of="${name}"]`)).toHaveCount(0);
    // the interfaces table is gone; and so is the firewall, a page of its own since task 57
    await expect(page.getByRole('columnheader', {name: 'MAC'})).toHaveCount(0);
    await expect(page.getByRole('heading', {level: 2, name: 'Firewall'})).toHaveCount(0);
    await expect(page.getByRole('heading', {level: 3, name: 'Addon ports'})).toHaveCount(0);
    expect(errors, errors.join('\n')).toEqual([]);
});

test('the current rate shows under the totals once two polls are in', async ({page}) => {
    // every answer carries 750 kB more received and 50 kB more sent on eth0: over the page's 5 s
    // poll that is 1.2 Mbit/s down and 80 kbit/s up, give or take the poll's timing
    let answers = 0;
    await page.route('**/api/system/v1/network', async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        const eth0 = body.network.interfaces.find((i: {name: string}) => i.name === 'eth0');
        eth0.statistics.rx_bytes += answers * 750_000;
        eth0.statistics.tx_bytes += answers * 50_000;
        answers++;
        await route.fulfill({response, json: body});
    });
    await page.goto('/system/network');
    const eth0 = page.locator('[data-iface="eth0"]');
    await expect(eth0.locator('.ol-traffic')).toHaveText('received 1.2 GB · sent 98.8 MB');
    // before the second answer the line holds its place with dashes
    await expect(eth0.locator('.ol-rate')).toHaveText('↓ – ↑ –');
    await expect(eth0.locator('.ol-rate')).toHaveText(/^↓ 1\.[12] Mbit\/s ↑ (7[5-9]|8[0-4]) kbit\/s$/, {timeout: 15_000});
    await expect(eth0.locator('.ol-rate')).not.toHaveClass(/ol-muted/);
    await expect(eth0.locator('.ol-rate')).toHaveAttribute('title', /^receiving 1\.[12] Mbit\/s, sending \d+ kbit\/s right now$/);
    // an interface without counters has neither line
    await expect(page.locator('[data-iface="eth1"] .ol-rate')).toHaveCount(0);
});

test('a pulled cable shows while the page is open', async ({page}) => {
    let answers = 0;
    await page.route('**/api/system/v1/network', async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        if (answers++ > 0) {
            const eth0 = body.network.interfaces.find((i: {name: string}) => i.name === 'eth0');
            Object.assign(eth0, {up: false, operstate: 'down', carrier: false});
            delete eth0.speed;
            delete eth0.duplex;
        }
        await route.fulfill({response, json: body});
    });
    await page.goto('/system/network');
    const eth0 = page.locator('[data-iface="eth0"]');
    await expect(eth0.locator('.ol-pill')).toHaveText('up');
    await expect(eth0.locator('.ol-pill')).toHaveText('no cable', {timeout: 15_000});
    await expect(eth0.locator('.ol-linkline')).toHaveText('no link');
});

test("a container's veth says virtual and managed by the host", async ({page}) => {
    await page.route('**/api/system/v1/network', async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        body.host_managed = true;
        body.writable = false;
        body.network.interfaces = [{name: 'eth0', mac: 'bc:24:11:aa:bb:cc', up: true, operstate: 'up', carrier: true, speed: 10000, duplex: 'full', mtu: 1500, kind: 'veth', default_route: true, ipv4: [{address: '192.0.2.50', prefix: 24}], ipv6: []}];
        await route.fulfill({response, json: body});
    });
    await page.goto('/system/network');
    const eth0 = page.locator('[data-iface="eth0"]');
    await expect(eth0.locator('.ol-linkline')).toHaveText('virtual · managed by the host');
    await expect(eth0).not.toContainText('Mbit/s');
});

test('the panels of a row share its height and stack on a phone', async ({page}, info) => {
    await page.goto('/system/network');
    // the maintainer, 2026-09-20: the name resolution and the time are panels of their own; task
    // 221: the interface columns' panels and the system panels share their rows' heights
    await expect(page.locator('[data-panel="name-resolution"]')).toBeVisible();
    await expect(page.locator('[data-panel="time"]')).toBeVisible();
    await expect(page.locator('[data-panel="ipv4"][data-of="wlan0"]')).toBeVisible();
    for (const grid of ['.ol-ifaces:not(.ol-syspanels) > .ol-ifcol', '.ol-syspanels']) {
        const boxes = await page.locator(`${grid} > .ol-card`).evaluateAll((els) => els.map((e) => {
            const r = e.getBoundingClientRect();
            return {left: Math.round(r.left), top: Math.round(r.top), height: Math.round(r.height)};
        }));
        expect(boxes.length).toBeGreaterThan(1);
        const rows = new Map<number, number[]>();
        for (const b of boxes) rows.set(b.top, [...(rows.get(b.top) ?? []), b.height]);
        for (const hs of rows.values()) expect(new Set(hs).size, `${grid}: ${hs.join(', ')}`).toBe(1);
        if (info.project.name === 'phone') expect(new Set(boxes.map((b) => b.left)).size, `${grid} stacks`).toBe(1);
    }
    expect(await fitsWindow(page)).toBe(true);
});
