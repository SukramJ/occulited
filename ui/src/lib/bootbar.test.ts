import {describe, expect, it} from 'vitest';
import table from '../../../internal/bootexpect/defaults.json';
import {
    backIn,
    barView,
    bootText,
    classifyHealth,
    defaultExpect,
    EASE_MS,
    ENTRY_KEY,
    estimatedEnd,
    fillAt,
    HINT_AFTER_MS,
    isBack,
    markSeen,
    newEntry,
    observe,
    parseEntry,
    pickLanguage,
    productOf,
    readyDone,
    readyView,
    remainingMs,
    STALE_MS,
    startingInterfaces,
    timingBody,
    type BootEntry,
} from './bootbar';

const T0 = 1_800_000_000_000;
// an easy reboot to count with: 10 s shutdown, 20 s boot to lighttpd, 10 s occulited, 50 s interfaces
const EXPECT = {down: 10, http: 20, ui: 10, ready: 50};
const reboot = (): BootEntry => newEntry('reboot', T0, EXPECT);
const at = (s: number) => T0 + s * 1000;

/** The fill sampled every 50 ms from `from` to `to` seconds. */
function samples(e: BootEntry, from: number, to: number): number[] {
    const out: number[] = [];
    for (let t = at(from); t <= at(to); t += 50) out.push(fillAt(e, t));
    return out;
}

function expectMonotone(v: number[]) {
    for (let i = 1; i < v.length; i++) expect(v[i]!).toBeLessThanOrEqual(v[i - 1]! + 1e-12);
}

describe('the product defaults', () => {
    it('come from the one table the daemon embeds', () => {
        expect(defaultExpect('rpi4', 'reboot')).toEqual(table.products.rpi4.reboot);
        expect(defaultExpect('RPI3', 'update')).toEqual(table.products.rpi3.update);
        expect(defaultExpect('ova', 'restore')).toEqual(table.products.ova.restore);
    });

    it('an unknown product is the default one; recovery and halt shut down like a reboot', () => {
        expect(productOf('ccu3')).toBe(table.default);
        expect(productOf(undefined)).toBe(table.default);
        expect(defaultExpect('', 'reboot')).toEqual(defaultExpect(table.default, 'reboot'));
        expect(defaultExpect('ova', 'recovery')).toEqual(defaultExpect('ova', 'reboot'));
        expect(defaultExpect('ova', 'halt')).toEqual(defaultExpect('ova', 'reboot'));
    });

    it('an update takes longer than a reboot', () => {
        for (const p of Object.keys(table.products)) expect(defaultExpect(p, 'update').http).toBeGreaterThan(defaultExpect(p, 'reboot').http);
    });
});

describe('the entry', () => {
    it('is kept under occulite.reboot and read back as written', () => {
        expect(ENTRY_KEY).toBe('occulite.reboot');
        const e = markSeen(reboot(), 'down', at(9));
        expect(parseEntry(JSON.stringify(e), at(10))).toEqual(e);
    });

    it.each<[string, unknown]>([
        ['not JSON', '{'],
        ['another version', {...reboot(), v: 2}],
        ['an unknown kind', {...reboot(), kind: 'sideways'}],
        ['no start', {...reboot(), started: 0}],
        ['a phase missing', {...reboot(), expect: {down: 1, http: 2, ui: 3}}],
        ['a negative phase', {...reboot(), expect: {...EXPECT, http: -1}}],
        ['checkpoints out of order', {...reboot(), seen: {down: at(20), http: at(10)}}],
        ['a checkpoint before the start', {...reboot(), seen: {down: T0 - 1}}],
    ])('is refused: %s', (_what, value) => {
        expect(parseEntry(typeof value === 'string' ? value : JSON.stringify(value), at(1))).toBeNull();
    });

    it('is stale after half an hour, and one from the future is not believed', () => {
        const raw = JSON.stringify(reboot());
        expect(parseEntry(raw, T0 + STALE_MS)).not.toBeNull();
        expect(parseEntry(raw, T0 + STALE_MS + 1)).toBeNull();
        expect(parseEntry(raw, T0 - 2 * 60_000)).toBeNull();
        expect(parseEntry(null, T0)).toBeNull();
    });

    it('keeps the first sighting of a checkpoint, and no earlier phase after a later one', () => {
        let e = markSeen(reboot(), 'down', at(8));
        expect(markSeen(e, 'down', at(12)).seen.down).toBe(at(8));
        e = markSeen(e, 'ui', at(35));
        // lighttpd answering after occulited did is no boot phase
        expect(markSeen(e, 'http', at(40)).seen.http).toBeUndefined();
        // a clock that stepped back does not put http before down
        expect(markSeen(markSeen(reboot(), 'down', at(8)), 'http', at(3)).seen.http).toBe(at(8));
    });
});

describe('the remaining time', () => {
    it('is the rest of the current phase and the phases after it', () => {
        const e = reboot();
        expect(estimatedEnd(e)).toBe(at(40));
        expect(remainingMs(e, at(5))).toBe(35_000);
        expect(remainingMs(e, at(50))).toBe(0);
    });

    it('a checkpoint replaces the finished phases with what they took', () => {
        // the shutdown took 4 s instead of 10: the end moves 6 s earlier
        let e = markSeen(reboot(), 'down', at(4));
        expect(estimatedEnd(e)).toBe(at(34));
        // lighttpd only after 40 s: 10 s of occulited left from there
        e = markSeen(e, 'http', at(40));
        expect(estimatedEnd(e)).toBe(at(50));
        expect(remainingMs(e, at(41))).toBe(9_000);
        // occulited answered
        e = markSeen(e, 'ui', at(43));
        expect(remainingMs(e, at(43))).toBe(0);
    });

    it('a checkpoint that was not seen is skipped: lighttpd straight after the start', () => {
        const e = markSeen(reboot(), 'http', at(25));
        expect(estimatedEnd(e)).toBe(at(35));
    });
});

describe('the fill', () => {
    it('starts full and empties evenly to the estimate', () => {
        const e = reboot();
        expect(fillAt(e, T0)).toBe(1);
        expect(fillAt(e, at(10))).toBeCloseTo(0.75);
        expect(fillAt(e, at(30))).toBeCloseTo(0.25);
        expect(fillAt(e, at(40))).toBe(0);
        expectMonotone(samples(e, 0, 45));
    });

    it('an early checkpoint runs it down to the new estimate within half a second', () => {
        // lighttpd after 12 s instead of 30: 10 s of 22 are left
        const e = markSeen(markSeen(reboot(), 'down', at(6)), 'http', at(12));
        const before = fillAt(e, at(12));
        expect(before).toBeGreaterThan(0.6);
        const after = fillAt(e, at(12) + EASE_MS);
        expect(after).toBeCloseTo((at(22) - (at(12) + EASE_MS)) / (at(22) - T0));
        // and from there on the schedule of the new estimate
        expect(fillAt(e, at(17))).toBeCloseTo(5 / 22);
        expect(fillAt(e, at(22))).toBe(0);
        expectMonotone(samples(e, 0, 25));
    });

    it('occulited answering early runs it out completely within half a second', () => {
        const e = markSeen(reboot(), 'ui', at(20));
        expect(fillAt(e, at(20))).toBeCloseTo(0.5);
        expect(fillAt(e, at(20) + EASE_MS / 2)).toBeLessThan(0.5);
        expect(fillAt(e, at(20) + EASE_MS)).toBe(0);
        expectMonotone(samples(e, 0, 22));
    });

    it('a late checkpoint slows it so it still ends at the new estimate, never refilling', () => {
        // the shutdown took 25 s instead of 10: the bar stands at 37.5 % and has 30 s left
        const e = markSeen(reboot(), 'down', at(25));
        const f = fillAt(e, at(25));
        expect(f).toBeCloseTo(15 / 40);
        expect(fillAt(e, at(40))).toBeCloseTo(f / 2);
        expect(fillAt(e, at(55))).toBe(0);
        expectMonotone(samples(e, 0, 60));
    });

    it('stays empty when a checkpoint comes after it ran out', () => {
        const e = markSeen(reboot(), 'http', at(70));
        expect(fillAt(e, at(71))).toBe(0);
        expectMonotone(samples(e, 0, 90));
    });

    it('is the same whichever page draws it: only the entry and the time count', () => {
        const e = markSeen(markSeen(reboot(), 'down', at(7)), 'http', at(33));
        const copy = parseEntry(JSON.stringify(e), at(34))!;
        for (const s of [0, 7, 20, 33, 33.2, 34, 40, 43]) expect(fillAt(copy, at(s))).toBe(fillAt(e, at(s)));
    });

    it('never rises over a whole boot of checkpoints early and late', () => {
        for (const [down, http, ui] of [
            [3, 15, 16],
            [15, 45, 46],
            [9, 31, 60],
            [2, 5, 50],
            [0, 0, 0.1],
        ]) {
            const e = markSeen(markSeen(markSeen(reboot(), 'down', at(down!)), 'http', at(http!)), 'ui', at(ui!));
            expectMonotone(samples(e, 0, 70));
        }
    });
});

describe('what the bar says', () => {
    it('the time left, in seconds and in minutes for an install', () => {
        const v = barView(reboot(), at(15), 'en');
        expect(v).toMatchObject({indeterminate: false, done: false, remainingS: 25, percent: 38, text: 'Back in about 25 s', hint: ''});
        expect(barView(reboot(), at(15), 'de').text).toBe('In etwa 25 s wieder da');
        expect(backIn(0.2, 'en')).toBe('Back in about 1 s');
        expect(backIn(450, 'en')).toBe('Back in about 8 min');
        expect(backIn(450, 'de')).toBe('In etwa 8 min wieder da');
    });

    it('empty and nothing answering: the indeterminate state, taking longer than usual', () => {
        const v = barView(reboot(), at(41), 'en');
        expect(v).toMatchObject({fill: 0, indeterminate: true, percent: null, text: 'Taking longer than usual'});
        expect(barView(reboot(), at(41), 'de').text).toBe('Dauert länger als üblich');
    });

    it('a checkpoint after the bar ran out gives a time again, with the stripe', () => {
        const v = barView(markSeen(reboot(), 'http', at(60)), at(62), 'en');
        expect(v).toMatchObject({indeterminate: true, text: 'Back in about 8 s'});
    });

    it('three minutes past the estimate: the recovery hint, by address', () => {
        expect(barView(reboot(), at(40) + HINT_AFTER_MS - 1, 'en', 'http://192.0.2.7/').hint).toBe('');
        const v = barView(reboot(), at(40) + HINT_AFTER_MS, 'en', 'http://192.0.2.7/');
        expect(v.hint).toContain('http://192.0.2.7/');
        expect(v.hint).toContain('recovery system');
        expect(barView(reboot(), at(40) + HINT_AFTER_MS, 'de', 'http://192.0.2.7/').hint).toContain('Recovery-System');
        expect(barView(reboot(), at(40) + HINT_AFTER_MS, 'en').hint).toContain('IP address');
    });

    it('occulited answered: done, no hint', () => {
        const v = barView(markSeen(reboot(), 'ui', at(400)), at(400), 'en', 'http://192.0.2.7/');
        expect(v).toMatchObject({done: true, indeterminate: false, hint: '', text: 'The system is back'});
    });

    it('the interfaces: a thinner bar over the ready phase from occulited\'s answer', () => {
        const e = markSeen(reboot(), 'ui', at(40));
        expect(readyView(e, at(40), 'en')).toMatchObject({fill: 1, indeterminate: false, remainingS: 50, title: 'The system has restarted'});
        expect(readyView(e, at(65), 'de')).toMatchObject({fill: 0.5, percent: 50, remainingS: 25, title: 'Das System wurde neu gestartet'});
        expect(readyView(e, at(91), 'en')).toMatchObject({indeterminate: true, remainingS: 0, expired: false});
        expect(readyView(e, at(90) + 5 * 60_000 + 1, 'en').expired).toBe(true);
    });

    it('B-104: the sentence names what still starts and about how long it takes', () => {
        const e = markSeen(reboot(), 'ui', at(40));
        const both = ['HmIP-RF', 'BidCos-RF'];
        expect(readyView(e, at(45), 'en', both).text).toBe('The radio interfaces are still starting – about 45 s until HmIP-RF and BidCos-RF are ready.');
        expect(readyView(e, at(45), 'de', both).text).toBe('Die Funkschnittstellen starten noch – etwa 45 s, bis HmIP-RF und BidCos-RF bereit sind.');
        expect(readyView(e, at(45), 'en', ['BidCos-RF']).text).toBe('The radio interfaces are still starting – about 45 s until BidCos-RF is ready.');
        expect(readyView(e, at(45), 'de', ['HmIP-RF']).text).toBe('Die Funkschnittstellen starten noch – etwa 45 s, bis HmIP-RF bereit ist.');
        // before the services list answered
        expect(readyView(e, at(45), 'en').text).toBe('The radio interfaces are still starting – about 45 s.');
        expect(readyView(e, at(45), 'de', null).text).toBe('Die Funkschnittstellen starten noch – etwa 45 s.');
        // a part of a second left still counts as one
        expect(readyView(e, at(89.4), 'en', both).text).toContain('about 1 s');
        // past the estimate
        expect(readyView(e, at(95), 'en', both).text).toBe('The radio interfaces are still starting – HmIP-RF and BidCos-RF take longer than usual.');
        expect(readyView(e, at(95), 'de', ['BidCos-RF']).text).toBe('Die Funkschnittstellen starten noch – BidCos-RF braucht länger als üblich.');
        expect(readyView(e, at(95), 'en', []).text).toBe('The radio interfaces are still starting – it takes longer than usual.');
    });

    it('every text in both languages', () => {
        for (const k of ['starting', 'late', 'ready', 'shuttingDown', 'haltWait', 'barLabel', 'readyLabel', 'readyTitle', 'readyIn', 'readyInOne', 'readyInAny', 'readyLate', 'readyLateOne', 'readyLateAny', 'and'] as const) {
            expect(bootText(k, 'de')).not.toBe(bootText(k, 'en'));
        }
        expect(bootText('recovery', 'en', {url: 'http://192.0.2.7/'})).toBe('The recovery system starts; it is at http://192.0.2.7/');
        expect(bootText('starting', 'en')).toBe('openccu-lite is starting …');
        expect(bootText('starting', 'de')).toBe('openccu-lite startet …');
    });

    it('the language: the shell\'s choice, then the browser\'s list, English otherwise', () => {
        expect(pickLanguage('de', ['en-US'])).toBe('de');
        expect(pickLanguage(null, ['fr-FR', 'de-AT', 'en'])).toBe('de');
        expect(pickLanguage(null, ['en-GB', 'de'])).toBe('en');
        expect(pickLanguage('xx', ['fr'])).toBe('en');
        expect(pickLanguage(undefined, undefined)).toBe('en');
    });
});

describe('the checkpoints from the health route', () => {
    it.each<[string, number | null, unknown, string]>([
        ['no answer', null, null, 'down'],
        ["lighttpd's starting page", 503, {error: 'starting'}, 'http'],
        ['a bare 502', 502, null, 'http'],
        ['a 504', 504, null, 'http'],
        ["occulited's answer", 200, {ok: true, uptime_s: 3}, 'ui'],
        ['a 200 that is not occulited', 200, null, 'other'],
        ["the recovery system's 404", 404, null, 'other'],
        ['a login wall', 401, {error: 'unauthenticated'}, 'other'],
    ])('%s', (_what, status, body, want) => {
        expect(classifyHealth(status, body)).toBe(want);
    });

    it('back is a smaller uptime, an answer after a gap, or an occulited younger than the request', () => {
        const e = reboot();
        expect(isBack(e, 12, 1000, at(30))).toBe(true);
        expect(isBack(e, 1001, 1000, at(30))).toBe(false);
        expect(isBack(markSeen(e, 'down', at(5)), 1001, 1000, at(30))).toBe(true);
        expect(isBack(markSeen(e, 'http', at(5)), 5000, -1, at(30))).toBe(true);
        // a page reloaded mid-boot does not know the uptime before
        expect(isBack(e, 20, -1, at(30))).toBe(true);
        expect(isBack(e, 29, -1, at(30))).toBe(false);
    });

    it("lighttpd's 503 before the box went down is the shutdown, not the boot", () => {
        const e = reboot();
        expect(observe(e, 'http', null, 4990, at(1)).entry.seen).toEqual({});
        const gone = observe(e, 'down', null, 4990, at(6)).entry;
        expect(observe(gone, 'http', null, 4990, at(30)).entry.seen).toEqual({down: at(6), http: at(30)});
    });

    it('a poll records its checkpoint and says when the box is back', () => {
        let e = reboot();
        let r = observe(e, 'ui', 5000, 4990, at(2));
        expect(r).toEqual({entry: e, back: false});
        r = observe(e, 'down', null, 4990, at(9));
        expect(r.entry.seen.down).toBe(at(9));
        e = r.entry;
        r = observe(e, 'http', null, 4990, at(30));
        expect(r.back).toBe(false);
        r = observe(r.entry, 'other', null, 4990, at(31));
        expect(r.back).toBe(false);
        r = observe(r.entry, 'ui', 1, 4990, at(33));
        expect(r.back).toBe(true);
        expect(r.entry.seen).toEqual({down: at(9), http: at(30), ui: at(33)});
    });
});

describe('the interfaces and the timing report', () => {
    it.each<[string, {id: string; running?: boolean; enabled?: boolean; skipped?: boolean; failed?: boolean}[], boolean]>([
        ['both running', [{id: 'rfd', running: true}, {id: 'hmipserver', running: true}], true],
        ['hmipserver still starting', [{id: 'rfd', running: true}, {id: 'hmipserver', running: false, enabled: true}], false],
        ['no hmipserver on this system', [{id: 'rfd', running: true}], true],
        ['rfd switched off', [{id: 'rfd', running: false, enabled: false}, {id: 'hmipserver', running: true}], true],
        ['its condition unmet', [{id: 'rfd', running: false, enabled: true, skipped: true}, {id: 'hmipserver', running: true}], true],
        ['failed', [{id: 'rfd', running: true}, {id: 'hmipserver', running: false, enabled: true, failed: true}], true],
        ['an empty list', [], true],
    ])('%s', (_what, list, want) => {
        expect(readyDone(list)).toBe(want);
    });

    it('B-104: the interfaces still starting, by name, HmIP-RF first', () => {
        expect(startingInterfaces([{id: 'rfd', running: false, enabled: true}, {id: 'hmipserver', running: false, enabled: true}])).toEqual(['HmIP-RF', 'BidCos-RF']);
        expect(startingInterfaces([{id: 'rfd', running: false, enabled: true}, {id: 'hmipserver', running: true}])).toEqual(['BidCos-RF']);
        expect(startingInterfaces([{id: 'rfd', running: true}, {id: 'hmipserver', running: false, enabled: true}])).toEqual(['HmIP-RF']);
        // up, not there, switched off, its condition unmet or failed: nothing to wait for
        expect(startingInterfaces([{id: 'rfd', running: false, enabled: false}, {id: 'hmipserver', running: false, enabled: true, failed: true}])).toEqual([]);
        expect(startingInterfaces([{id: 'rfd', running: false, enabled: true, skipped: true}])).toEqual([]);
        expect(startingInterfaces([])).toEqual([]);
    });

    it('the report carries the start, the checkpoints seen and the ready time', () => {
        const e = markSeen(markSeen(reboot(), 'down', at(9)), 'ui', at(40));
        expect(timingBody(e, at(80))).toEqual({kind: 'reboot', started: T0, down: at(9), ui: at(40), ready: at(80)});
        expect(timingBody(e)).toEqual({kind: 'reboot', started: T0, down: at(9), ui: at(40)});
    });
});
