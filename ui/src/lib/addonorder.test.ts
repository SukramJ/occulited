import {describe, expect, it} from 'vitest';
import {arrange, same, withPin, type AddonPref} from './addonorder';

const p = (id: string, pinned = false): AddonPref => ({id, pinned});

describe('arrange', () => {
    it('with nothing stored: the given order, unpinned', () => {
        expect(arrange(['mh', 'redmatic'], [])).toEqual([p('mh'), p('redmatic')]);
    });
    it('the stored order first, with its pins, then the addons it does not name', () => {
        expect(arrange(['iobroker', 'mh', 'redmatic'], [p('redmatic', true), p('mh')])).toEqual([p('redmatic', true), p('mh'), p('iobroker')]);
    });
    it('skips an entry whose addon is gone, pin and all, and a duplicate', () => {
        expect(arrange(['mh'], [p('gone', true), p('mh', true), p('mh')])).toEqual([p('mh', true)]);
    });
    it('returns copies', () => {
        const stored = [p('mh', true)];
        const out = arrange(['mh'], stored);
        out[0]!.pinned = false;
        expect(stored[0]!.pinned).toBe(true);
    });
});

describe('withPin and same', () => {
    it('sets one pin and leaves the rest', () => {
        const list = [p('a'), p('b', true)];
        expect(withPin(list, 'a', true)).toEqual([p('a', true), p('b', true)]);
        expect(withPin(list, 'b', false)).toEqual([p('a'), p('b')]);
        expect(list).toEqual([p('a'), p('b', true)]);
    });
    it('same compares entry by entry', () => {
        expect(same([p('a'), p('b', true)], [p('a'), p('b', true)])).toBe(true);
        expect(same([p('a'), p('b', true)], [p('a'), p('b')])).toBe(false);
        expect(same([p('a')], [p('a'), p('b')])).toBe(false);
    });
});
