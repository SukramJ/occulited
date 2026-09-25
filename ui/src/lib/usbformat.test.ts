import {describe, expect, it} from 'vitest';
import {labelError, suggestLabel} from './usbformat';

// openccu-lite task 228: the Format dialog's label
describe('labelError', () => {
    it.each([
        ['exfat', 'OPENCCU', ''],
        ['exfat', 'ABCDEFGHIJKLMNO', ''],
        ['exfat', 'ABCDEFGHIJKLMNOP', 'At most {n} characters.'],
        ['ext4', 'ABCDEFGHIJKLMNOP', ''],
        ['ext4', '', 'A label is required: the journal and the backups find the stick by it.'],
        ['ext4', 'my stick', 'Letters, digits, - and _ only.'],
        ['exfat', 'Log-Stick_1', ''],
    ] as const)('%s %j', (fs, label, want) => {
        expect(labelError(fs, label)).toBe(want);
    });
});

describe('suggestLabel', () => {
    it('keeps a current label that suits, cleans one that does not, else OPENCCU', () => {
        expect(suggestLabel(['LOGSTICK'], 'exfat')).toBe('LOGSTICK');
        expect(suggestLabel(['My Stick!'], 'exfat')).toBe('My_Stick');
        expect(suggestLabel(['AVERYLONGLABELNAME'], 'exfat')).toBe('AVERYLONGLABELN');
        expect(suggestLabel(['', '  '], 'ext4')).toBe('OPENCCU');
        expect(suggestLabel([], 'ext4')).toBe('OPENCCU');
    });
});
