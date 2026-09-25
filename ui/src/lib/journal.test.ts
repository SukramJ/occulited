import {describe, expect, it} from 'vitest';
import type {JournalConfig} from './api';
import {effectiveStorage, journalBody, parseStickTarget, stickName, stickTarget, switchEffect} from './journal';

describe('effectiveStorage', () => {
    it('reads the empty setting as the product default', () => {
        expect(effectiveStorage('', 'ram')).toBe('ram');
        expect(effectiveStorage('', 'persistent')).toBe('persistent');
        expect(effectiveStorage('ram', 'persistent')).toBe('ram');
        expect(effectiveStorage('persistent', 'ram')).toBe('persistent');
        expect(effectiveStorage('ram-sync', 'persistent')).toBe('ram-sync');
    });
});

describe('switchEffect', () => {
    const cases: [string, Parameters<typeof switchEffect>, ReturnType<typeof switchEffect>][] = [
        ['nothing changed', ['ram', 'ram', 'ram', 'ram'], 'none'],
        ['RAM to persistent applies at once', ['ram', 'persistent', 'ram', 'ram'], 'persistent'],
        ['the RAM default to persistent applies at once', ['', 'persistent', 'ram', 'ram'], 'persistent'],
        ['persistent to RAM waits for the reboot', ['persistent', 'ram', 'ram', 'persistent'], 'reboot'],
        ['the persistent default to RAM waits for the reboot', ['', 'ram', 'persistent', 'persistent'], 'reboot'],
        ['persistent to its own product default is no switch', ['persistent', '', 'persistent', 'persistent'], 'none'],
        ['RAM to the RAM default is no switch', ['ram', '', 'ram', 'ram'], 'none'],
        ['taking back a pending switch to RAM moves nothing', ['ram', 'persistent', 'ram', 'persistent'], 'none'],
        ['persistent that never mounted, to RAM, moves nothing', ['persistent', 'ram', 'ram', 'ram'], 'none'],
        ['RAM to ram-sync starts the copies at once', ['ram', 'ram-sync', 'ram', 'ram'], 'ram-sync'],
        ['the persistent default to ram-sync applies at once', ['', 'ram-sync', 'persistent', 'persistent'], 'ram-sync'],
        ['ram-sync to RAM stops the copies', ['ram-sync', 'ram', 'ram', 'ram-sync'], 'stop-copies'],
        ['ram-sync to persistent applies at once', ['ram-sync', 'persistent', 'ram', 'ram-sync'], 'persistent'],
        ['taking back a pending switch from ram-sync to RAM moves nothing', ['ram', 'ram-sync', 'ram', 'ram-sync'], 'none'],
        ['ram-sync that never mounted, to RAM, moves nothing', ['ram-sync', 'ram', 'ram', 'ram'], 'none'],
    ];
    for (const [name, args, want] of cases) {
        it(name, () => expect(switchEffect(...args)).toBe(want));
    }
});

describe('journalBody', () => {
    const view: JournalConfig = {
        storage: 'ram',
        target: 'userfs',
        runtime_max_use: '8M',
        system_max_use: '32M',
        system_max_file: '',
        rate_limit_burst: '500',
        sync_interval: '1h',
        target_max_use: '',
        target_max_age: '30d',
        persist: '1',
        platform: 'ova',
        default_storage: 'persistent',
        default_persistent: true,
        persistent: true,
        effective: 'persistent',
        reboot_pending: true,
        ram_usage: 0,
        target_usage: 1024,
        target_free: null,
        target_ok: true,
        usage: 'Archived and active journals take up 32.4M in the file system.',
        last_sync: '2026-09-12T18:00:00Z',
        last_sync_result: 'ok',
        last_sync_copied: 3,
        next_sync: null,
    };
    it('carries the settings and storage, never persist, the figures or the copies', () => {
        const body = journalBody(view);
        expect(body).toEqual({
            storage: 'ram',
            target: 'userfs',
            runtime_max_use: '8M',
            system_max_use: '32M',
            system_max_file: '',
            rate_limit_burst: '500',
            sync_interval: '1h',
            target_max_use: '',
            target_max_age: '30d',
        });
        expect('persist' in body).toBe(false);
        expect('last_sync' in body).toBe(false);
    });
    it('keeps an empty storage as the product default', () => {
        expect(journalBody({...view, storage: ''}).storage).toBe('');
    });
});

describe('the USB stick target (task 216)', () => {
    it('puts usb:<label>/<dir> together and takes it apart', () => {
        expect(stickTarget('LOG_STICK', 'journal')).toBe('usb:LOG_STICK/journal');
        expect(stickTarget('LOG_STICK', '/ccu/journal/')).toBe('usb:LOG_STICK/ccu/journal');
        expect(stickTarget('LOG_STICK', '  ')).toBe('usb:LOG_STICK/journal');
        expect(parseStickTarget('usb:LOG_STICK/ccu/journal')).toEqual({label: 'LOG_STICK', dir: 'ccu/journal'});
        expect(parseStickTarget('usb:LOG_STICK')).toEqual({label: 'LOG_STICK', dir: ''});
        expect(parseStickTarget('usb:')).toEqual({label: '', dir: ''});
        expect(parseStickTarget('usb:/journal')).toEqual({label: '', dir: 'journal'});
        expect(parseStickTarget('userfs')).toBeNull();
        expect(parseStickTarget('')).toBeNull();
    });
    it('names a stick by its label, else vendor and model, else the device', () => {
        const base = {mount: '/media/usb1', path: '/media/usb1', device: '/dev/sda1', fstype: 'vfat', free_bytes: 0};
        expect(stickName({...base, label: 'LOG STICK', vendor: 'SanDisk'})).toBe('LOG STICK');
        expect(stickName({...base, vendor: 'SanDisk', model: 'Cruzer'})).toBe('SanDisk Cruzer');
        expect(stickName(base)).toBe('/dev/sda1');
    });
});
