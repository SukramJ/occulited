import {describe, expect, it} from 'vitest';
import type {LogLevels} from './api';
import {levelsKey, MULTIMACD_LEVELS, multimacdLevel, OCCULITED_AREAS, occulitedDebugOn, toggleArea} from './loglevels';

const base: LogLevels = {
    rfd: 5,
    hs485d: 5,
    multimacd: 2,
    hmip: 'WARN',
    loghost: '',
    lighttpd: {request_handling: false, condition_handling: false, file_not_found: false, access_log: false},
    occulited: {level: 'info', debug_areas: []},
    restart: [],
    applied: [],
};

describe('multimacdLevel', () => {
    it('is Debug for 1 and Info for everything else, never rfd\'s (task 297)', () => {
        expect(multimacdLevel(1)).toBe(1);
        expect(multimacdLevel(2)).toBe(2);
        // an older system's stored 3-7, none, or the old null: Info, as the system reads it
        for (const n of [0, 3, 4, 5, 7, null, undefined]) expect(multimacdLevel(n)).toBe(2);
        expect(MULTIMACD_LEVELS.map((l) => l.v)).toEqual([1, 2]);
    });
});

describe('toggleArea', () => {
    it("keeps the areas in logctl's order, once each", () => {
        expect(toggleArea([], 'led', true)).toEqual(['led']);
        expect(toggleArea(['led'], 'acme', true)).toEqual(['acme', 'led']);
        expect(toggleArea(['acme', 'led'], 'acme', true)).toEqual(['acme', 'led']);
        expect(toggleArea(['acme', 'led'], 'acme', false)).toEqual(['led']);
        expect(toggleArea(['nonsense', 'http'], 'x', false)).toEqual(['http']);
        expect(OCCULITED_AREAS.map((a) => a.id)).toEqual(['acme', 'radio-firmware', 'addons', 'metadata', 'http', 'auth', 'led', 'rpc']);
    });
});

describe('levelsKey', () => {
    it('changes with every setting and ignores what a PUT answers', () => {
        const k = levelsKey(base);
        expect(levelsKey({...base, restart: ['multimacd'], applied: ['occulited']})).toBe(k);
        expect(levelsKey({...base, multimacd: 1})).not.toBe(k);
        expect(levelsKey({...base, occulited: {level: 'debug', debug_areas: []}})).not.toBe(k);
        expect(levelsKey({...base, occulited: {level: 'info', debug_areas: ['acme']}})).not.toBe(k);
        // the areas' order is not a change
        expect(levelsKey({...base, occulited: {level: 'info', debug_areas: ['led', 'acme']}})).toBe(levelsKey({...base, occulited: {level: 'info', debug_areas: ['acme', 'led']}}));
    });
});

describe('occulitedDebugOn', () => {
    it('is on at debug or with an area', () => {
        expect(occulitedDebugOn(base)).toBe(false);
        expect(occulitedDebugOn({occulited: {level: 'debug', debug_areas: []}})).toBe(true);
        expect(occulitedDebugOn({occulited: {level: 'error', debug_areas: ['http']}})).toBe(true);
    });
});
