import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {openEventStream, RETRY_MAX, RETRY_MIN, SILENCE, SSEParser, type SSEMessage} from './eventstream';

// occulited B-47: the App's stream is read with fetch(), so what an EventSource did by itself is
// tested here - the format, the reconnect with Last-Event-ID, and the headers the system asks for.

describe('SSEParser', () => {
    it('reads the system\'s messages: id, event and one data line, comments skipped', () => {
        const p = new SSEParser();
        const out = p.push(': connected\n\nid: b-1\nevent: hello\ndata: {"seq":1}\n\n: ping\n\nid: b-2\nevent: event\ndata: {"key":"STATE"}\n\n');
        expect(out).toEqual([
            {id: 'b-1', event: 'hello', data: '{"seq":1}'},
            {id: 'b-2', event: 'event', data: '{"key":"STATE"}'},
        ]);
        expect(p.lastId).toBe('b-2');
    });
    it('puts a message together from chunks cut anywhere, a CRLF cut in two included', () => {
        const whole = 'id: b-7\r\nevent: event\r\ndata: {"a":1}\r\n\r\nevent: resync\r\ndata: {"reason":"gap"}\r\n\r\n';
        for (let cut = 1; cut < whole.length; cut++) {
            const p = new SSEParser();
            const out = [...p.push(whole.slice(0, cut)), ...p.push(whole.slice(cut))];
            expect(out, `cut at ${cut}`).toEqual([
                {id: 'b-7', event: 'event', data: '{"a":1}'},
                // a message without an id keeps the last one: where a reconnect resumes
                {id: 'b-7', event: 'resync', data: '{"reason":"gap"}'},
            ]);
        }
    });
    it('follows the format\'s corners: several data lines, no event name, no space, a lone CR, retry', () => {
        const p = new SSEParser();
        expect(p.push('data:one\ndata: two\n\n')).toEqual([{id: '', event: 'message', data: 'one\ntwo'}]);
        // a CR ends a line too; the one at the very end waits for what follows (it may be half a CRLF)
        expect(p.push('event: x\rdata\r\r')).toEqual([]);
        expect(p.push(': ping\n')).toEqual([{id: '', event: 'x', data: ''}]);
        // an event name without data is no message, and does not stick to the next one
        expect(p.push('event: lost\n\ndata: y\n\n')).toEqual([{id: '', event: 'message', data: 'y'}]);
        expect(p.push('retry: 5000\nretry: soon\nid: a\0b\nunknown: z\n\n')).toEqual([]);
        expect(p.retry).toBe(5000);
        expect(p.lastId).toBe('');
        // nothing is given out before the blank line
        expect(p.push('id: 9\ndata: half')).toEqual([]);
        expect(p.push('\n')).toEqual([]);
        expect(p.push('\n')).toEqual([{id: '9', event: 'message', data: 'half'}]);
    });
});

/** A fetch whose answers the test hands out one by one; each call is recorded with its headers. */
function fakeFetch() {
    const calls: {url: string; headers: Record<string, string>; signal: AbortSignal; push: (text: string) => void; end: () => void}[] = [];
    const answers: ((call: (typeof calls)[number]) => Response | Promise<Response>)[] = [];
    const f = ((url: string, init: RequestInit) => {
        const enc = new TextEncoder();
        let ctl!: ReadableStreamDefaultController<Uint8Array>;
        const body = new ReadableStream<Uint8Array>({start: (c) => void (ctl = c)});
        const signal = init.signal as AbortSignal;
        signal.addEventListener('abort', () => {
            try {
                ctl.error(new DOMException('aborted', 'AbortError'));
            } catch {
                /* ended already */
            }
        });
        const call = {url, headers: init.headers as Record<string, string>, signal, push: (text: string) => ctl.enqueue(enc.encode(text)), end: () => ctl.close()};
        calls.push(call);
        const answer = answers.shift();
        if (answer) return Promise.resolve(answer(call));
        return Promise.resolve(new Response(body, {status: 200, headers: {'Content-Type': 'text/event-stream'}}));
    }) as unknown as typeof fetch;
    /** the nth call, which the test expects to have been made */
    const at = (i: number) => {
        const c = calls[i];
        if (!c) throw new Error(`no call ${i}: ${calls.length} were made`);
        return c;
    };
    return {f, calls, answers, at};
}
const settle = () => vi.advanceTimersByTimeAsync(0);

describe('openEventStream', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.stubGlobal('sessionStorage', {getItem: (k: string) => (k === 'ol.sid' ? 'S1' : null)});
    });
    afterEach(() => {
        vi.useRealTimers();
        vi.unstubAllGlobals();
    });

    it('sends the header credential and the session as Bearer, and where to resume', async () => {
        const {f, calls, at} = fakeFetch();
        const got: SSEMessage[] = [];
        const close = openEventStream({url: '/api/rpc/v1/events?type=event', lastEventId: 'b-40', onMessage: (m) => got.push(m), fetch: f});
        await settle();
        expect(calls).toHaveLength(1);
        expect(at(0).url).toBe('/api/rpc/v1/events?type=event');
        // what a page of another origin cannot send, and an EventSource not at all
        expect(at(0).headers).toMatchObject({'X-Occulite-Request': '1', Authorization: 'Bearer S1', 'Last-Event-ID': 'b-40', Accept: 'text/event-stream'});
        at(0).push('id: b-41\nevent: event\ndata: {"key":"STATE"}\n\n');
        await settle();
        expect(got).toEqual([{id: 'b-41', event: 'event', data: '{"key":"STATE"}'}]);
        close();
        await settle();
        expect(at(0).signal.aborted).toBe(true);
    });

    it('without a session (the public mode) the header credential alone goes out', async () => {
        vi.stubGlobal('sessionStorage', {getItem: () => null});
        const {f, calls, at} = fakeFetch();
        const close = openEventStream({url: '/e', onMessage: () => undefined, fetch: f});
        await settle();
        expect(at(0).headers['X-Occulite-Request']).toBe('1');
        expect(at(0).headers).not.toHaveProperty('Authorization');
        expect(at(0).headers).not.toHaveProperty('Last-Event-ID');
        close();
    });

    it('a stream that ends is opened again after a second, resuming after the last id seen', async () => {
        const {f, calls, at} = fakeFetch();
        const got: string[] = [];
        const close = openEventStream({url: '/e', onMessage: (m) => got.push(m.event), fetch: f});
        await settle();
        at(0).push('id: b-1\nevent: hello\ndata: {}\n\nid: b-2\nevent: event\ndata: {}\n\nevent: resync\ndata: {"reason":"overflow"}\n\n');
        await settle();
        at(0).end(); // the system restarts, or the Interfaces page's x
        await vi.advanceTimersByTimeAsync(RETRY_MIN - 1);
        expect(calls).toHaveLength(1);
        await vi.advanceTimersByTimeAsync(1);
        expect(calls).toHaveLength(2);
        expect(at(1).headers['Last-Event-ID']).toBe('b-2');
        expect(got).toEqual(['hello', 'event', 'resync']);
        close();
    });

    it('an answer that is not the stream yet (429, 502) and a network error are tried again, each wait twice the last', async () => {
        const {f, calls, at, answers} = fakeFetch();
        answers.push(
            () => new Response('{"error":"too-many-streams"}', {status: 429}),
            () => new Response('', {status: 502}),
            () => Promise.reject(new TypeError('Failed to fetch')),
        );
        const refused = vi.fn();
        const close = openEventStream({url: '/e', onMessage: () => undefined, onRefused: refused, fetch: f});
        await settle();
        expect(calls).toHaveLength(1);
        await vi.advanceTimersByTimeAsync(RETRY_MIN);
        expect(calls).toHaveLength(2);
        await vi.advanceTimersByTimeAsync(2 * RETRY_MIN - 1);
        expect(calls).toHaveLength(2);
        await vi.advanceTimersByTimeAsync(1);
        expect(calls).toHaveLength(3);
        await vi.advanceTimersByTimeAsync(4 * RETRY_MIN);
        expect(calls).toHaveLength(4); // the stream at last
        expect(refused).not.toHaveBeenCalled();
        // once it streamed, the next end is followed by the short wait again
        at(3).end();
        await vi.advanceTimersByTimeAsync(RETRY_MIN);
        expect(calls).toHaveLength(5);
        close();
    });

    it('says where it stands: busy after a 429, reconnecting after a 502, a network error or an end, live while open', async () => {
        const {f, at, answers} = fakeFetch();
        answers.push(
            () => new Response('{"error":"too-many-streams"}', {status: 429}),
            () => new Response('{"error":"too-many-streams"}', {status: 429}),
            () => new Response('', {status: 502}),
            () => Promise.reject(new TypeError('Failed to fetch')),
        );
        const states: string[] = [];
        const close = openEventStream({url: '/e', onMessage: () => undefined, onState: (s) => states.push(s), fetch: f});
        await settle();
        // occulited task 19: another window holds the account's streams - the same state is told once
        expect(states).toEqual(['busy']);
        await vi.advanceTimersByTimeAsync(RETRY_MIN);
        expect(states).toEqual(['busy']);
        await vi.advanceTimersByTimeAsync(2 * RETRY_MIN);
        expect(states).toEqual(['busy', 'reconnecting']);
        await vi.advanceTimersByTimeAsync(4 * RETRY_MIN);
        expect(states).toEqual(['busy', 'reconnecting']);
        await vi.advanceTimersByTimeAsync(8 * RETRY_MIN);
        expect(states).toEqual(['busy', 'reconnecting', 'live']);
        at(4).end(); // the system restarts
        await settle();
        expect(states).toEqual(['busy', 'reconnecting', 'live', 'reconnecting']);
        await vi.advanceTimersByTimeAsync(RETRY_MIN);
        expect(states).toEqual(['busy', 'reconnecting', 'live', 'reconnecting', 'live']);
        close(); // aborts the open stream
        await vi.advanceTimersByTimeAsync(RETRY_MAX);
        // nothing after the page let it go
        expect(states).toHaveLength(5);
    });

    it('a refusal says refused, nothing before the first answer', async () => {
        const {f, answers} = fakeFetch();
        let answer!: (r: Response) => void;
        answers.push(() => new Promise<Response>((resolve) => (answer = resolve)));
        const states: string[] = [];
        openEventStream({url: '/e', onMessage: () => undefined, onState: (s) => states.push(s), fetch: f});
        await settle();
        expect(states).toEqual([]);
        answer(new Response('{"error":"forbidden"}', {status: 403}));
        await settle();
        expect(states).toEqual(['refused']);
    });

    it('the wait between attempts stops growing at half a minute', async () => {
        const {f, calls, at, answers} = fakeFetch();
        for (let i = 0; i < 12; i++) answers.push(() => new Response('', {status: 503}));
        const close = openEventStream({url: '/e', onMessage: () => undefined, fetch: f});
        await vi.advanceTimersByTimeAsync(RETRY_MIN * (1 + 2 + 4 + 8 + 16)); // 31 s: six attempts
        expect(calls).toHaveLength(6);
        await vi.advanceTimersByTimeAsync(RETRY_MAX - 1);
        expect(calls).toHaveLength(6);
        await vi.advanceTimersByTimeAsync(1);
        expect(calls).toHaveLength(7);
        await vi.advanceTimersByTimeAsync(RETRY_MAX);
        expect(calls).toHaveLength(8);
        close();
    });

    it('a refusal (401, 403) ends it: nothing is asked again, and the caller is told', async () => {
        for (const status of [401, 403, 400, 501]) {
            const {f, calls, at, answers} = fakeFetch();
            answers.push(() => new Response('{"error":"forbidden"}', {status}));
            const refused = vi.fn();
            openEventStream({url: '/e', onMessage: () => undefined, onRefused: refused, fetch: f});
            await vi.advanceTimersByTimeAsync(5 * RETRY_MAX);
            expect(calls, String(status)).toHaveLength(1);
            expect(refused).toHaveBeenCalledWith(status);
        }
    });

    it('45 s without a byte: the connection is given up and opened again; a ping keeps it', async () => {
        const {f, calls, at} = fakeFetch();
        const close = openEventStream({url: '/e', onMessage: () => undefined, fetch: f});
        await settle();
        at(0).push('id: b-5\nevent: hello\ndata: {}\n\n');
        await vi.advanceTimersByTimeAsync(SILENCE - 1000);
        at(0).push(': ping\n\n');
        await vi.advanceTimersByTimeAsync(SILENCE - 1000);
        expect(at(0).signal.aborted).toBe(false);
        await vi.advanceTimersByTimeAsync(1000);
        expect(at(0).signal.aborted).toBe(true);
        await vi.advanceTimersByTimeAsync(RETRY_MIN);
        expect(calls).toHaveLength(2);
        expect(at(1).headers['Last-Event-ID']).toBe('b-5');
        close();
    });

    it('closed while it waits for the next attempt: no attempt follows; closed in a listener: no further message', async () => {
        const {f, calls, at, answers} = fakeFetch();
        answers.push(() => new Response('', {status: 503}));
        const close = openEventStream({url: '/e', onMessage: () => undefined, fetch: f});
        await settle();
        close();
        await vi.advanceTimersByTimeAsync(5 * RETRY_MAX);
        expect(calls).toHaveLength(1);

        const two = fakeFetch();
        const got: string[] = [];
        const close2 = openEventStream({url: '/e', onMessage: (m) => (got.push(m.data), close2()), fetch: two.f});
        await settle();
        two.at(0).push('data: 1\n\ndata: 2\n\n');
        await vi.advanceTimersByTimeAsync(5 * RETRY_MAX);
        expect(got).toEqual(['1']);
        expect(two.calls).toHaveLength(1);
    });
});
