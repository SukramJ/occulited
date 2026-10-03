import {describe, expect, it} from 'vitest';

import {imageCandidates} from './addonimages';

const all = {icon: '/api/system/v1/addons/x/images/icon?v=1', 'icon-dark': '/api/system/v1/addons/x/images/icon-dark?v=1', logo: '/api/system/v1/addons/x/images/logo?v=1', 'logo-dark': '/api/system/v1/addons/x/images/logo-dark?v=1'};

// openccu-lite task 100: the theme's variant first, the other as its stand-in; a logo for a missing
// icon and the other way round; nothing from another origin
describe('imageCandidates', () => {
    it('puts the variant of the theme first and the other one behind it', () => {
        expect(imageCandidates(all, 'icon', false)).toEqual([all.icon, all['icon-dark'], all.logo, all['logo-dark']]);
        expect(imageCandidates(all, 'icon', true)).toEqual([all['icon-dark'], all.icon, all['logo-dark'], all.logo]);
        expect(imageCandidates(all, 'logo', true)).toEqual([all['logo-dark'], all.logo, all['icon-dark'], all.icon]);
    });
    it('falls back from a missing dark variant to the light one and the other way round', () => {
        expect(imageCandidates({icon: all.icon}, 'icon', true)).toEqual([all.icon]);
        expect(imageCandidates({'icon-dark': all['icon-dark']}, 'icon', false)).toEqual([all['icon-dark']]);
    });
    it('uses the logo for a missing icon and the icon for a missing logo', () => {
        expect(imageCandidates({logo: all.logo}, 'icon', false)).toEqual([all.logo]);
        expect(imageCandidates({icon: all.icon, 'icon-dark': all['icon-dark']}, 'logo', true)).toEqual([all['icon-dark'], all.icon]);
    });
    it('is empty without images', () => {
        expect(imageCandidates(undefined, 'icon', false)).toEqual([]);
        expect(imageCandidates({}, 'logo', true)).toEqual([]);
    });
    it('takes only paths of this origin\'s API', () => {
        expect(imageCandidates({icon: 'https://example.org/icon.svg', logo: '//example.org/x', 'icon-dark': '/addons/x/icon.svg', 'logo-dark': all['logo-dark']}, 'icon', false)).toEqual([all['logo-dark']]);
        expect(imageCandidates({icon: '/api/x"onerror=alert(1)'}, 'icon', false)).toEqual([]);
    });
});
