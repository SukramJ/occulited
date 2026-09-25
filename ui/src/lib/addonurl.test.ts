import {describe, expect, it} from 'vitest';
import {addonUrl, withLook} from './addonurl';

// task 125: the alias goes into the URL only where the box says legacy_session
describe('addonUrl', () => {
    it('appends the alias for an addon the box marks legacy_session', () => {
        expect(addonUrl('/addons/red/', 'LeGaCy0001', true)).toBe('/addons/red/?sid=@LeGaCy0001@');
        expect(addonUrl('/addons/mosquitto/settings.cgi?x=1', 'LeGaCy0001', true)).toBe('/addons/mosquitto/settings.cgi?x=1&sid=@LeGaCy0001@');
    });
    it('leaves the URL alone for an addon without legacy_session - one that reads the header, or switched off', () => {
        expect(addonUrl('/addons/red/', 'LeGaCy0001', false)).toBe('/addons/red/');
        expect(addonUrl('/addons/redmatic/settings.cgi', 'LeGaCy0001')).toBe('/addons/redmatic/settings.cgi');
        expect(addonUrl('/addons/red/', 'LeGaCy0001', undefined)).toBe('/addons/red/');
    });
    it('leaves the URL alone without an alias', () => {
        expect(addonUrl('/addons/red/', '', true)).toBe('/addons/red/');
    });
});

// B-200: theme= and lang= on every addon frame's URL, the nav.d pages' too
describe('withLook', () => {
    it.each([
        ['/addons/hmm/', 'dark', 'de', '/addons/hmm/?theme=dark&lang=de'],
        ['/addons/red/?sid=@LeGaCy0001@', 'system', 'en', '/addons/red/?sid=@LeGaCy0001@&theme=system&lang=en'],
        ['/addons/x/index.html#/devices', 'light', 'en', '/addons/x/index.html?theme=light&lang=en#/devices'],
        ['/addons/x/?a=1#top', 'dark', 'de', '/addons/x/?a=1&theme=dark&lang=de#top'],
    ])('%s with %s/%s is %s', (url, theme, lang, want) => {
        expect(withLook(url, theme, lang)).toBe(want);
    });
});
