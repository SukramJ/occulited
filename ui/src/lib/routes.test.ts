import {describe, expect, it} from 'vitest';
import {BOUNCE_WINDOW_MS, mayBounce, onPage, parseBounce, safeReturn, segmentOf, settingsAlias, settingsPath} from './routes';

describe('onPage', () => {
    it.each([
        ['/log', '/log', true],
        ['/log/', '/log', true],
        ['/log/x', '/log', true],
        ['/login', '/log', false],
        ['/logout', '/log', false],
        ['/settings', '/settings', true],
        ['/settingsx', '/settings', false],
        ['/addons', '/addons', true],
        ['/addon-settings/mosquitto', '/addons', false],
        ['/names', '/metadata', false],
        ['/', '/log', false],
    ])('%s on %s: %s', (path, base, want) => {
        expect(onPage(path, base)).toBe(want);
    });
});

describe('safeReturn', () => {
    const O = 'https://box.local:8443';
    it.each([
        ['/addons/hmm/', '/addons/hmm/'],
        ['/addons/redmatic/settings.cgi?x=1#top', '/addons/redmatic/settings.cgi?x=1#top'],
        ['/services', '/services'],
        ['/a/../addons/hmm/', '/addons/hmm/'],
    ])('follows the path %s', (raw, want) => {
        expect(safeReturn(raw, O)).toBe(want);
    });

    it.each([
        [null],
        [''],
        ['addons/hmm/'],
        ['https://evil.example/'],
        ['//evil.example/'],
        ['/\\evil.example/'],
        ['/\t/evil.example/'],
        ['/\n/evil.example/'],
        ['/.//evil.example/'],
        ['/..//evil.example/'],
        ['javascript:alert(1)'],
        ['/login'],
        ['/login?return=/addons/hmm/'],
    ])('refuses %j', (raw) => {
        expect(safeReturn(raw, O)).toBe('');
    });

    it('a target it follows stays on the origin when the browser resolves it', () => {
        for (const raw of ['/addons/hmm/', '/%2F/evil.example/', '/%5Cevil.example/']) {
            const target = safeReturn(raw, O);
            expect(new URL(O + target).origin).toBe(O);
            expect(new URL(target, O).origin).toBe(O);
        }
    });
});

describe('the bounce guard', () => {
    it('lets the first bounce and one to another target through', () => {
        expect(mayBounce(null, '/addons/hmm/', 1000)).toBe(true);
        expect(mayBounce({target: '/addons/red/', at: 1000}, '/addons/hmm/', 1500)).toBe(true);
    });

    it('stops a second bounce to the same target within the window, not after it', () => {
        const last = {target: '/addons/hmm/', at: 10_000};
        expect(mayBounce(last, '/addons/hmm/', 12_000)).toBe(false);
        expect(mayBounce(last, '/addons/hmm/', 10_000 + BOUNCE_WINDOW_MS + 1)).toBe(true);
        // a clock that went backwards does not lock the target out
        expect(mayBounce(last, '/addons/hmm/', 5_000)).toBe(true);
    });

    it('reads only what it wrote', () => {
        expect(parseBounce(JSON.stringify({target: '/x', at: 5}))).toEqual({target: '/x', at: 5});
        expect(parseBounce('{"target":1,"at":5}')).toBeNull();
        expect(parseBounce('not json')).toBeNull();
        expect(parseBounce(null)).toBeNull();
    });
});

describe('the settings route', () => {
    it('is a path of its own, outside the addons\' www prefix', () => {
        expect(settingsPath('mosquitto')).toBe('/addon-settings/mosquitto');
        expect(settingsPath('a b/c')).toBe('/addon-settings/a%20b%2Fc');
        expect(onPage(settingsPath('mosquitto'), '/addons')).toBe(false);
        expect(settingsPath('x').startsWith('/addons/')).toBe(false);
    });

    it.each([
        ['/nav/redmatic', '/nav', 'redmatic'],
        ['/addon-settings/jp-hb-devices-addon', '/addon-settings', 'jp-hb-devices-addon'],
        ['/addon-settings/a%20b', '/addon-settings', 'a b'],
        ['/addon-settings/', '/addon-settings', ''],
        ['/addon-settings', '/addon-settings', ''],
        ['/addon-settings/x/y', '/addon-settings', ''],
        ['/addon-settingsx/y', '/addon-settings', ''],
        ['/nav/%E0%A4%A', '/nav', ''],
    ])('segmentOf(%s, %s) is %j', (path, base, want) => {
        expect(segmentOf(path, base)).toBe(want);
    });

    it.each([
        ['/addons/mosquitto', '/addon-settings/mosquitto'],
        ['/addons/jp-hb-devices-addon', '/addon-settings/jp-hb-devices-addon'],
        ['/addons', ''],
        ['/addons/', ''],
        ['/addons/red/', ''],
        ['/addons/redmatic/settings.cgi', ''],
        ['/addon-settings/mosquitto', ''],
        ['/services', ''],
    ])('the old path %s stands for %j', (path, want) => {
        expect(settingsAlias(path)).toBe(want);
    });
});
