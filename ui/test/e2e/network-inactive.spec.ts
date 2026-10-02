import {type Page} from '@playwright/test';
import {expect, test} from './fixtures';

// openccu-lite task 226 (the maintainer, 2026-09-24: "don't show IPv4/6 panel for an
// inactive/disabled interface"): an interface that is administratively down, has neither cable
// nor address, is a virtual link without addresses, or is the Wi-Fi while Wi-Fi is switched off,
// is its interface panel alone. The configured interface (eth0) keeps its IPv4 panel with the form
// also without a cable; an interface up with a link but no address yet (a DHCP wait) keeps both.

const v4 = (a: string) => [{address: a, prefix: 24}];
const ll = [{address: 'fe80::1', prefix: 64, scope: 'link-local', flags: ['permanent']}];

interface Iface { name: string; up: boolean; operstate: string; carrier?: boolean; kind: string; ipv4?: unknown[]; ipv6?: unknown[]; default_route?: boolean }

async function withInterfaces(page: Page, interfaces: Iface[], wifiEnabled = true, chips = true) {
    await page.route('**/api/system/v1/network', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const res = await route.fetch();
        const body = await res.json();
        body.network.interfaces = interfaces.map((i) => ({mtu: 1500, ipv4: [], ipv6: [], ...i}));
        await route.fulfill({json: body});
    });
    await page.route('**/api/system/v1/wifi', async (route) => {
        if (route.request().method() !== 'GET') return route.fallback();
        const res = await route.fetch();
        const body = await res.json();
        body.settings = {...body.settings, enabled: wifiEnabled};
        if (!chips) body.chips = [];
        await route.fulfill({json: body});
    });
}

// B-292: the page loaded with both routed answers in. A test whose assertion held once /network
// had answered ended while /wifi was still in its route handler, and route.fetch then threw "Test
// ended" into the worker; it could also judge the Wi-Fi panels before the Wi-Fi state had arrived.
async function openNetwork(page: Page) {
    const answered = (path: string) => page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname === path);
    const network = answered('/api/system/v1/network');
    const wifi = answered('/api/system/v1/wifi');
    await page.goto('/system/network');
    await Promise.all([network, wifi]);
}

const panelsOf = (page: Page, name: string) => page.locator(`[data-panel^="ipv"][data-of="${name}"]`);

test('the cases: which interface keeps its address panels', async ({page}) => {
    await withInterfaces(page, [
        // the configured interface without a cable: its form stays
        {name: 'eth0', up: false, operstate: 'down', carrier: false, kind: 'ethernet'},
        // up with a link, no address yet: a DHCP wait keeps the panels
        {name: 'eth1', up: true, operstate: 'up', carrier: true, kind: 'ethernet', ipv6: ll},
        // administratively down, even with an address left on it
        {name: 'eth2', up: false, operstate: 'down', kind: 'ethernet', ipv4: v4('10.0.2.1')},
        // no cable and no address
        {name: 'eth3', up: false, operstate: 'down', carrier: false, kind: 'ethernet', ipv6: ll},
        // a virtual link without addresses, and one with an address
        {name: 'veth0', up: true, operstate: 'up', carrier: true, kind: 'veth'},
        {name: 'br0', up: true, operstate: 'up', carrier: true, kind: 'bridge', ipv4: v4('10.0.9.1')},
        // Wi-Fi switched off
        {name: 'wlan0', up: false, operstate: 'down', kind: 'wireless'},
    ], false);
    await openNetwork(page);
    await expect(page.locator('[data-iface="br0"]')).toBeVisible();
    await expect(panelsOf(page, 'eth0')).toHaveCount(2);
    await expect(page.locator('[data-panel="ipv4"][data-of="eth0"] #net-mode')).toBeVisible();
    await expect(panelsOf(page, 'eth1')).toHaveCount(2);
    await expect(panelsOf(page, 'br0')).toHaveCount(2);
    for (const name of ['eth2', 'eth3', 'veth0', 'wlan0']) {
        await expect(page.locator(`[data-iface="${name}"]`)).toBeVisible();
        await expect(panelsOf(page, name), name).toHaveCount(0);
    }
});

test('Wi-Fi on and connected: its panels are back', async ({page}) => {
    await withInterfaces(page, [
        {name: 'eth0', up: true, operstate: 'up', carrier: true, kind: 'ethernet', ipv4: v4('10.0.0.2'), default_route: true},
        {name: 'wlan0', up: true, operstate: 'up', carrier: true, kind: 'wireless', ipv4: v4('10.0.1.2'), ipv6: ll},
    ], true);
    await openNetwork(page);
    await expect(panelsOf(page, 'wlan0')).toHaveCount(2);
});

test('the layout: a column that is its panel alone leaves no hole, the next row starts below the taller column', async ({page}, info) => {
    await withInterfaces(page, [
        {name: 'eth0', up: true, operstate: 'up', carrier: true, kind: 'ethernet', ipv4: v4('10.0.0.2'), default_route: true},
        {name: 'eth1', up: false, operstate: 'down', carrier: false, kind: 'ethernet'},
        {name: 'eth2', up: true, operstate: 'up', carrier: true, kind: 'ethernet', ipv4: v4('10.0.2.2')},
        {name: 'eth3', up: false, operstate: 'down', carrier: false, kind: 'ethernet'},
    ], true, false);
    await openNetwork(page);
    await expect(page.locator('[data-panel="ipv6"][data-of="eth2"]')).toBeVisible();
    const box = async (sel: string) => (await page.locator(sel).first().boundingBox())!;
    const eth0 = await box('[data-iface="eth0"]');
    const v6eth0 = await box('[data-panel="ipv6"][data-of="eth0"]');
    const eth1 = await box('[data-iface="eth1"]');
    const eth2 = await box('[data-iface="eth2"]');
    const v4eth2 = await box('[data-panel="ipv4"][data-of="eth2"]');
    const eth3 = await box('[data-iface="eth3"]');
    if (info.project.name === 'phone') {
        // stacked: eth0, IPv4, IPv6, eth1, eth2, IPv4, IPv6, eth3 - 12 px between any two panels
        expect(Math.round(eth1.y - (v6eth0.y + v6eth0.height))).toBe(12);
        expect(Math.round(eth2.y - (eth1.y + eth1.height))).toBe(12);
        expect(Math.round(v4eth2.y - (eth2.y + eth2.height))).toBe(12);
    } else {
        // eth0 | eth1 in the first row group, eth2 | eth3 in the second, starting below eth0's IPv6
        expect(Math.abs(eth1.y - eth0.y)).toBeLessThan(1);
        expect(Math.abs(eth3.y - eth2.y)).toBeLessThan(1);
        expect(Math.round(eth2.y - (v6eth0.y + v6eth0.height))).toBe(12);
        // the IPv4 panels below the interface panels of a row start 12 px below the taller one
        expect(Math.round(v4eth2.y - (Math.max(eth2.y + eth2.height, eth3.y + eth3.height)))).toBe(12);
    }
});
