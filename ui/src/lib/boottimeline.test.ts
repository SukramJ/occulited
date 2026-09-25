import {describe, expect, it} from 'vitest';
import {chartSVG, compareUnits, deltaKind, deltaLabel, duration, exportName, filterUnits, followers, laterNames, logUnit, restartedAt, seconds, sortUnits, tableRowSelector, tickStep, ticks, timelineEnd, unitEnd, waitedFor, type BootTimeline, type BootUnit} from './boottimeline';

const units: BootUnit[] = [
    {id: 'usr-local.mount', activating: 3.55, active: 3.95, duration_ms: 391, state: 'active', sub: 'mounted'},
    {id: 'sysinit.target', activating: 5.678, active: 5.678, duration_ms: 0, state: 'active', after: ['usr-local.mount']},
    {id: 'rfd.service', description: 'BidCos-RF', activating: 20.1, active: 24.3, duration_ms: 4247, state: 'active', after: ['sysinit.target', 'multimacd.service']},
    {id: 'multimacd.service', activating: 20.25, active: 24.59, duration_ms: 4339, state: 'active', after: ['sysinit.target']},
    {id: 'occu-fstrim.service', activating: 30, inactive: 33.49, duration_ms: 3490, state: 'inactive'},
    {id: 'broken.service', activating: 31, inactive: 31.004, duration_ms: 4, state: 'failed'},
    {id: 'hmipserver.service', activating: 40, duration_ms: 15000, state: 'activating', after: ['rfd.service', 'multimacd.service']},
    {id: 'local-backup.timer', activating: 6, active: 6.001, duration_ms: 1, state: 'active'},
];
const tl: BootTimeline = {
    boot_id: '6c97f67681ee40c78e69d3bc0c03efea',
    started: '2026-09-12T19:06:11+02:00',
    finished: false,
    timestamps: {kernel: 0, userspace: 3, multi_user: undefined, finish: undefined},
    summary: {kernel_ms: 3000},
    units,
    critical_chain: ['multi-user.target', 'rfd.service', 'sysinit.target'],
    later: 0,
};

describe('the units shown', () => {
    const f = {q: '', hideShort: true, onlyServices: false, chain: false};
    it('hides what is under 10 ms, but not what fails or is still starting', () => {
        expect(filterUnits(tl, f).map((u) => u.id)).toEqual(['usr-local.mount', 'rfd.service', 'multimacd.service', 'occu-fstrim.service', 'broken.service', 'hmipserver.service']);
        expect(filterUnits(tl, {...f, hideShort: false})).toHaveLength(8);
    });
    it('keeps the chain whole while it is shown, and a search narrows everything', () => {
        expect(filterUnits(tl, {...f, chain: true}).map((u) => u.id)).toContain('sysinit.target');
        expect(filterUnits(tl, {...f, onlyServices: true, chain: true}).map((u) => u.id)).toEqual(['sysinit.target', 'rfd.service', 'multimacd.service', 'occu-fstrim.service', 'broken.service', 'hmipserver.service']);
        expect(filterUnits(tl, {...f, q: 'bidcos', chain: true}).map((u) => u.id)).toEqual(['rfd.service']);
        expect(filterUnits(tl, {...f, q: '  MULTI '}).map((u) => u.id)).toEqual(['multimacd.service']);
    });
    it('keeps occulited and its helper whatever "hide short units" says (B-175: their restart marks)', () => {
        const own: BootUnit[] = [
            {id: 'occulited.service', activating: 8, active: 8.004, duration_ms: 4, state: 'active'},
            {id: 'occulited-helper.service', activating: 7.9, active: 7.902, duration_ms: 2, state: 'active'},
        ];
        const t2: BootTimeline = {...tl, units: [...units, ...own]};
        const ids = filterUnits(t2, f).map((u) => u.id);
        expect(ids).toContain('occulited.service');
        expect(ids).toContain('occulited-helper.service');
        expect(ids).not.toContain('local-backup.timer');
        // the other filters still apply to them
        expect(filterUnits(t2, {...f, onlyServices: true}).map((u) => u.id)).toContain('occulited.service');
        expect(filterUnits(t2, {...f, q: 'rfd'}).map((u) => u.id)).toEqual(['rfd.service']);
    });
});

describe('bars and the axis', () => {
    it('ends a bar when it was up, ended, or now', () => {
        expect(unitEnd(units[2]!)).toBe(24.3);
        expect(unitEnd(units[4]!)).toBe(33.49);
        expect(unitEnd(units[6]!)).toBe(55);
        expect(timelineEnd(tl, units)).toBe(55);
        expect(timelineEnd({...tl, timestamps: {...tl.timestamps, finish: 114.7}}, units)).toBe(114.7);
        expect(timelineEnd({...tl, units: []}, [])).toBe(1);
    });
    it('picks a step whose ticks stand apart and counts the ticks', () => {
        expect(tickStep(10)).toBe(10);
        expect(tickStep(64)).toBe(1);
        expect(tickStep(1000)).toBe(0.1);
        expect(tickStep(0.01)).toBe(600);
        expect(ticks(1, 0.25)).toEqual([0, 0.25, 0.5, 0.75, 1]);
        expect(ticks(0.35, 0.1)).toEqual([0, 0.1, 0.2, 0.3]);
        expect(ticks(114.7, 30)).toEqual([0, 30, 60, 90]);
    });
});

describe('a unit and its neighbours', () => {
    const byId = new Map(units.map((u) => [u.id, u]));
    it('names what it waited for, the one up last first, and what came after it', () => {
        expect(waitedFor(units[6]!, byId).map((u) => u.id)).toEqual(['multimacd.service', 'rfd.service']);
        expect(waitedFor({...units[0]!, after: ['not-there.target']}, byId)).toEqual([]);
        expect(followers('multimacd.service', units).map((u) => u.id)).toEqual(['rfd.service', 'hmipserver.service']);
        expect(followers('sysinit.target', units).map((u) => u.id)).toEqual(['rfd.service', 'multimacd.service']);
    });
    it('sorts the list, ties in the order they began', () => {
        expect(sortUnits(units, 'duration', false).map((u) => u.id).slice(0, 3)).toEqual(['hmipserver.service', 'multimacd.service', 'rfd.service']);
        expect(sortUnits(units, 'unit', true)[0]!.id).toBe('broken.service');
        expect(sortUnits(units, 'state', true).map((u) => u.state).slice(0, 2)).toEqual(['activating', 'active']);
        expect(sortUnits(units, 'start', true).map((u) => u.id)).toEqual(units.map((u) => u.id).sort((a, b) => byId.get(a)!.activating - byId.get(b)!.activating));
    });
    it('finds its row in the Services tables and its name in the Log page', () => {
        expect(tableRowSelector('rfd.service')).toBe('tr[data-service="rfd"]');
        expect(tableRowSelector('addon-mosquitto.service')).toBe('tr[data-service="addon-mosquitto"]');
        expect(tableRowSelector('local-backup.timer')).toBe('tr:has([data-svc="local-backup.timer"])');
        expect(tableRowSelector('systemd-fsck@dev-disk-by\\x2dlabel-userfs.service')).toBe('tr[data-service="systemd-fsck@dev-disk-by\\\\x2dlabel-userfs"]');
        expect(tableRowSelector('sysinit.target')).toBe('');
        expect(logUnit('rfd.service')).toBe('rfd');
        expect(logUnit('usr-local.mount')).toBe('usr-local.mount');
    });
});

describe('numbers and files', () => {
    it('writes seconds and durations in the reader language', () => {
        expect(seconds(12.3456, 'en')).toBe('12.346 s');
        expect(seconds(106.0077, 'de')).toBe('106,008 s');
        expect(duration(296, 'en')).toBe('296 ms');
        expect(duration(4247, 'en')).toBe('4.25 s');
        expect(duration(42335, 'de')).toBe('42,3 s');
        expect(duration(67000, 'en')).toBe('1 min 7 s');
        expect(duration(120000, 'en')).toBe('2 min');
        expect(exportName(tl, 'svg')).toBe('boot-6c97f676-2026-09-12.svg');
        expect(exportName({...tl, started: undefined}, 'json')).toBe('boot-6c97f676.json');
    });
    it('draws a standalone SVG with its colours, the chain dimming the rest', () => {
        const colors = {bg: '#fff', fg: '#111', muted: '#777', grid: '#eee', activating: '#9cf', active: '#036', failed: '#c33', chain: '#f90'};
        const shown = filterUnits(tl, {q: '', hideShort: true, onlyServices: false, chain: true});
        const svg = chartSVG(tl, shown, {pxPerSecond: 10, nameWidth: 200, rowHeight: 20, chain: true, colors, title: 'Boot <6c97f676>'});
        expect(svg.startsWith('<svg xmlns="http://www.w3.org/2000/svg"')).toBe(true);
        expect(svg).toContain('<title>Boot &lt;6c97f676&gt;</title>');
        expect(svg.match(/<text x="4"/g)).toHaveLength(shown.length);
        expect(svg).toContain('fill="#c33"'); // the failed unit
        expect(svg).toContain('fill="#f90"'); // the chain
        expect(svg).toContain('opacity="0.3"'); // the rest dimmed
        expect(svg).not.toContain('var(--');
        expect(svg.trim().endsWith('</svg>')).toBe(true);
    });
});

describe('two boots compared', () => {
    const before: BootUnit[] = [
        {id: 'rfd.service', activating: 19.0, active: 22.0, duration_ms: 3000, state: 'active'},
        {id: 'hmipserver.service', activating: 30, active: 60, duration_ms: 30000, state: 'active'},
        {id: 'gone.service', activating: 5, active: 5.5, duration_ms: 500, state: 'active'},
    ];
    it('pairs the units by id and gives their deltas', () => {
        const d = compareUnits(units, before);
        expect(d.get('rfd.service')).toMatchObject({startMs: 1100, tookMs: 1247});
        expect(d.get('rfd.service')!.other.activating).toBe(19);
        expect(d.get('hmipserver.service')).toMatchObject({startMs: 10000, tookMs: -15000});
        expect(d.has('usr-local.mount')).toBe(false);
        expect(d.has('gone.service')).toBe(false);
    });
    it('writes a delta with its sign, quiet below 10 ms', () => {
        expect(deltaLabel(12300, 'en')).toBe('+12.3 s');
        expect(deltaLabel(-340, 'en')).toBe('−340 ms');
        expect(deltaLabel(4, 'en')).toBe('±0');
        expect(deltaLabel(-1500, 'de')).toBe('−1,50 s');
        expect(deltaLabel(-67000, 'en')).toBe('−1 min 7 s');
    });
    it('colours a second or ten per cent', () => {
        expect(deltaKind(1200, 50000)).toBe('worse');
        expect(deltaKind(-1200, 50000)).toBe('better');
        expect(deltaKind(300, 2000)).toBe('worse');
        expect(deltaKind(50, 200)).toBe('');
        expect(deltaKind(400, 10000)).toBe('');
    });
});

describe('the running boot after restarts (B-153)', () => {
    const run: BootTimeline = {...tl, started: '2026-09-18T14:43:00Z', later: 10, later_units: ['a.service', 'b.service', 'c.service', 'd.service', 'e.service', 'f.service', 'g.service', 'h.service', 'i.service', 'j.service']};
    it('a restart on the wall clock, from the boot\'s start', () => {
        const u: BootUnit = {id: 'rfd.service', activating: 33.2, duration_ms: 2500, state: 'active', restarted: 1308};
        expect(restartedAt(run, u)?.toISOString()).toBe('2026-09-18T15:04:48.000Z');
        expect(restartedAt(run, {...u, restarted: undefined})).toBeNull();
        expect(restartedAt({...run, started: undefined}, u)).toBeNull();
    });
    it('names at most eight units started after the boot, and how many more', () => {
        expect(laterNames(run)).toEqual({names: run.later_units!.slice(0, 8), more: 2});
        expect(laterNames({...run, later_units: ['rfd.service']})).toEqual({names: ['rfd.service'], more: 0});
        expect(laterNames({...run, later_units: undefined})).toEqual({names: [], more: 0});
    });
});
