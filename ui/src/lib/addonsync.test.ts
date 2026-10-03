import {describe, expect, it} from 'vitest';
import {ADDONS_STREAM, addonsChanged, watchAddons, type WatchEnv} from './addonsync';

// openccu-lite B-297: the shell reads its menu again when the addons changed

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
    send(revision: unknown) {
        this.dispatchEvent(new MessageEvent('addons', {data: JSON.stringify({revision})}));
    }
}

function env() {
    const doc = new EventTarget() as EventTarget & {visibilityState: DocumentVisibilityState};
    doc.visibilityState = 'visible';
    const streams: FakeStream[] = [];
    const e: WatchEnv = {
        doc: doc as unknown as WatchEnv['doc'],
        open: (url) => {
            const s = new FakeStream(url);
            streams.push(s);
            return s as unknown as EventSource;
        },
    };
    const setVisible = (v: boolean) => {
        doc.visibilityState = v ? 'visible' : 'hidden';
        doc.dispatchEvent(new Event('visibilitychange'));
    };
    return {e, streams, setVisible, last: () => streams[streams.length - 1]!};
}

describe('watchAddons', () => {
    it('reads again when the revision moves, not for the first one or the same one', () => {
        const {e, streams, last} = env();
        let n = 0;
        const stop = watchAddons(() => n++, e);
        expect(streams).toHaveLength(1);
        expect(last().url).toBe(ADDONS_STREAM);
        last().send(100);
        expect(n).toBe(0);
        last().send(100);
        expect(n).toBe(0);
        last().send(101);
        expect(n).toBe(1);
        // a restarted service starts from its clock: another number, read again
        last().send(5);
        expect(n).toBe(2);
        // what is no revision is ignored
        last().send('x');
        last().dispatchEvent(new MessageEvent('addons', {data: '{'}));
        expect(n).toBe(2);
        stop();
        expect(last().closed).toBe(true);
    });

    it('holds no stream while the tab is hidden, and on its return reads again only when something moved', () => {
        const {e, streams, setVisible, last} = env();
        let n = 0;
        const stop = watchAddons(() => n++, e);
        last().send(7);
        setVisible(false);
        expect(last().closed).toBe(true);
        setVisible(true);
        expect(streams).toHaveLength(2);
        last().send(7);
        expect(n).toBe(0);
        setVisible(false);
        setVisible(true);
        expect(streams).toHaveLength(3);
        last().send(9);
        expect(n).toBe(1);
        stop();
        // stopped: a visibility change opens nothing
        setVisible(false);
        setVisible(true);
        expect(streams).toHaveLength(3);
    });

    it('a hidden tab opens its stream when it is shown', () => {
        const {e, streams, setVisible} = env();
        setVisible(false);
        const stop = watchAddons(() => undefined, e);
        expect(streams).toHaveLength(0);
        setVisible(true);
        expect(streams).toHaveLength(1);
        stop();
    });

    it('a stream the browser gave up on is opened anew on the return', () => {
        const {e, streams, setVisible, last} = env();
        const stop = watchAddons(() => undefined, e);
        last().readyState = 2; // the browser closed it (a 401)
        setVisible(true); // a visibilitychange to visible, e.g. after a lock screen
        expect(streams).toHaveLength(2);
        stop();
    });

    it('addonsChanged() reaches every watcher, and none after it stopped', () => {
        const {e} = env();
        let a = 0;
        let b = 0;
        const stopA = watchAddons(() => a++, e);
        const stopB = watchAddons(() => b++, {...e, open: null});
        addonsChanged();
        expect([a, b]).toEqual([1, 1]);
        stopA();
        addonsChanged();
        expect([a, b]).toEqual([1, 2]);
        stopB();
        addonsChanged();
        expect([a, b]).toEqual([1, 2]);
    });
});
