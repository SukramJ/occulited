import {describe, expect, it} from 'vitest';
import {nameError, normalisePath, source, suggestName} from './shares';

describe('shares', () => {
    it('checks the name the mount point is made of', () => {
        expect(nameError('nas')).toBe('');
        expect(nameError('nas2')).toBe('');
        expect(nameError('')).toContain('required');
        expect(nameError('NAS')).toContain('Lower-case');
        expect(nameError('my-nas')).toContain('Lower-case');
        expect(nameError('2nas')).toContain('Lower-case');
        expect(nameError('a'.repeat(17))).toContain('16');
    });
    it('suggests a name from the server', () => {
        expect(suggestName('truenas.lan', [])).toBe('truenas');
        expect(suggestName('192.168.1.5', [])).toBe('nas');
        expect(suggestName('', ['nas'])).toBe('nas2');
        expect(suggestName('My-NAS.local', ['mynas', 'mynas2'])).toBe('mynas3');
    });
    it('normalises the path per kind', () => {
        expect(normalisePath('cifs', '/backup/')).toBe('backup');
        expect(normalisePath('cifs', '\\\\data\\ccu')).toBe('data/ccu');
        expect(normalisePath('nfs', 'mnt/tank/data/')).toBe('/mnt/tank/data');
        expect(normalisePath('nfs', '')).toBe('');
    });
    it('names the source as a mount does', () => {
        expect(source({kind: 'nfs', server: 'nas', path: '/data'})).toBe('nas:/data');
        expect(source({kind: 'cifs', server: 'nas', path: 'data'})).toBe('//nas/data');
    });
});
