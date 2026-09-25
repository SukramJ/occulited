import {describe, expect, it} from 'vitest';
import type {LogLevels} from './api';
import {levelsKey, multimacdLevel, OCCULITED_AREAS, occulitedDebugOn, toggleArea} from './loglevels';

const base: LogLevels = {
    rfd: 5,
    hs485d: 5,
    multimacd: null,
    hmip: 'WARN',
    loghost: '',
    lighttpd: {request_handling: false, condition_handling: false, file_not_found: false, access_log: false},
    occulited: {level: 'info', debug_areas: []},
    restart: [],
    applied: [],
};

describe('multimacdLevel', () => {
    it("is rfd's while multimacd has none, its own otherwise", () => {
        expect(multimacdLevel(base)).toBe(5);
        expect(multimacdLevel({...base, rfd: 2})).toBe(2);
        expect(multimacdLevel({...base, rfd: 2, multimacd: 1})).toBe(1);
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
