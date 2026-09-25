import {describe, expect, it} from 'vitest';
import {newerFirmware, shownVersion} from './fwversion';

describe('newerFirmware', () => {
    it('compares like the daemon', () => {
        expect(newerFirmware('1.18.2', '1.0.3')).toBe(true);
        expect(newerFirmware('1.18.2', '1.18.2')).toBe(false);
        expect(newerFirmware('2.1.4', '2.1')).toBe(false);
        expect(newerFirmware('2.2.0', '2.1')).toBe(true);
        expect(newerFirmware('0.0.0', '1.0.3')).toBe(false);
        expect(newerFirmware(undefined, '1.0.3')).toBe(false);
        expect(newerFirmware('1.10.0', '1.9.9')).toBe(true);
    });
});

describe('shownVersion', () => {
    it('drops the all-zero placeholder', () => {
        expect(shownVersion('0.0.0')).toBe('');
        expect(shownVersion(undefined)).toBe('');
        expect(shownVersion('1.0.0')).toBe('1.0.0');
    });
});
