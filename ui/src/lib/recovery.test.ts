import {afterEach, describe, expect, it, vi} from 'vitest';
import type {NetIface} from './netpanels';
import {boxAddress, isIPLiteral, lookupBoxAddress, nameLink, urlOf, withTimeout, type NetworkView} from './recovery';

// the stub's Pi: Wi-Fi with the default route, a second LAN on eth0, a cable-less USB adapter
const wlan0: NetIface = {name: 'wlan0', up: true, default_route: true, ipv4: [{address: '192.0.2.119', prefix: 24}], ipv6: [{address: 'fe80::dea6:32ff:fe01:205', prefix: 64, scope: 'link-local', flags: ['permanent']}]};
const eth0: NetIface = {
    name: 'eth0',
    up: true,
    ipv4: [{address: '10.10.0.2', prefix: 24}],
    ipv6: [
        {address: '2001:db8:1:0:1234:5678:abcd:ef01', prefix: 64, scope: 'global', flags: ['temporary', 'deprecated']},
        {address: 'fd00::119', prefix: 64, scope: 'unique-local'},
        {address: '2001:db8:1::119', prefix: 64, scope: 'global', flags: ['permanent']},
        {address: 'fe80::dea6:32ff:fe01:203', prefix: 64, scope: 'link-local', flags: ['permanent']},
    ],
};
const eth1: NetIface = {name: 'eth1', up: false, ipv4: [{address: '198.51.100.7', prefix: 24}], ipv6: []};
const noV4 = (i: NetIface): NetIface => ({...i, ipv4: []});

describe('an IP literal', () => {
    it.each([
        ['192.0.2.119', true],
        ['10.0.0.1', true],
        ['[fd00::1]', true],
        ['fd00::1', true],
        ['[::ffff:192.0.2.1]', true],
        ['openccu', false],
        ['openccu.home.arpa', false],
        ['localhost', false],
        ['256.1.1.1', false],
        ['1.2.3', false],
        ['', false],
        ['cafe', false],
    ])('%s: %s', (host, want) => {
        expect(isIPLiteral(host)).toBe(want);
    });
});

describe('the address the recovery system is opened at', () => {
    it.each<[string, string, NetworkView | null, string, string]>([
        // 1. the host the browser already uses
        ['by IPv4, the network is not read', '192.0.2.50', {interfaces: [wlan0]}, 'literal', 'http://192.0.2.50/'],
        ['by IPv6 in brackets, kept', '[fd00::119]', {interfaces: [wlan0]}, 'literal', 'http://[fd00::119]/'],
        ['by a bare IPv6 address, bracketed', 'fd00::119', null, 'literal', 'http://[fd00::119]/'],
        // 2. IPv4 of the default route's interface
        ['by name: the default route wins over the first name', 'openccu', {interfaces: [eth0, eth1, wlan0]}, 'ipv4', 'http://192.0.2.119/'],
        ['by name, no default route: the first interface by name', 'openccu', {interfaces: [wlan0, eth0].map((i) => ({...i, default_route: false}))}, 'ipv4', 'http://10.10.0.2/'],
        ['the default route has no IPv4: another interface has', 'openccu', {interfaces: [noV4(wlan0), eth0]}, 'ipv4', 'http://10.10.0.2/'],
        ['a down interface is skipped', 'openccu', {interfaces: [eth1]}, 'name', 'http://openccu/'],
        ['loopback and 169.254 are no address to hand out', 'openccu', {interfaces: [{name: 'eth0', up: true, default_route: true, ipv4: [{address: '169.254.3.4', prefix: 16}, {address: '127.0.0.2', prefix: 8}]}]}, 'name', 'http://openccu/'],
        // 3. IPv6 only without any IPv4: global before unique-local, never temporary or link-local
        ['no IPv4: the stable global IPv6 address, bracketed', 'openccu', {interfaces: [noV4(wlan0), noV4(eth0)], ipv6_state: {available: true, enabled: true, gateway_interface: 'eth0', dns: []}}, 'ipv6', 'http://[2001:db8:1::119]/'],
        ['no IPv4, no global address: the unique-local one', 'openccu', {interfaces: [{...noV4(eth0), ipv6: eth0.ipv6!.filter((a) => a.scope !== 'global')}]}, 'ipv6', 'http://[fd00::119]/'],
        ['the IPv6 gateway interface first', 'openccu', {interfaces: [{name: 'eth0', up: true, default_route: true, ipv6: [{address: 'fd00::1', prefix: 64}]}, {name: 'eth9', up: true, ipv6: [{address: 'fd00::9', prefix: 64}]}], ipv6_state: {available: true, enabled: true, gateway_interface: 'eth9', dns: []}}, 'ipv6', 'http://[fd00::9]/'],
        ['an older answer without scope: link-local is recognised by its prefix', 'openccu', {interfaces: [{name: 'eth0', up: true, ipv6: [{address: 'fe80::1', prefix: 64}]}]}, 'name', 'http://openccu/'],
        // 4. nothing: the name
        ['only link-local IPv6: the name', 'openccu', {interfaces: [noV4(wlan0)]}, 'name', 'http://openccu/'],
        ['no network answer: the name', 'openccu.home.arpa', null, 'name', 'http://openccu.home.arpa/'],
        ['no interfaces: the name', 'openccu', {interfaces: []}, 'name', 'http://openccu/'],
    ])('%s', (_what, host, network, kind, url) => {
        const a = boxAddress(host, network);
        expect(a.kind).toBe(kind);
        expect(a.url).toBe(url);
        expect(urlOf(a.host)).toBe(url);
    });
});

describe('the lookup when a dialog opens', () => {
    afterEach(() => {
        vi.useRealTimers();
    });

    it('asks nothing when the browser came by address', async () => {
        const load = vi.fn(async () => ({network: {interfaces: [wlan0]}}));
        expect((await lookupBoxAddress('192.0.2.50', load)).url).toBe('http://192.0.2.50/');
        expect(load).not.toHaveBeenCalled();
    });

    it('reads the network API when the browser came by name', async () => {
        const load = vi.fn(async () => ({network: {interfaces: [eth0, wlan0]}}));
        expect(await lookupBoxAddress('openccu', load)).toEqual({kind: 'ipv4', host: '192.0.2.119', url: 'http://192.0.2.119/'});
        expect(load).toHaveBeenCalledOnce();
    });

    it('falls back to the name when the API fails or throws at once', async () => {
        expect((await lookupBoxAddress('openccu', () => Promise.reject(new Error('503')))).kind).toBe('name');
        expect(
            (
                await lookupBoxAddress('openccu', () => {
                    throw new Error('offline');
                })
            ).url,
        ).toBe('http://openccu/');
    });

    it('falls back to the name when the API does not answer in time', async () => {
        vi.useFakeTimers();
        const pending = lookupBoxAddress('openccu', () => new Promise(() => {}), 4000);
        await vi.advanceTimersByTimeAsync(4000);
        expect(await pending).toEqual({kind: 'name', host: 'openccu', url: 'http://openccu/'});
    });

    it('withTimeout hands on a value that comes in time', async () => {
        expect(await withTimeout(Promise.resolve(7), 1000, 0)).toBe(7);
        expect(await withTimeout(Promise.reject(new Error('x')), 1000, 0)).toBe(0);
    });
});

describe('the name as a second link', () => {
    const ip = boxAddress('openccu', {interfaces: [wlan0]});
    it.each<[string, ReturnType<typeof boxAddress>, string, boolean | null, string]>([
        ['HSTS off: the name follows the address', ip, 'openccu', false, 'http://openccu/'],
        ['HSTS on: only the address', ip, 'openccu', true, ''],
        ['HSTS unknown: counts as on', ip, 'openccu', null, ''],
        ['came by address: there is no name to add', boxAddress('192.0.2.119'), '192.0.2.119', false, ''],
        ['no address could be read: the name is the link already', boxAddress('openccu', null), 'openccu', false, ''],
    ])('%s', (_what, address, host, hsts, want) => {
        expect(nameLink(address, host, hsts)).toBe(want);
    });
});
