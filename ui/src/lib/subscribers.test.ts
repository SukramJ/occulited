import {describe, expect, it} from 'vitest';
import {clientKind, processAddress, processOrder} from './subscribers';

describe('the subscriber rows', () => {
    it.each([
        ['nr_Ab1Cd2_HmIP-RF', 'node-red'],
        ['hmm_VirtualDevices', 'hmm'],
        ['BidCos-RF_java', 'java'],
        ['1007', 'rega'],
        ['mb_BidCos_RF', ''],
        ['olt_test', ''],
    ])('names the client of %s', (id, kind) => {
        expect(clientKind(id)).toBe(kind);
    });

    it.each([
        ['xmlrpc_bin://127.0.0.1:32001', '127.0.0.1:32001'],
        ['xmlrpc://127.0.0.1:39292/groups', '127.0.0.1:39292/groups'],
        ['xmlrpc://127.0.0.1:2010/', '127.0.0.1:2010'],
        ['127.0.0.1:1', '127.0.0.1:1'],
    ])('shows %s as %s', (url, shown) => {
        expect(processAddress(url)).toBe(shown);
    });

    it('orders the processes under their module cards, the rest after them as listed', () => {
        const list = [{name: 'VirtualDevices'}, {name: 'HmIP-RF'}, {name: 'BidCos-Wired'}, {name: 'BidCos-RF'}];
        expect(processOrder(list, ['BidCos-RF', 'HmIP-RF']).map((i) => i.name)).toEqual(['BidCos-RF', 'HmIP-RF', 'VirtualDevices', 'BidCos-Wired']);
        expect(processOrder(list, []).map((i) => i.name)).toEqual(['VirtualDevices', 'HmIP-RF', 'BidCos-Wired', 'BidCos-RF']);
        expect(processOrder(list, ['HmIP-RF', 'Missing']).map((i) => i.name)).toEqual(['HmIP-RF', 'VirtualDevices', 'BidCos-Wired', 'BidCos-RF']);
    });
});
