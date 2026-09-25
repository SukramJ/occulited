import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {LiveClock, SecondTicker, clockLocale} from './liveclock';

// openccu-lite task 225: the Time panel's clocks tick every second, the system's from its last
// reading plus the monotonic clock, re-anchored on every re-read

describe('LiveClock', () => {
    it('is the reading plus the monotonic time since, not the browser clock', () => {
        let mono = 5000;
        const c = new LiveClock(() => mono);
        expect(c.anchored).toBe(false);
        expect(Number.isNaN(c.now())).toBe(true);
        c.anchor('2026-09-24T20:19:33.000Z');
        expect(c.anchored).toBe(true);
        expect(new Date(c.now()).toISOString()).toBe('2026-09-24T20:19:33.000Z');
        mono += 2500;
        expect(new Date(c.now()).toISOString()).toBe('2026-09-24T20:19:35.500Z');
    });
    it('jumps with a new reading (an NTP step) and ignores a bad one', () => {
        let mono = 0;
        const c = new LiveClock(() => mono);
        c.anchor('2026-09-24T20:00:00Z');
        mono = 60_000;
        c.anchor('2026-09-24T21:00:00Z');
        expect(new Date(c.now()).toISOString()).toBe('2026-09-24T21:00:00.000Z');
        c.anchor('not a time');
        mono = 61_000;
        expect(new Date(c.now()).toISOString()).toBe('2026-09-24T21:00:01.000Z');
    });
});

describe('SecondTicker', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date('2026-09-24T20:19:33.400Z'));
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it('ticks at once, then at every full second of the browser clock', () => {
        const at: number[] = [];
        const tk = new SecondTicker(() => at.push(Date.now() % 1000));
        tk.run(true);
        expect(at).toEqual([400]);
        vi.advanceTimersByTime(599);
        expect(at).toHaveLength(1);
        vi.advanceTimersByTime(1);
        expect(at).toEqual([400, 0]);
        vi.advanceTimersByTime(3000);
        expect(at).toEqual([400, 0, 0, 0, 0]);
        tk.stop();
    });
    it('pauses (a hidden tab) and resumes aligned again; one timer at a time', () => {
        let n = 0;
        const tk = new SecondTicker(() => n++);
        tk.run(true);
        vi.advanceTimersByTime(2600);
        expect(n).toBe(4);
        tk.run(false);
        expect(tk.running).toBe(false);
        expect(vi.getTimerCount()).toBe(0);
        vi.advanceTimersByTime(10_000);
        expect(n).toBe(4);
        tk.run(true);
        tk.run(true);
        expect(n).toBe(5);
        expect(vi.getTimerCount()).toBe(1);
        vi.advanceTimersByTime(1000);
        expect(n).toBe(6);
        expect(vi.getTimerCount()).toBe(1);
        tk.stop();
    });
    it('stop clears every timer and cannot be restarted', () => {
        let n = 0;
        const tk = new SecondTicker(() => n++);
        tk.run(true);
        vi.advanceTimersByTime(1500);
        tk.stop();
        expect(vi.getTimerCount()).toBe(0);
        tk.run(true);
        vi.advanceTimersByTime(5000);
        expect(n).toBe(2);
        expect(vi.getTimerCount()).toBe(0);
    });
});

describe('clockLocale', () => {
    it.each([
        ['en', ['en-US', 'de-DE'], 'en-US'],
        ['de', ['en-US', 'de-AT'], 'de-AT'],
        ['de', ['en-US'], 'de-DE'],
        ['en', ['de-DE'], 'en-GB'],
        ['en', [], 'en-GB'],
    ])('%s with %j is %s', (lang, browser, want) => {
        expect(clockLocale(lang, browser)).toBe(want);
    });
});
