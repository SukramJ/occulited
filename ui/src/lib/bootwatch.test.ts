import {afterEach, describe, expect, it, vi} from 'vitest';
import {newEntry, type BootEntry} from './bootbar';
import {beginBoot, probeHealth, watchBoot} from './bootwatch';

// the health route as a sequence of answers: a number is an uptime, null a failed request, 503 lighttpd alone
function healthAnswers(answers: (number | null | 503)[]) {
    let n = 0;
    const fetch = vi.fn(async () => {
        const a = answers[Math.min(n++, answers.length - 1)];
        if (a === null) throw new TypeError('Failed to fetch');
        if (a === 503) return new Response('{"error":"starting"}', {status: 503});
        return new Response(JSON.stringify({uptime_s: a}), {status: 200});
    });
    vi.stubGlobal('fetch', fetch);
    return fetch;
}

const EXPECT = {down: 5, http: 30, ui: 2, ready: 40};

describe('waiting for the box to come back', () => {
    afterEach(() => {
        vi.useRealTimers();
        vi.unstubAllGlobals();
    });

    it('one poll: the status and the JSON, null for no answer', async () => {
        healthAnswers([4711]);
        expect(await probeHealth()).toEqual({status: 200, body: {uptime_s: 4711}});
        healthAnswers([null]);
        expect(await probeHealth()).toEqual({status: null, body: null});
        healthAnswers([503]);
        expect(await probeHealth()).toEqual({status: 503, body: {error: 'starting'}});
    });

    it.each<[string, number, (number | null | 503)[], number, string[]]>([
        // polls: the box still up, then a fresh boot with a smaller uptime
        ['a smaller uptime is a new boot', 1000, [1001, 1003, 12], 3, ['ui']],
        ['an answer after none is the box back', 1000, [1001, null, null, 1009], 4, ['down', 'ui']],
        // lighttpd answers alone while occulited stops, then the box goes, then lighttpd boots
        ["lighttpd's 503 counts after the box was gone", -1, [503, null, 503, 7000], 4, ['down', 'http', 'ui']],
        ['without a known uptime, only an answer after a gap', -1, [5000, 5002, null, 9000], 4, ['down', 'ui']],
        ['the whole boot', 1000, [1001, null, null, 503, 503, 2], 6, ['down', 'http', 'ui']],
    ])('%s', async (_what, before, answers, polls, seen) => {
        vi.useFakeTimers();
        const fetch = healthAnswers(answers);
        const back = vi.fn();
        const updates: BootEntry[] = [];
        watchBoot({entry: newEntry('reboot', Date.now(), EXPECT), before, pollMs: 1000, onBack: back, onUpdate: (e) => updates.push(e)});
        for (let i = 1; i < polls; i++) {
            await vi.advanceTimersByTimeAsync(1000);
            expect(back).not.toHaveBeenCalled();
        }
        await vi.advanceTimersByTimeAsync(1000);
        expect(back).toHaveBeenCalledOnce();
        const final = back.mock.calls[0]![0] as BootEntry;
        expect(Object.keys(final.seen)).toEqual(seen);
        expect(updates.at(-1)).toEqual(final);
        // and nothing is polled after that
        const calls = fetch.mock.calls.length;
        await vi.advanceTimersByTimeAsync(5000);
        expect(fetch.mock.calls.length).toBe(calls);
        expect(back).toHaveBeenCalledOnce();
    });

    it('a halt stops at the first poll without an answer', async () => {
        vi.useFakeTimers();
        const fetch = healthAnswers([1001, 503, null, 7]);
        const down = vi.fn(() => true);
        const back = vi.fn();
        watchBoot({entry: newEntry('halt', Date.now(), EXPECT), before: -1, pollMs: 1000, onDown: down, onBack: back});
        await vi.advanceTimersByTimeAsync(3000);
        expect(down).toHaveBeenCalledOnce();
        await vi.advanceTimersByTimeAsync(5000);
        expect(fetch.mock.calls.length).toBe(3);
        expect(back).not.toHaveBeenCalled();
    });

    it('stops when told to', async () => {
        vi.useFakeTimers();
        const fetch = healthAnswers([null]);
        const back = vi.fn();
        const stop = watchBoot({entry: newEntry('reboot', Date.now(), EXPECT), before: 1000, pollMs: 1000, onBack: back});
        await vi.advanceTimersByTimeAsync(2000);
        stop();
        const calls = fetch.mock.calls.length;
        await vi.advanceTimersByTimeAsync(5000);
        expect(fetch.mock.calls.length).toBe(calls);
        expect(back).not.toHaveBeenCalled();
    });
});

describe('writing the entry before the request', () => {
    it("takes the box's expectation", async () => {
        const e = await beginBoot('update', async () => ({kind: 'update', expect: {down: 4, http: 400, ui: 2, ready: 45}}), 'rpi4');
        expect(e).toMatchObject({v: 1, kind: 'update', expect: {down: 4, http: 400, ui: 2, ready: 45}, seen: {}});
    });

    it('falls back to the built-in figures when the box does not answer, answers nonsense or is slow', async () => {
        const failing = await beginBoot('reboot', async () => {
            throw new Error('501');
        }, 'ova');
        expect(failing.expect.http).toBeGreaterThan(0);
        const nonsense = await beginBoot('reboot', async () => ({}), 'ova');
        expect(nonsense.expect).toEqual(failing.expect);
        vi.useFakeTimers();
        const slow = beginBoot('reboot', () => new Promise(() => {}), 'ova', 1000);
        await vi.advanceTimersByTimeAsync(1000);
        expect((await slow).expect).toEqual(failing.expect);
        vi.useRealTimers();
    });
});
