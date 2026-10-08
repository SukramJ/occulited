import {describe, expect, it} from 'vitest';
import {arrange, same, withFullscreen, withPin, type AddonPref} from './addonorder';

const p = (id: string, pinned = false): AddonPref => ({id, pinned});
const f = (id: string, pinned = false): AddonPref => ({id, pinned, fullscreen: true});

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

// occulited task 24: the whole-window flag on an entry
describe('fullscreen', () => {
    it('arrange keeps the flag of a stored entry, and only when it is set', () => {
        expect(arrange(['mh', 'redmatic'], [f('redmatic', true), p('mh')])).toEqual([f('redmatic', true), p('mh')]);
        expect(arrange(['mh'], [{id: 'mh', pinned: false, fullscreen: false}])).toEqual([p('mh')]);
    });
    it('withPin leaves the flag alone', () => {
        expect(withPin([f('a'), p('b')], 'a', true)).toEqual([f('a', true), p('b')]);
        expect(withPin([f('a', true)], 'a', false)).toEqual([f('a')]);
    });
    it('withFullscreen sets or clears one flag and keeps the pin', () => {
        const list = [p('a', true), p('b')];
        expect(withFullscreen(list, 'a', true)).toEqual([f('a', true), p('b')]);
        expect(withFullscreen([f('a', true), p('b')], 'a', false)).toEqual([p('a', true), p('b')]);
        expect(list).toEqual([p('a', true), p('b')]);
    });
    it('withFullscreen adds an addon the list does not name, unpinned, at the end; off for one it does not name changes nothing', () => {
        expect(withFullscreen([p('a')], 'new', true)).toEqual([p('a'), f('new')]);
        expect(withFullscreen([p('a')], 'new', false)).toEqual([p('a')]);
    });
    it('same tells the flag apart', () => {
        expect(same([f('a')], [p('a')])).toBe(false);
        expect(same([f('a')], [f('a')])).toBe(true);
        expect(same([{id: 'a', pinned: false, fullscreen: false}], [p('a')])).toBe(true);
    });
});
