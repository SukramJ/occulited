import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {LINGER, RETRY_MAX, RETRY_MIN, SETTLE, StreamHub, streamURL, type HubClient, type ToClient} from './hub';

// occulited B-53: one stream for every window of the browser

class FakeStream extends EventTarget {
    readyState = 0;
    closed = false;
    constructor(public url: string) {
        super();
    }
    close() {
        this.closed = true;
        this.readyState = 2;
    }
    opened() {
        this.readyState = 1;
        this.dispatchEvent(new Event('open'));
    }
    send(event: string, data: string) {
        this.dispatchEvent(new MessageEvent(event, {data}));
    }
    fail(readyState: 0 | 2) {
        this.readyState = readyState;
        this.dispatchEvent(new Event('error'));
    }
}

function rig() {
    const streams: FakeStream[] = [];
    const hub = new StreamHub({
        open: (url) => {
            const s = new FakeStream(url);
            streams.push(s);
            return s;
        },
    });
    const client = () => {
        const got: ToClient[] = [];
        const c: HubClient & {got: ToClient[]; events: () => string[]} = {got, post: (m) => got.push(m), events: () => got.map((m) => (m.t === 'event' ? `${m.topic}:${m.data}` : m.t))};
        return c;
    };
    const open = () => streams.filter((s) => !s.closed);
    return {hub, streams, client, open, last: () => streams[streams.length - 1]!};
}

describe('StreamHub', () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it('holds one stream for every window, with the topics the shown ones want', () => {
        const {hub, streams, client, open, last} = rig();
        const a = client();
        const b = client();
        const c = client();
        hub.update(a, {topics: ['addons'], visible: true, key: 's1'});
        hub.update(b, {topics: ['addons', 'service-messages'], visible: true, key: 's1'});
        hub.update(c, {topics: ['pairing'], visible: true, key: 's1'});
        expect(streams).toHaveLength(0); // gathered first
        vi.advanceTimersByTime(SETTLE);
        expect(streams).toHaveLength(1);
        expect(last().url).toBe(streamURL(['addons', 'pairing', 'service-messages']));
        expect(hub.url).toBe('/api/system/v1/stream?topics=addons,pairing,service-messages');
        last().opened();
        last().send('addons', '{"revision":1}');
        last().send('messages', '{"count":0}');
        last().send('pairing', '{"requests":[]}');
        expect(a.events()).toEqual(['addons:{"revision":1}']);
        expect(b.events()).toEqual(['addons:{"revision":1}', 'service-messages:{"count":0}']);
        expect(c.events()).toEqual(['pairing:{"requests":[]}']);
        // the same wants again: the stream stays
        hub.update(a, {topics: ['addons'], visible: true, key: 's1'});
        vi.advanceTimersByTime(LINGER * 2);
        expect(open()).toHaveLength(1);
    });

    it('a hidden window gets nothing, and on its return the last event of each topic at once', () => {
        const {hub, client, last} = rig();
        const a = client();
        const b = client();
        hub.update(a, {topics: ['addons', 'service-messages'], visible: true, key: ''});
        hub.update(b, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        last().send('addons', '1');
        hub.update(b, {topics: ['addons'], visible: false, key: ''});
        last().send('addons', '2');
        last().send('messages', 'm');
        expect(b.events()).toEqual(['addons:1']);
        hub.update(b, {topics: ['addons'], visible: true, key: ''});
        expect(b.events()).toEqual(['addons:1', 'addons:2']);
        // a topic it adds while the stream has it: its last event at once
        hub.update(b, {topics: ['addons', 'service-messages'], visible: true, key: ''});
        expect(b.events()).toEqual(['addons:1', 'addons:2', 'service-messages:m']);
        // and nothing twice for what it had
        hub.update(b, {topics: ['addons', 'service-messages'], visible: true, key: ''});
        expect(b.got).toHaveLength(3);
    });

    it('a topic more opens the stream anew with it; a topic less too', () => {
        const {hub, client, open, last} = rig();
        const a = client();
        hub.update(a, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        const first = last();
        hub.update(a, {topics: ['addons', 'pairing'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        expect(first.closed).toBe(true);
        expect(open()).toHaveLength(1);
        expect(last().url).toBe(streamURL(['addons', 'pairing']));
        // the old stream's late event is not passed on
        first.send('addons', 'late');
        expect(a.got).toHaveLength(0);
        hub.update(a, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        expect(last().url).toBe(streamURL(['addons']));
        expect(open()).toHaveLength(1);
    });

    it('with no window shown the stream closes after a moment; a window shown in time keeps it', () => {
        const {hub, streams, client, open} = rig();
        const a = client();
        hub.update(a, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        hub.update(a, {topics: ['addons'], visible: false, key: ''});
        vi.advanceTimersByTime(LINGER - 1);
        hub.update(a, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(LINGER * 2);
        expect(streams).toHaveLength(1);
        expect(open()).toHaveLength(1);
        hub.update(a, {topics: ['addons'], visible: false, key: ''});
        vi.advanceTimersByTime(LINGER);
        expect(open()).toHaveLength(0);
        // shown again: a new stream, whose first event everyone gets
        hub.update(a, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        expect(open()).toHaveLength(1);
    });

    it('the last window gone closes it; a window without topics opens nothing', () => {
        const {hub, streams, client, open} = rig();
        const a = client();
        const b = client();
        hub.update(b, {topics: [], visible: true, key: ''});
        vi.advanceTimersByTime(LINGER);
        expect(streams).toHaveLength(0);
        hub.update(a, {topics: ['service-messages'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        hub.remove(a);
        vi.advanceTimersByTime(LINGER);
        expect(open()).toHaveLength(0);
        hub.remove(a); // twice is nothing
    });

    it('another session opens the stream anew and forgets the old one\'s events', () => {
        const {hub, client, last, open} = rig();
        const a = client();
        hub.update(a, {topics: ['pairing'], visible: true, key: 's1'});
        vi.advanceTimersByTime(SETTLE);
        last().send('pairing', 'admin-only');
        const first = last();
        const b = client();
        hub.update(b, {topics: ['pairing'], visible: true, key: 's2'});
        // not the old session's event to the new one
        expect(b.got).toHaveLength(0);
        vi.advanceTimersByTime(SETTLE);
        expect(first.closed).toBe(true);
        expect(open()).toHaveLength(1);
    });

    it('a stream the browser gave up on is opened again, later each time; one that reconnects itself is left alone', () => {
        const {hub, streams, client, last} = rig();
        const a = client();
        hub.update(a, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        last().fail(0); // the browser reconnects itself
        vi.advanceTimersByTime(RETRY_MAX);
        expect(streams).toHaveLength(1);
        last().fail(2); // lighttpd's 503: it gave up
        vi.advanceTimersByTime(RETRY_MIN - 1);
        expect(streams).toHaveLength(1);
        vi.advanceTimersByTime(1);
        expect(streams).toHaveLength(2);
        last().fail(2);
        vi.advanceTimersByTime(RETRY_MIN * 2 - 1);
        expect(streams).toHaveLength(2);
        vi.advanceTimersByTime(1);
        expect(streams).toHaveLength(3);
        // open: the wait starts from the first again
        last().opened();
        last().fail(2);
        vi.advanceTimersByTime(RETRY_MIN);
        expect(streams).toHaveLength(4);
        // the wait stops at RETRY_MAX
        for (let i = 0; i < 8; i++) {
            last().fail(2);
            vi.advanceTimersByTime(RETRY_MAX);
        }
        expect(streams).toHaveLength(12);
        // a retry pending, the same wants: it is not opened twice
        last().fail(2);
        hub.update(a, {topics: ['addons'], visible: true, key: ''});
        vi.advanceTimersByTime(SETTLE);
        expect(streams).toHaveLength(12);
        // nobody shown: the pending retry is dropped with the stream
        hub.update(a, {topics: ['addons'], visible: false, key: ''});
        vi.advanceTimersByTime(RETRY_MAX * 2);
        expect(streams).toHaveLength(12);
    });
});
