import {describe, expect, it} from 'vitest';
import {warningLink, warningText, type Warning, type Words} from './warnings';

const t = (key: string, params?: Record<string, string | number>) => {
    let s = key;
    for (const [k, v] of Object.entries(params ?? {})) s = s.replaceAll(`{${k}}`, String(v));
    return s;
};
const words: Words = {t, addonName: (a) => a.id, storage: () => '', when: (iso) => `at ${iso}`};

describe('the database copy on a USB stick (task 229)', () => {
    it('names the stick that is not plugged in, and a copy that failed', () => {
        const w: Warning = {id: 'store-target', variant: 'usb', severity: 'warning', href: '/log?settings=history', params: {label: 'LOGSTICK', folder: 'occulited'}};
        expect(warningText(w, words)).toBe("The USB stick LOGSTICK for the database's copy is not plugged in. The history stays on the userfs; a new card would start without it.");
        expect(warningText({...w, variant: 'failed', params: {label: 'LOGSTICK', reason: 'read-only'}}, words)).toBe(
            'The last copy of the database to the USB stick LOGSTICK failed: read-only. The history stays on the userfs; a new card would start without it.',
        );
        expect(warningLink(w, t)).toEqual({href: '/log?settings=history', label: 'History settings'});
    });
});
