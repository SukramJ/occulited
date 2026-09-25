import {describe, expect, it} from 'vitest';
import {addonState, compare, frontendKey, isSettingsKey, keepable, leaving, MAX_KEPT, overflow, settingsKey} from './framekeep';

describe('overflow', () => {
    const slots = [
        {key: 'nav:redmatic', used: 4},
        {key: 'nav:mh', used: 2},
        {key: 'nav:hm2mqtt', used: 3},
        {key: 'nav:tools', used: 5},
        {key: 'settings:mosquitto', used: 1},
    ];

    it('keeps three frontends by default', () => {
        expect(MAX_KEPT).toBe(3);
    });

    it('drops nothing while there is room', () => {
        expect(overflow(slots.slice(0, 3), 3, 'nav:redmatic')).toEqual([]);
        expect(overflow([], 3, 'x')).toEqual([]);
    });

    it('drops the least recently shown frontend', () => {
        expect(overflow(slots, 3, 'nav:tools')).toEqual(['nav:mh']);
        expect(overflow(slots, 2, 'nav:tools')).toEqual(['nav:mh', 'nav:hm2mqtt']);
    });

    it('never drops the page being opened, even when it is the oldest', () => {
        expect(overflow(slots, 3, 'nav:mh')).toEqual(['nav:hm2mqtt']);
    });

    it('neither counts nor drops a settings page: it never pushes a frontend out', () => {
        const three = [...slots.slice(0, 3), {key: 'settings:mosquitto', used: 9}, {key: 'settings:hm2mqtt', used: 0}];
        expect(overflow(three, 3, 'settings:mosquitto')).toEqual([]);
        expect(overflow(slots, 3, 'settings:mosquitto')).toEqual(['nav:mh']);
    });
});

describe('leaving a settings page', () => {
    const slots = [{key: 'nav:redmatic'}, {key: 'settings:mosquitto'}, {key: 'settings:hm2mqtt'}];

    it('drops every settings page but the one shown', () => {
        expect(leaving(slots, 'settings:hm2mqtt')).toEqual(['settings:mosquitto']);
        expect(leaving(slots, 'nav:redmatic')).toEqual(['settings:mosquitto', 'settings:hm2mqtt']);
        expect(leaving(slots, '')).toEqual(['settings:mosquitto', 'settings:hm2mqtt']);
        expect(leaving([{key: 'nav:redmatic'}], '')).toEqual([]);
    });

    it('tells a settings key from a frontend key', () => {
        expect(isSettingsKey(settingsKey('redmatic'))).toBe(true);
        expect(isSettingsKey(frontendKey('redmatic'))).toBe(false);
        expect(isSettingsKey(frontendKey('settings'))).toBe(false);
    });
});

describe('keys and keepable', () => {
    it('keeps a frontend and a settings page of one addon apart', () => {
        expect(frontendKey('redmatic')).not.toBe(settingsKey('redmatic'));
    });

    it.each([
        [{source: 'addon', target: 'iframe'}, true],
        [{source: 'addon', target: 'blank'}, false],
        [{source: 'nav.d', target: 'iframe'}, false],
        [{source: 'nav.d', target: 'iframe', keep_alive: true}, true],
        [{source: 'nav.d', target: 'blank', keep_alive: true}, false],
    ] as const)('%j is kept: %s', (e, want) => {
        expect(keepable(e)).toBe(want);
    });
});

describe('a kept page goes stale', () => {
    const red = {id: 'redmatic', version: '9.4.0', running: true, pid: 1841, enabled: true};

    it('follows version, running, pid and the switch, and nothing else', () => {
        const s = addonState(red);
        expect(addonState({...red})).toBe(s);
        expect(addonState({...red, running: false, pid: 0})).not.toBe(s);
        expect(addonState({...red, pid: 1900})).not.toBe(s);
        expect(addonState({...red, enabled: false})).not.toBe(s);
        expect(addonState({...red, version: '9.4.1'})).not.toBe(s);
        expect(addonState(undefined)).toBe('gone');
    });

    it('learns the state of a new page, drops a changed or uninstalled one, leaves a nav.d page alone', () => {
        const kept = [
            {key: 'nav:redmatic', addon: 'redmatic', state: ''},
            {key: 'settings:redmatic', addon: 'redmatic', state: addonState({...red, pid: 1000})},
            {key: 'nav:mh', addon: 'mh', state: addonState({version: '3.0.0', running: true, pid: 2011, enabled: true})},
            {key: 'settings:gone', addon: 'gone', state: ''},
            {key: 'nav:grafana', addon: '', state: ''},
        ];
        const addons = [red, {id: 'mh', version: '3.0.0', running: true, pid: 2011, enabled: true}];
        expect(compare(kept, addons)).toEqual({drop: ['settings:redmatic', 'settings:gone'], learn: {'nav:redmatic': addonState(red)}});
    });
});
