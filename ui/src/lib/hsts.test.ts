import {describe, expect, it} from 'vitest';
import {clearingUntil, hstsNames, hstsRemembered} from './hsts';

// task 96: the names a browser keeps HSTS for, and the clearing state of GET /https
describe('hstsNames', () => {
    const view = {redirect_fqdn_host: 'ccu', redirect_fqdn_target: 'ccu.example.org'};
    it('lists the page host first, then <host>.<domain> and the bare name, each once', () => {
        expect(hstsNames('ccu.example.org', view)).toEqual(['ccu.example.org', 'ccu']);
        expect(hstsNames('CCU', view)).toEqual(['ccu', 'ccu.example.org']);
        expect(hstsNames('ccu.example.org.', view)).toEqual(['ccu.example.org', 'ccu']);
    });
    it('never an IP literal or localhost, and nothing without names', () => {
        expect(hstsNames('192.0.2.7', {redirect_fqdn_host: 'ccu', redirect_fqdn_target: null})).toEqual(['ccu']);
        expect(hstsNames('[fe80::1]', null)).toEqual([]);
        expect(hstsNames('localhost', {redirect_fqdn_host: '', redirect_fqdn_target: null})).toEqual([]);
    });
});

describe('hstsRemembered', () => {
    it('is on, or off while the box still clears', () => {
        expect(hstsRemembered({hsts: true})).toBe(true);
        expect(hstsRemembered({hsts: false, hsts_clearing: true})).toBe(true);
        expect(hstsRemembered({hsts: false, hsts_clearing: false})).toBe(false);
        // a daemon without the field
        expect(hstsRemembered({hsts: false})).toBe(false);
    });
});

describe('clearingUntil', () => {
    it('is the deadline while clearing, else null', () => {
        expect(clearingUntil({hsts_clearing: true, hsts_clearing_until: '2026-10-12T20:00:00Z'})?.toISOString()).toBe('2026-10-12T20:00:00.000Z');
        expect(clearingUntil({hsts_clearing: true})).toBeNull();
        expect(clearingUntil({hsts_clearing: false, hsts_clearing_until: '2026-10-12T20:00:00Z'})).toBeNull();
        expect(clearingUntil({hsts_clearing: true, hsts_clearing_until: 'soon'})).toBeNull();
        expect(clearingUntil(null)).toBeNull();
    });
});
