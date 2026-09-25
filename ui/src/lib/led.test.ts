import {describe, expect, it} from 'vitest';
import {backgroundOf, changed, disjoint, normalChoice, normalizeLook, overNormalOption, secondColors, type LEDConfig} from './led';

const COLORS = ['red', 'green', 'blue', 'yellow', 'cyan', 'magenta', 'white', 'off'];

describe('status LED model', () => {
    it('knows which colours can alternate', () => {
        expect(disjoint('yellow', 'blue')).toBe(true);
        expect(disjoint('blue', 'cyan')).toBe(false);
        expect(disjoint('white', 'red')).toBe(false);
        expect(secondColors('yellow', COLORS)).toEqual(['blue']);
        expect(secondColors('blue', COLORS)).toEqual(['red', 'green', 'yellow']);
        expect(secondColors('white', COLORS)).toEqual([]);
    });

    it('normalises a look as the API spells it', () => {
        expect(normalizeLook({color: 'off', pattern: 'fast', color2: 'red'}, COLORS)).toEqual({color: 'off', pattern: 'solid'});
        expect(normalizeLook({color: 'red', pattern: 'slow', color2: 'blue'}, COLORS)).toEqual({color: 'red', pattern: 'slow'});
        expect(normalizeLook({color: 'yellow', pattern: 'alternate'}, COLORS)).toEqual({color: 'yellow', pattern: 'alternate', color2: 'blue'});
        expect(normalizeLook({color: 'blue', pattern: 'alternate', color2: 'cyan'}, COLORS)).toEqual({color: 'blue', pattern: 'alternate', color2: 'red'});
        // white lights every channel: nothing to alternate with, so it stays steady
        expect(normalizeLook({color: 'white', pattern: 'alternate'}, COLORS)).toEqual({color: 'white', pattern: 'solid'});
    });

    it('keeps over_normal only where a pattern has an off phase (task 134)', () => {
        expect(normalizeLook({color: 'yellow', pattern: 'slow', over_normal: true}, COLORS)).toEqual({color: 'yellow', pattern: 'slow', over_normal: true});
        expect(normalizeLook({color: 'red', pattern: 'double', over_normal: true}, COLORS)).toEqual({color: 'red', pattern: 'double', over_normal: true});
        // off is not written at all, so a saved draft equals what the API answers
        expect(normalizeLook({color: 'yellow', pattern: 'slow', over_normal: false}, COLORS)).toEqual({color: 'yellow', pattern: 'slow'});
        expect(normalizeLook({color: 'yellow', pattern: 'solid', over_normal: true}, COLORS)).toEqual({color: 'yellow', pattern: 'solid'});
        expect(normalizeLook({color: 'yellow', pattern: 'alternate', over_normal: true}, COLORS)).toEqual({color: 'yellow', pattern: 'alternate', color2: 'blue'});
        expect(normalizeLook({color: 'off', pattern: 'fast', over_normal: true}, COLORS)).toEqual({color: 'off', pattern: 'solid'});
    });

    it('offers blinking over the normal colour for the four patterns, and not in the same colour', () => {
        const blue = {color: 'blue', pattern: 'solid'};
        for (const p of ['slow', 'fast', 'flash', 'double']) expect(overNormalOption({color: 'yellow', pattern: p}, blue)).toBe('yes');
        for (const p of ['solid', 'alternate']) expect(overNormalOption({color: 'yellow', pattern: p}, blue)).toBe('no');
        expect(overNormalOption({color: 'off', pattern: 'solid'}, blue)).toBe('no');
        expect(overNormalOption({color: 'blue', pattern: 'slow'}, blue)).toBe('same');
        expect(overNormalOption({color: 'cyan', pattern: 'flash'}, blue)).toBe('yes');
        // the background on the box: normal's colour, dark when normal is off, the LED off, or the option unset
        expect(backgroundOf({color: 'yellow', pattern: 'slow', over_normal: true}, blue)).toBe('blue');
        expect(backgroundOf({color: 'yellow', pattern: 'slow'}, blue)).toBe('');
        expect(backgroundOf({color: 'yellow', pattern: 'slow', over_normal: true}, {color: 'off', pattern: 'solid'})).toBe('');
        expect(backgroundOf({color: 'blue', pattern: 'slow', over_normal: true}, blue)).toBe('');
        expect(backgroundOf({color: 'yellow', pattern: 'slow', over_normal: true}, blue, false)).toBe('');
    });

    it('reads the segmented control of the look when all is fine', () => {
        expect(normalChoice({color: 'blue', pattern: 'solid'})).toBe('blue');
        expect(normalChoice({color: 'green', pattern: 'solid'})).toBe('green');
        expect(normalChoice({color: 'off', pattern: 'solid'})).toBe('off');
        expect(normalChoice({color: 'blue', pattern: 'flash'})).toBe('custom');
        expect(normalChoice({color: 'white', pattern: 'solid'})).toBe('custom');
    });

    it('tells a changed draft from the stored configuration', () => {
        const a = {enabled: true} as LEDConfig;
        expect(changed(a, {enabled: true} as LEDConfig)).toBe(false);
        expect(changed(a, {enabled: false} as LEDConfig)).toBe(true);
        expect(changed(null, a)).toBe(true);
    });
});
