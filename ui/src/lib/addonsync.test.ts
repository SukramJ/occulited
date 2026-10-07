import {describe, expect, it} from 'vitest';
import {addonsChanged, watchAddons, type Subscribe} from './addonsync';

// openccu-lite B-297: the shell reads its menu again when the addons changed; the revision comes
// from the topic addons of the shell's stream (occulited B-53, shellstream/)

function feed() {
    const subs = new Map<string, Set<(data: string) => void>>();
    const subscribe: Subscribe = (topic, f) => {
        if (!subs.has(topic)) subs.set(topic, new Set());
        subs.get(topic)!.add(f);
        return () => subs.get(topic)!.delete(f);
    };
    const send = (revision: unknown) => {
        for (const f of subs.get('addons') ?? []) f(JSON.stringify({revision}));
    };
    return {subscribe, send, count: () => subs.get('addons')?.size ?? 0, raw: (d: string) => subs.get('addons')?.forEach((f) => f(d))};
}

describe('watchAddons', () => {
    it('reads again when the revision moves, not for the first one or the same one', () => {
        const fd = feed();
        let n = 0;
        const stop = watchAddons(() => n++, fd.subscribe);
        expect(fd.count()).toBe(1);
        fd.send(100);
        expect(n).toBe(0);
        fd.send(100);
        expect(n).toBe(0);
        fd.send(101);
        expect(n).toBe(1);
        // a restarted service starts from its clock: another number, read again
        fd.send(5);
        expect(n).toBe(2);
        // what is no revision is ignored
        fd.send('x');
        fd.raw('{');
        expect(n).toBe(2);
        stop();
        expect(fd.count()).toBe(0);
    });

    it('a window back from hidden: the replayed revision reads again only when it moved', () => {
        const fd = feed();
        let n = 0;
        const stop = watchAddons(() => n++, fd.subscribe);
        fd.send(7);
        fd.send(7); // the hub's replay on the return: nothing moved
        expect(n).toBe(0);
        fd.send(9); // moved while hidden
        expect(n).toBe(1);
        stop();
    });

    it('addonsChanged() reaches every watcher, and none after it stopped', () => {
        const fd = feed();
        let a = 0;
        let b = 0;
        const stopA = watchAddons(() => a++, fd.subscribe);
        const stopB = watchAddons(() => b++, fd.subscribe);
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
