import {describe, expect, it} from 'vitest';
import {addressOf, formatSGTIN, keyShape, normalizeSGTIN, parseDeviceCode, parseWifiCode, printedKey} from './devicekeys';

describe('device keys', () => {
    it("reads a sticker's code wherever it stands", () => {
        const want = {sgtin: '3014F711A0000EDD89A81DBA', key: 'CA477C71EC12F9BDD289046D34012259'};
        expect(parseDeviceCode('EQ01SG3014F711A0000EDD89A81DBADLKCA477C71EC12F9BDD289046D34012259')).toEqual(want);
        expect(parseDeviceCode(' eq01sg3014f711a0000edd89a81dba dlkca477c71ec12f9bdd289046d34012259 ')).toEqual(want);
        expect(parseDeviceCode('WIFI:S:x;;')).toBeNull();
    });

    it('writes the SGTIN and the key as the sticker prints them', () => {
        expect(formatSGTIN('3014F711A0000EDD89A81DBA')).toBe('3014-F711-A000-0EDD-89A8-1DBA');
        expect(normalizeSGTIN('3014-f711-a000-0edd-89a8-1dba')).toBe('3014F711A0000EDD89A81DBA');
        expect(normalizeSGTIN('3014-F711')).toBeNull();
        expect(addressOf('3014F711A0000EDD89A81DBA')).toBe('000EDD89A81DBA');
        // the server's conversion reads 6A8XY73U0KZ6YX5284EMT028KS as CA477C71… (internal/radio)
        expect(printedKey('CA477C71EC12F9BDD289046D34012259')).toBe('6A8XY-73U0K-Z6YX5-284EM-T028KS');
        expect(printedKey('0'.repeat(32))).toBe('00000-00000-00000-00000-000000');
    });

    it('tells the key shapes apart', () => {
        expect(keyShape('6A8XY-73U0K-Z6YX5-284EM-T028KS')).toBe('printed');
        expect(keyShape('ca477c71ec12f9bdd289046d34012259')).toBe('hex');
        expect(keyShape('6A8XY-73U0K-Z6YX5-284EM-T028KO')).toBe('odiv');
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
