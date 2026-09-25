import {describe, expect, it} from 'vitest';

import {addonIconSrc} from './addonicon';

// task 56: the one source of a logo is the addon's own Info: line, and only a path into its
// own /addons/<id>/ directory is accepted - never an external image of any kind
describe('addonIconSrc', () => {
    it('takes the image of the Info: line in both spellings seen on the lab box', () => {
        expect(addonIconSrc('redmatic', '<div><a href="x"><img src="/addons/redmatic/redmatic5-wide.png" height="48"/></a></div>')).toBe('/addons/redmatic/redmatic5-wide.png');
        expect(addonIconSrc('jp-hb-devices-addon', "<center><img src='../addons/jp-hb-devices-addon/jp-hb-devices-addon.png'></img></center>")).toBe('/addons/jp-hb-devices-addon/jp-hb-devices-addon.png');
    });
    it('is empty without an Info: line or without an image in it', () => {
        expect(addonIconSrc('hm2mqtt', undefined)).toBe('');
        expect(addonIconSrc('hm2mqtt', '<div>Homematic to MQTT bridge - <a href="https://github.com/x/y">github.com/x/y</a></div>')).toBe('');
        expect(addonIconSrc('x', '<img src="">')).toBe('');
    });
    it('refuses an absolute URL, data:, javascript:, another addon and a path that climbs out', () => {
        expect(addonIconSrc('x', '<img src="https://example.org/logo.png">')).toBe('');
        expect(addonIconSrc('x', '<img src="//example.org/logo.png">')).toBe('');
        expect(addonIconSrc('x', '<img src="data:image/png;base64,AAAA">')).toBe('');
        expect(addonIconSrc('x', '<img src="javascript:alert(1)">')).toBe('');
        expect(addonIconSrc('x', '<img src="/addons/y/logo.png">')).toBe('');
        expect(addonIconSrc('x', '<img src="/addons/x/../y/logo.png">')).toBe('');
        expect(addonIconSrc('x', '<img src="../addons/x/../../etc/passwd">')).toBe('');
    });
});
