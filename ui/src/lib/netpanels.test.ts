import {describe, expect, it} from 'vitest';
import {formatBytes, formatRate, groupIPv6, interfaceColumns, ipv6GatewayOf, linkLine, linkState, orderInterfaces, quietAddress, sampleOf, showsAddressPanels, shownFlags, trafficRates, type NetIface} from './netpanels';

const iface = (name: string, extra: Partial<NetIface> = {}): NetIface => ({name, up: true, ...extra});

describe('the interface panels', () => {
    it('put the interface with the default route first, the rest by name', () => {
        const list = [iface('eth1'), iface('wlan0', {default_route: true}), iface('eth0'), iface('veth0')];
        expect(orderInterfaces(list).map((i) => i.name)).toEqual(['wlan0', 'eth0', 'eth1', 'veth0']);
        expect(list[0]!.name).toBe('eth1'); // a copy, the answer itself is left alone
    });

    it('tell up, no cable and down apart', () => {
        expect(linkState(iface('eth0', {carrier: true}))).toBe('up');
        expect(linkState(iface('eth0', {up: false, carrier: false}))).toBe('no-cable');
        expect(linkState(iface('eth0', {up: false}))).toBe('down');
    });

    it('show a speed only for a wired link that has one', () => {
        expect(linkLine(iface('eth0', {kind: 'ethernet', speed: 1000, duplex: 'full'}))).toEqual({kind: 'speed', speed: 1000, duplex: 'full'});
        expect(linkLine(iface('eth1', {kind: 'ethernet'}))).toEqual({kind: 'none'});
        expect(linkLine(iface('wlan0', {kind: 'wireless', speed: 72}))).toEqual({kind: 'wireless'});
        expect(linkLine(iface('eth0', {kind: 'veth', speed: 10000}))).toEqual({kind: 'virtual'});
        expect(linkLine(iface('br0', {kind: 'bridge'}))).toEqual({kind: 'virtual'});
    });

    it('group the IPv6 addresses global, unique-local, link-local', () => {
        const groups = groupIPv6([
            {address: 'fe80::1', prefix: 64, scope: 'link-local'},
            {address: '2001:db8::1', prefix: 64, scope: 'global'},
            {address: 'fd00::1', prefix: 64, scope: 'unique-local'},
            {address: '2001:db8::2', prefix: 64, scope: 'global', flags: ['temporary', 'deprecated']},
        ]);
        expect(groups.map((g) => g.scope)).toEqual(['global', 'unique-local', 'link-local']);
        expect(groups[0]!.addrs.map((a) => a.address)).toEqual(['2001:db8::1', '2001:db8::2']);
        expect(groupIPv6(undefined)).toEqual([]);
    });

    it('mute temporary and deprecated addresses and leave permanent unmarked', () => {
        expect(quietAddress({address: 'x', prefix: 64, flags: ['temporary']})).toBe(true);
        expect(quietAddress({address: 'x', prefix: 64, flags: ['deprecated']})).toBe(true);
        expect(quietAddress({address: 'x', prefix: 64, flags: ['permanent']})).toBe(false);
        expect(shownFlags({address: 'x', prefix: 64, flags: ['tentative', 'permanent']})).toEqual(['tentative']);
    });

    it('format the traffic counters', () => {
        expect(formatBytes(0)).toBe('0 B');
        expect(formatBytes(999)).toBe('999 B');
        expect(formatBytes(1234)).toBe('1.2 kB');
        expect(formatBytes(1234567890)).toBe('1.2 GB');
    });

    it('compute the rate between two samples in bits per second', () => {
        const stats = (rx: number, tx: number) => ({rx_bytes: rx, tx_bytes: tx, rx_errors: 0, tx_errors: 0, rx_dropped: 0, tx_dropped: 0});
        const first = sampleOf([iface('eth0', {statistics: stats(1_000_000, 50_000)}), iface('wlan0'), iface('eth1', {statistics: stats(900, 900)})], 10_000);
        expect(first.counters).toEqual({eth0: {rx: 1_000_000, tx: 50_000}, eth1: {rx: 900, tx: 900}});
        // five seconds later: 750 kB and 50 kB more on eth0, eth1's counters reset, veth0 new
        const second = sampleOf([iface('eth0', {statistics: stats(1_750_000, 100_000)}), iface('eth1', {statistics: stats(100, 100)}), iface('veth0', {statistics: stats(5, 5)})], 15_000);
        expect(trafficRates(first, second)).toEqual({eth0: {rx: 1_200_000, tx: 80_000}});
        expect(trafficRates(null, second)).toEqual({});
        expect(trafficRates(second, {...second, at: second.at})).toEqual({});
    });

    it.each([
        [0, '0 bit/s'],
        [999, '999 bit/s'],
        [80_000, '80 kbit/s'],
        [1_234, '1.2 kbit/s'],
        [9_960, '10 kbit/s'],
        [1_200_000, '1.2 Mbit/s'],
        [999_700, '1.0 Mbit/s'],
        [940_000_000, '940 Mbit/s'],
        [2_500_000_000, '2.5 Gbit/s'],
        [-5, '0 bit/s'],
    ])('format a rate of %d bit/s as %s', (bits, text) => {
        expect(formatRate(bits)).toBe(text);
    });
});

// openccu-lite task 221: a column per interface - the configured one, the Wi-Fi panel, the rest
describe('the columns', () => {
    const eth0: NetIface = {name: 'eth0', up: true, kind: 'ethernet', default_route: true};
    const eth1: NetIface = {name: 'eth1', up: false, kind: 'ethernet'};
    const wlan0: NetIface = {name: 'wlan0', up: true, kind: 'wireless'};
    const veth0: NetIface = {name: 'veth0', up: true, kind: 'veth'};
    const cols = (list: NetIface[], netconfig: string, chip: boolean) => interfaceColumns(list, netconfig, chip).map((c) => `${c.kind}:${c.name}`);

    it.each([
        ['the configured one, the Wi-Fi, the rest', [veth0, wlan0, eth1, eth0], 'eth0', true, ['iface:eth0', 'wifi:wlan0', 'iface:eth1', 'iface:veth0']],
        ['a Wi-Fi chip with its driver unloaded still has its column', [eth0], 'eth0', true, ['iface:eth0', 'wifi:wlan0']],
        ['without a chip a wireless interface is an interface like the rest', [wlan0, eth0], 'eth0', false, ['iface:eth0', 'iface:wlan0']],
        ['no configured interface: the Wi-Fi first', [eth1, wlan0], '', true, ['wifi:wlan0', 'iface:eth1']],
        ['a container: its veth', [veth0], 'eth0', false, ['iface:veth0']],
    ] as const)('%s', (_name, list, netconfig, chip, want) => {
        expect(cols([...list], netconfig, chip)).toEqual(want);
    });

    it('carries the live interface of the Wi-Fi column when there is one', () => {
        expect(interfaceColumns([eth0, wlan0], 'eth0', true)[1]!.iface).toBe(wlan0);
        expect(interfaceColumns([eth0], 'eth0', true)[1]!.iface).toBeUndefined();
    });

    it('names the IPv6 gateway on the interface it leaves by only', () => {
        const st = {available: true, enabled: true, gateway: 'fe80::1', gateway_interface: 'eth0', dns: []};
        expect(ipv6GatewayOf('eth0', st)).toBe('fe80::1');
        expect(ipv6GatewayOf('wlan0', st)).toBe('');
        expect(ipv6GatewayOf('eth0', undefined)).toBe('');
        expect(ipv6GatewayOf('eth0', {...st, gateway: undefined})).toBe('');
    });
});

// openccu-lite task 226: no IPv4/IPv6 panels for an inactive or disabled interface
describe('showsAddressPanels', () => {
    const v4 = [{address: '192.0.2.5', prefix: 24}];
    const ll = [{address: 'fe80::1', prefix: 64, scope: 'link-local'}];
    const gl = [{address: '2001:db8::5', prefix: 64, scope: 'global'}];
    it.each([
        ['up with addresses', {name: 'eth1', up: true, carrier: true, kind: 'ethernet', ipv4: v4}, {}, true],
        ['up with a global IPv6 alone', {name: 'eth1', up: true, carrier: true, kind: 'ethernet', ipv6: gl}, {}, true],
        ['up with a link, no address yet (a DHCP wait)', {name: 'eth1', up: true, carrier: true, kind: 'ethernet', ipv6: ll}, {}, true],
        ['no cable, no address', {name: 'eth1', up: false, carrier: false, kind: 'ethernet', ipv4: [], ipv6: []}, {}, false],
        ['no cable, only link-local left', {name: 'eth1', up: false, carrier: false, kind: 'ethernet', ipv6: ll}, {}, false],
        ['no cable, a static address still set', {name: 'eth1', up: false, carrier: false, kind: 'ethernet', ipv4: v4}, {}, true],
        ['administratively down', {name: 'eth1', up: false, kind: 'ethernet', ipv4: v4}, {}, false],
        ['Wi-Fi switched off', {name: 'wlan0', up: false, kind: 'wireless'}, {wifiOff: true}, false],
        ['Wi-Fi switched off, the link not yet down, no address', {name: 'wlan0', up: true, carrier: true, kind: 'wireless', ipv6: ll}, {wifiOff: true}, false],
        ['Wi-Fi on, associated, waiting for DHCP', {name: 'wlan0', up: true, carrier: true, kind: 'wireless', ipv6: ll}, {}, true],
        ['Wi-Fi on, not associated', {name: 'wlan0', up: false, carrier: false, kind: 'wireless'}, {}, false],
        ['virtual, up, no address', {name: 'veth0', up: true, carrier: true, kind: 'veth', ipv4: [], ipv6: []}, {}, false],
        ['virtual, up, with an address', {name: 'br0', up: true, carrier: true, kind: 'bridge', ipv4: v4}, {}, true],
        ['the configured interface without a cable', {name: 'eth0', up: false, carrier: false, kind: 'ethernet'}, {configured: true}, true],
        ['the configured interface down', {name: 'eth0', up: false, kind: 'ethernet'}, {configured: true}, true],
    ] as [string, NetIface, {configured?: boolean; wifiOff?: boolean}, boolean][])('%s: %s', (_, i, o, want) => {
        expect(showsAddressPanels(i, o)).toBe(want);
    });
    it('no live interface (the Wi-Fi driver unloaded): no panels', () => {
        expect(showsAddressPanels(undefined)).toBe(false);
        expect(showsAddressPanels(undefined, {configured: true})).toBe(true);
    });
});
