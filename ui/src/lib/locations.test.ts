import {describe, expect, it} from 'vitest';
import {folderError, formatLocation, parseLocation, stateTone} from './locations';

describe('locations', () => {
    it('parses and formats the stored form', () => {
        expect(parseLocation('userfs:etc/occulite/data')).toEqual({id: 'userfs', kind: 'userfs', name: '', folder: 'etc/occulite/data'});
        expect(parseLocation('usb:LOGSTICK/journal')).toEqual({id: 'usb:LOGSTICK', kind: 'usb', name: 'LOGSTICK', folder: 'journal'});
        expect(parseLocation('share:nas')).toEqual({id: 'share:nas', kind: 'share', name: 'nas', folder: ''});
        expect(parseLocation('share:nas/a/b/')).toEqual({id: 'share:nas', kind: 'share', name: 'nas', folder: 'a/b'});
        expect(parseLocation('userfs:')).toBeNull();
        expect(parseLocation('/media/usb1/backup')).toBeNull();
        expect(parseLocation('sftp:x')).toBeNull();
        expect(formatLocation('userfs', '/backup/')).toBe('userfs:backup');
        expect(formatLocation('usb:LOGSTICK', 'journal')).toBe('usb:LOGSTICK/journal');
        expect(formatLocation('share:nas', '')).toBe('share:nas');
    });
    it('checks a folder', () => {
        expect(folderError('journal', false)).toBe('');
        expect(folderError('', false)).toContain('required');
        expect(folderError('', true)).toBe('');
        expect(folderError('a/b/c/d/e', false)).toContain('four');
        expect(folderError('a/../b', false)).toContain('letters');
        expect(folderError('.hidden', false)).toContain('letters');
        expect(folderError('a b', false)).toContain('letters');
        expect(folderError('backup/ccu', false, 'backup')).toBe('');
        expect(folderError('etc', false, 'backup')).toContain('{prefix}');
        expect(folderError('var/log', false, 'var/log/journal', true)).toContain('always');
    });
    it('gives a state its tone', () => {
        expect(stateTone('present')).toBe('good');
        expect(stateTone('mounted')).toBe('good');
        expect(stateTone('idle')).toBe('idle');
        expect(stateTone('unreachable')).toBe('bad');
        expect(stateTone('missing')).toBe('bad');
    });
});
