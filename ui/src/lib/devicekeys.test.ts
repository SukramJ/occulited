import {describe, expect, it} from 'vitest';
import {addressOf, formatSGTIN, keyShape, normalizeSGTIN, parseDeviceCode, parseWifiCode, printedKey} from './devicekeys';

describe('device keys', () => {
    it("reads a sticker's code wherever it stands", () => {
        const want = {sgtin: '3014F711A0000E0000000A05', key: '0123456789ABCDEFFEDCBA9876543210'};
        expect(parseDeviceCode('EQ01SG3014F711A0000E0000000A05DLK0123456789ABCDEFFEDCBA9876543210')).toEqual(want);
        expect(parseDeviceCode(' eq01sg3014f711a0000e0000000a05 dlk0123456789abcdeffedcba9876543210 ')).toEqual(want);
        expect(parseDeviceCode('WIFI:S:x;;')).toBeNull();
    });

    it('writes the SGTIN and the key as the sticker prints them', () => {
        expect(formatSGTIN('3014F711A0000E0000000A05')).toBe('3014-F711-A000-0E00-0000-0A05');
        expect(normalizeSGTIN('3014-f711-a000-0e00-0000-0a05')).toBe('3014F711A0000E0000000A05');
        expect(normalizeSGTIN('3014-F711')).toBeNull();
        expect(addressOf('3014F711A0000E0000000A05')).toBe('000E0000000A05');
        // the server's conversion reads 014E2PG2EBSQQZXQ5TL1U58CHH as 01234567… (internal/radio)
        expect(printedKey('0123456789ABCDEFFEDCBA9876543210')).toBe('014E2-PG2EB-SQQZX-Q5TL1-U58CHH');
        expect(printedKey('0'.repeat(32))).toBe('00000-00000-00000-00000-000000');
    });

    it('tells the key shapes apart', () => {
        expect(keyShape('014E2-PG2EB-SQQZX-Q5TL1-U58CHH')).toBe('printed');
        expect(keyShape('0123456789abcdeffedcba9876543210')).toBe('hex');
        expect(keyShape('014E2-PG2EB-SQQZX-Q5TL1-U58CHO')).toBe('odiv');
        expect(keyShape('short')).toBe('');
    });

    it('reads a Wi-Fi code with its escapes', () => {
        expect(parseWifiCode('WIFI:T:WPA;S:My Net;P:se\\;cr\\:et\\\\;H:true;;')).toEqual({ssid: 'My Net', password: 'se;cr:et\\', type: 'WPA', hidden: true});
        expect(parseWifiCode('WIFI:S:Cafe;T:nopass;;')).toEqual({ssid: 'Cafe', password: '', type: 'NOPASS', hidden: false});
        expect(parseWifiCode('WIFI:T:SAE;P:x12345678;S:New;')).toEqual({ssid: 'New', password: 'x12345678', type: 'SAE', hidden: false});
        expect(parseWifiCode('EQ01SG…')).toBeNull();
        expect(parseWifiCode('WIFI:T:WPA;P:x;;')).toBeNull();
    });
});
