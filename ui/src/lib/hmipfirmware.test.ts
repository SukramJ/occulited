import {describe, expect, it} from 'vitest';
import {firmwareRefusalLines, refusalOf} from './hmipfirmware';

const t = (key: string, params?: Record<string, string | number>) => key.replace(/\{(\w+)\}/g, (_, k) => String(params?.[k] ?? `{${k}}`));

describe('the HmIP firmware refusal (openccu-lite B-289)', () => {
    it('reads the API detail', () => {
        expect(refusalOf({module: 'M', version: '1.8.3', minimum: '2.8.0'})).toEqual({module: 'M', version: '1.8.3', minimum: '2.8.0'});
        expect(refusalOf({module: 'M', version: '1.8.3'})?.minimum).toBe('2.8.0');
        expect(refusalOf(undefined)).toBeNull();
        expect(refusalOf({module: 'M'})).toBeNull();
    });
    it('names the module, the firmware and the update', () => {
        const r = {module: '3014F711A000040000000A01', version: '1.8.3', minimum: '2.8.0'};
        const change = firmwareRefusalLines(r, 'change', t).join('\n');
        expect(change).toContain('module 3014F711A000040000000A01');
        expect(change).toContain('firmware 1.8.3');
        expect(change).toContain('below 2.8.0');
        expect(change).toContain('Update the module\'s firmware first');
        expect(change).toContain('local key mode on');
        const backup = firmwareRefusalLines(r, 'backup', t).join('\n');
        expect(backup).toContain('this backup');
        expect(backup).toContain('backup taken in local key mode');
    });
});
