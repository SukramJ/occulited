import {describe, expect, it} from 'vitest';
import {ENABLED_RANK, STATUS_RANK, enabledKind, span, statusDot, statusKind, switchable, unknownState, type StatusKind, type UnitState} from './units';

// B-65: red for a failure only; a condition that kept a unit off says so; the rest is stopped
describe('the Status column', () => {
    it.each<[string, UnitState, StatusKind, string]>([
        ['a running daemon', {running: true}, 'running', 'ok'],
        ['an addon outside its unit', {running: true, oneshot: false}, 'running', 'ok'],
        ['a one-shot that ended well', {running: false, oneshot: true, result: 'success'}, 'completed', 'once'],
        ['a one-shot that failed', {running: false, oneshot: true, result: 'exit-code', failed: true}, 'failed', 'once failed'],
        ['a one-shot with a bad result only', {running: false, oneshot: true, result: 'timeout'}, 'failed', 'once failed'],
        ['a failed daemon', {running: false, failed: true, result: 'exit-code'}, 'failed', 'err'],
        ['hmlangw outside LAN gateway mode', {running: false, skipped: true}, 'skipped', 'skipped'],
        ['hs485d without a wired interface', {running: false, skipped: true, result: 'exec-condition'}, 'skipped', 'skipped'],
        // the old rule made these red: static, started by a timer, boot-only, stopped by hand
        ['a static unit', {running: false}, 'stopped', ''],
        ['a failed unit whose later start was skipped', {running: false, failed: true, skipped: true}, 'failed', 'err'],
        // task 94: hmipserver's JVM at boot - neither running nor stopped nor failed
        ['a unit systemd is starting', {running: false, starting: true}, 'starting', 'starting'],
        ['an addon whose start script still runs', {running: false, starting: true, oneshot: false}, 'starting', 'starting'],
        // B-158: an addon that keeps a daemon, its unit empty - red, not Completed
        ['an addon whose daemon ended', {running: false, oneshot: true, result: 'success', ended: true}, 'ended', 'err'],
    ])('%s', (_, s, kind, dot) => {
        expect(statusKind(s)).toBe(kind);
        expect(statusDot(s)).toBe(dot);
    });
    it('sorts running first and stopped last', () => {
        const order: StatusKind[] = ['running', 'starting', 'completed', 'ended', 'failed', 'skipped', 'stopped'];
        expect([...order].sort((a, b) => STATUS_RANK[a] - STATUS_RANK[b])).toEqual(order);
    });
});

// the Uptime column and a timer's countdown write their spans alike
describe('a span of seconds', () => {
    it.each([
        [0, '0 s'],
        [59.9, '59 s'],
        [60, '1 min'],
        [3599, '59 min'],
        [7381, '2 h 3 min'],
        [86400 * 7 + 3600, '7 d 1 h'],
        [-5, '0 s'],
    ])('writes %s seconds as %s', (s, text) => {
        expect(span(s)).toBe(text);
    });
});

// task 49: the Enabled column's mapping of systemd's UnitFileState (the table in the task)
describe('the Enabled column', () => {
    it.each([
        ['enabled', 'yes'],
        ['enabled-runtime', 'yes'],
        ['alias', 'yes'],
        ['indirect', 'yes'],
        ['generated', 'addon'],
        ['static', 'static'],
        ['disabled', 'no'],
        ['masked', 'off'],
        ['masked-runtime', 'off'],
    ])('%s is %s', (state, kind) => {
        // the verdict passed along contradicts the state on purpose: a known state decides
        expect(enabledKind(state, kind === 'no' || kind === 'off')).toBe(kind);
    });
    it('falls back to the verdict without a state (busybox) or with one it does not know', () => {
        expect(enabledKind(undefined, true)).toBe('yes');
        expect(enabledKind('', false)).toBe('no');
        expect(enabledKind('linked', false)).toBe('no');
        expect(unknownState('linked')).toBe('linked');
        expect(unknownState('generated')).toBe('');
        expect(unknownState(undefined)).toBe('');
    });
    it('sorts on before off, and offers no switch on a static unit', () => {
        expect(ENABLED_RANK.yes).toBeLessThan(ENABLED_RANK.addon);
        expect(ENABLED_RANK.addon).toBeLessThan(ENABLED_RANK.static);
        expect(ENABLED_RANK.static).toBeLessThan(ENABLED_RANK.no);
        expect(ENABLED_RANK.no).toBeLessThan(ENABLED_RANK.off);
        expect(switchable('static')).toBe(false);
        expect(switchable('generated')).toBe(true);
        expect(switchable('masked-runtime')).toBe(true);
        expect(switchable(undefined)).toBe(true);
    });
});
