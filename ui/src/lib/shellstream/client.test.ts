import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {ShellStream, type ClientEnv} from './client';
import {SETTLE} from './hub';

// occulited B-53: a window's side of the shared stream

class FakePort extends EventTarget {
    sent: unknown[] = [];
    closed = false;
    started = false;
    postMessage(m: unknown) {
        this.sent.push(m);
    }
    start() {
        this.started = true;
    }
    close() {
        this.closed = true;
    }
    deliver(data: unknown) {
        this.dispatchEvent(new MessageEvent('message', {data}));
    }
}

class FakeWorker extends EventTarget {
    port = new FakePort();
}

class FakeStream extends EventTarget {
    readyState = 1;
    closed = false;
    constructor(public url: string) {
        super();
    }
    close() {
        this.closed = true;
        this.readyState = 2;
    }
}

function rig(o: {shared?: 'none' | 'throws'} = {}) {
    const doc = new EventTarget() as EventTarget & {visibilityState: DocumentVisibilityState};
    doc.visibilityState = 'visible';
    const win = new EventTarget();
    const workers: FakeWorker[] = [];
    const streams: FakeStream[] = [];
    const env: ClientEnv = {
        doc: doc as unknown as ClientEnv['doc'],
        win: win as unknown as ClientEnv['win'],
        shared:
            o.shared === 'none'
                ? null
                : () => {
                      if (o.shared === 'throws') throw new Error('SecurityError');
                      const w = new FakeWorker();
                      workers.push(w);
                      return w as unknown as SharedWorker;
                  },
        open: (url) => {
            const s = new FakeStream(url);
            streams.push(s);
            return s;
        },
    };
    const setVisible = (v: boolean) => {
        doc.visibilityState = v ? 'visible' : 'hidden';
        doc.dispatchEvent(new Event('visibilitychange'));
    };
    return {s: new ShellStream(env), workers, streams, win, setVisible};
}

const tick = () => Promise.resolve();

describe('ShellStream', () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it('tells the worker its topics and whether it is shown, and passes the events on', async () => {
        const {s, workers, setVisible, win} = rig();
        const got: string[] = [];
        const stop = s.subscribe('addons', (d) => got.push(`a:${d}`));
        s.subscribe('service-messages', (d) => got.push(`m:${d}`));
        s.setKey('sid1');
        expect(s.mode).toBe('shared');
        expect(workers).toHaveLength(1);
        const port = workers[0]!.port;
        expect(port.started).toBe(true);
        await tick();
        // the changes of one turn go out as one message
        expect(port.sent).toEqual([{t: 'want', topics: ['addons', 'service-messages'], visible: true, key: 'sid1'}]);
        port.deliver({t: 'event', topic: 'addons', data: '1'});
        port.deliver({t: 'event', topic: 'service-messages', data: 'x'});
        port.deliver({t: 'event', topic: 'pairing', data: 'p'});
        expect(got).toEqual(['a:1', 'm:x']);
        setVisible(false);
        await tick();
        expect(port.sent.at(-1)).toEqual({t: 'want', topics: ['addons', 'service-messages'], visible: false, key: 'sid1'});
        stop();
        await tick();
        expect(port.sent.at(-1)).toEqual({t: 'want', topics: ['service-messages'], visible: false, key: 'sid1'});
        win.dispatchEvent(new Event('pagehide'));
        expect(port.sent.at(-1)).toEqual({t: 'bye'});
        const back = new Event('pageshow') as Event & {persisted: boolean};
        back.persisted = true;
        win.dispatchEvent(back);
        await tick();
        expect(port.sent.at(-1)).toEqual({t: 'want', topics: ['service-messages'], visible: false, key: 'sid1'});
    });

    it("a listener's error does not keep the event from the others", async () => {
        const {s, workers} = rig();
        const got: string[] = [];
        s.subscribe('addons', () => {
            throw new Error('mine');
        });
        s.subscribe('addons', (d) => got.push(d));
        workers[0]!.port.deliver({t: 'event', topic: 'addons', data: '1'});
        expect(got).toEqual(['1']);
        expect(() => vi.runOnlyPendingTimers()).toThrow('mine');
    });

    for (const [why, shared, trigger] of [
        ['no SharedWorker in the browser', 'none', null],
        ['a SharedWorker that may not be made here', 'throws', null],
        ['a worker whose script fails', undefined, 'error'],
        ['a worker without EventSource', undefined, 'unsupported'],
    ] as const) {
        it(`${why}: the window holds the stream itself`, async () => {
            const {s, workers, streams, setVisible} = rig({shared});
            const got: string[] = [];
            s.subscribe('service-messages', (d) => got.push(d));
            const w = workers[0];
            if (trigger === 'error') w!.dispatchEvent(new Event('error'));
            if (trigger === 'unsupported') w!.port.deliver({t: 'unsupported'});
            if (w) expect(w.port.closed).toBe(true);
            expect(s.mode).toBe('local');
            await tick();
            vi.advanceTimersByTime(SETTLE);
            expect(streams).toHaveLength(1);
            expect(streams[0]!.url).toBe('/api/system/v1/stream?topics=service-messages');
            streams[0]!.dispatchEvent(new MessageEvent('messages', {data: 'v'}));
            expect(got).toEqual(['v']);
            // the worker's late word is ignored
            w?.port.deliver({t: 'event', topic: 'service-messages', data: 'late'});
            expect(got).toEqual(['v']);
            // hidden: closed after a moment
            setVisible(false);
            await tick();
            vi.advanceTimersByTime(5000);
            expect(streams[0]!.closed).toBe(true);
        });
    }

    it('nothing is connected before the first subscription', () => {
        const {s, workers} = rig();
        s.setKey('k');
        expect(workers).toHaveLength(0);
        expect(s.mode).toBe('');
    });
});
