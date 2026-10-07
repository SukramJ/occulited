import {describe, expect, it} from 'vitest';
import {openedAs, validHostname} from './hostname';

describe('validHostname (openccu-lite task 327)', () => {
    it('takes one DNS label as the daemon does', () => {
        for (const ok of ['openccu-lite-3f2a', 'attic', 'a', 'CCU3', '0ccu', 'x'.repeat(63)]) expect(validHostname(ok), ok).toBe(true);
    });
    it('refuses hyphens at the ends, dots, spaces, underscores, umlauts, the empty name and 64 characters', () => {
        for (const bad of ['', '-ccu', 'ccu-', 'ccu.home', 'my ccu', 'my_ccu', 'küche', 'x'.repeat(64)]) expect(validHostname(bad), bad).toBe(false);
    });
});

describe('openedAs', () => {
    it('knows the bare name and the name with a domain, in any case', () => {
        expect(openedAs('openccu-lite-3f2a', 'openccu-lite-3f2a')).toBe(true);
        expect(openedAs('OpenCCU-Lite-3F2A.fritz.box', 'openccu-lite-3f2a')).toBe(true);
        expect(openedAs('192.0.2.119', 'openccu-lite-3f2a')).toBe(false);
        expect(openedAs('openccu-lite-3f2ab', 'openccu-lite-3f2a')).toBe(false);
        expect(openedAs('x', '')).toBe(false);
    });
});
