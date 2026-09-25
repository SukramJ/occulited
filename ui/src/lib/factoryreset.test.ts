import {describe, expect, it} from 'vitest';
import {sameHostname} from './factoryreset';

describe('sameHostname', () => {
    it('takes the name as typed, in any case, with space around it', () => {
        expect(sameHostname('ccu', 'ccu')).toBe(true);
        expect(sameHostname(' CCU ', 'ccu')).toBe(true);
        expect(sameHostname('ccu.lan', 'ccu.lan')).toBe(true);
    });
    it('takes the FQDN for a short name, not the other way round', () => {
        expect(sameHostname('ccu.lan', 'ccu')).toBe(true);
        expect(sameHostname('ccu', 'ccu.lan')).toBe(false);
    });
    it('refuses another name, an empty one and a leading dot', () => {
        expect(sameHostname('ccu2', 'ccu')).toBe(false);
        expect(sameHostname('', 'ccu')).toBe(false);
        expect(sameHostname('ccu', '')).toBe(false);
        expect(sameHostname('.ccu', 'ccu')).toBe(false);
    });
});
