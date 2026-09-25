// Task 93: the boot timeline on the Services page - GET /boot's answer and what the chart and the
// list make of it: which units are shown, where a bar ends, the axis at a zoom, what a unit waited
// for, where its row stands in the Services tables above, and the chart as a standalone SVG file.
// Pure, so vitest runs it.

/** One unit's start, its times in seconds after the kernel started. */
export interface BootUnit {
    id: string;
    description?: string;
    activating: number;
    active?: number;
    inactive?: number;
    duration_ms: number;
    state: string;
    sub?: string;
    after?: string[];
    /** B-153: began to start again after the boot, in seconds after the kernel started; state and sub are of now */
    restarted?: number;
}

/** GET /boot. */
export interface BootTimeline {
    boot_id: string;
    started?: string;
    finished: boolean;
    timestamps: {kernel: number; initrd?: number; userspace: number; finish?: number; multi_user?: number};
    summary: {kernel_ms: number; initrd_ms?: number; userspace_ms?: number; total_ms?: number; multi_user_ms?: number; slowest?: {id: string; duration_ms: number}};
    units: BootUnit[];
    critical_chain: string[];
    later: number;
    /** B-153: the units that started after the boot, by name */
    later_units?: string[];
    /** an earlier boot, kept when it had finished */
    snapshot?: boolean;
    /** the boot kept before this one */
    previous?: {boot_id: string; started?: string; recorded_ms: number; total_ms?: number; userspace_ms?: number};
}

export interface UnitFilter {
    q: string;
    hideShort: boolean;
    onlyServices: boolean;
    chain: boolean;
}

/** Below this a unit is noise in the chart: a target, a device, a slice. */
export const SHORT_MS = 10;

/**
 * The units the chart and the list show, in the order they began. A typed search narrows
 * everything; otherwise the critical chain's units stay while the chain is shown, short or not,
 * so the highlighted chain has no gaps, and a unit still starting or failed is never short.
 */
/**
 * occulited and its helper are never hidden as short: their restart marks (B-153) are what the
 * list shows about a restart of the daemon, and both start in a few milliseconds (openccu-lite B-175).
 */
export const ALWAYS_SHOWN = new Set(['occulited.service', 'occulited-helper.service']);

export function filterUnits(t: BootTimeline, f: UnitFilter): BootUnit[] {
    const needle = f.q.trim().toLowerCase();
    const chain = new Set(f.chain ? t.critical_chain : []);
    return t.units.filter((u) => {
        if (needle && !u.id.toLowerCase().includes(needle) && !(u.description ?? '').toLowerCase().includes(needle)) return false;
        if (chain.has(u.id)) return true;
        if (f.hideShort && u.duration_ms < SHORT_MS && u.state !== 'activating' && u.state !== 'failed' && !ALWAYS_SHOWN.has(u.id)) return false;
        if (f.onlyServices && !u.id.endsWith('.service')) return false;
        return true;
    });
}

/** Where a unit's bar ends: up, else ended, else its time so far. */
export function unitEnd(u: BootUnit): number {
    if (u.active !== undefined && u.active >= u.activating) return u.active;
    if (u.inactive !== undefined && u.inactive >= u.activating) return u.inactive;
    return u.activating + u.duration_ms / 1000;
}

/** The seconds the chart spans: to the boot's finish or the last bar's end, whichever is later. */
export function timelineEnd(t: BootTimeline, units: readonly BootUnit[]): number {
    let end = t.timestamps.finish ?? 0;
    for (const u of units) end = Math.max(end, unitEnd(u));
    return end > 0 ? end : 1;
}

const STEPS = [0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600];

/** The axis step for a scale: the smallest of the usual steps whose ticks stand `minPx` apart. */
export function tickStep(pxPerSecond: number, minPx = 64): number {
    for (const s of STEPS) if (s * pxPerSecond >= minPx) return s;
    return STEPS[STEPS.length - 1]!;
}

/** The ticks from 0 to `end`, counted rather than summed so a tenth never drifts. */
export function ticks(end: number, step: number): number[] {
    const out: number[] = [];
    for (let i = 0; i * step <= end + 1e-9; i++) out.push(Math.round(i * step * 1000) / 1000);
    return out;
}

/** The units a unit is ordered after that are in the timeline, the one up last first. */
export function waitedFor(u: BootUnit, byId: ReadonlyMap<string, BootUnit>): BootUnit[] {
    return (u.after ?? [])
        .map((id) => byId.get(id))
        .filter((x): x is BootUnit => !!x)
        .sort((a, b) => unitEnd(b) - unitEnd(a) || a.id.localeCompare(b.id));
}

/** The units ordered after a unit, in the order they began. */
export function followers(id: string, units: readonly BootUnit[]): BootUnit[] {
    return units.filter((u) => (u.after ?? []).includes(id)).sort((a, b) => a.activating - b.activating || a.id.localeCompare(b.id));
}

export type ListKey = 'start' | 'duration' | 'unit' | 'state';

/** The list's order; ties keep the order the units began. */
export function sortUnits(units: readonly BootUnit[], key: ListKey, asc: boolean): BootUnit[] {
    const dir = asc ? 1 : -1;
    const by: Record<ListKey, (a: BootUnit, b: BootUnit) => number> = {
        start: (a, b) => a.activating - b.activating,
        duration: (a, b) => a.duration_ms - b.duration_ms,
        unit: (a, b) => a.id.localeCompare(b.id),
        state: (a, b) => a.state.localeCompare(b.state),
    };
    return units
        .map((u, i) => ({u, i}))
        .sort((x, y) => by[key](x.u, y.u) * dir || x.i - y.i)
        .map((x) => x.u);
}

function attr(v: string): string {
    return v.replaceAll('\\', '\\\\').replaceAll('"', '\\"');
}

/**
 * Where a unit stands in the Services page's tables above: a service's row carries
 * `data-service` - its id without `.service` - and a timer's row holds its menu, `data-svc` with
 * the timer's unit. '' for a unit the tables do not list (a target, a mount, a socket, a device).
 */
export function tableRowSelector(unit: string): string {
    if (unit.endsWith('.service')) return `tr[data-service="${attr(unit.slice(0, -'.service'.length))}"]`;
    if (unit.endsWith('.timer')) return `tr:has([data-svc="${attr(unit)}"])`;
    return '';
}

/** The Log page's unit for a unit of the timeline: a service without `.service`, anything else whole. */
export function logUnit(unit: string): string {
    return unit.endsWith('.service') ? unit.slice(0, -'.service'.length) : unit;
}

/** Seconds after the kernel started, to the millisecond: `12.345 s`. */
export function seconds(s: number, lang: string): string {
    return `${new Intl.NumberFormat(lang === 'de' ? 'de-DE' : 'en-GB', {minimumFractionDigits: 3, maximumFractionDigits: 3}).format(s)} s`;
}

/** A unit's duration: `345 ms`, `4.21 s`, `42.3 s`, `1 min 7 s`. */
export function duration(ms: number, lang: string): string {
    const nf = (digits: number) => new Intl.NumberFormat(lang === 'de' ? 'de-DE' : 'en-GB', {minimumFractionDigits: digits, maximumFractionDigits: digits});
    if (ms < 1000) return `${Math.round(ms)} ms`;
    if (ms < 10_000) return `${nf(2).format(ms / 1000)} s`;
    if (ms < 60_000) return `${nf(1).format(ms / 1000)} s`;
    const total = Math.round(ms / 1000);
    const m = Math.floor(total / 60);
    const s = total % 60;
    return s ? `${m} min ${s} s` : `${m} min`;
}

/** The file name of an export: `boot-<the id's first eight digits>[-<the day it began>].<ext>`. */
export function exportName(t: BootTimeline, ext: 'svg' | 'json'): string {
    const day = t.started ? t.started.slice(0, 10) : '';
    return `boot-${t.boot_id.slice(0, 8)}${day ? `-${day}` : ''}.${ext}`;
}

export interface ChartColors {
    bg: string;
    fg: string;
    muted: string;
    grid: string;
    activating: string;
    active: string;
    failed: string;
    chain: string;
}

function xml(s: string): string {
    return s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
}

/**
 * The chart as a file of its own: the names, the axis, the milestones and the bars with their
 * colours written in, since the page's CSS variables do not travel with it.
 */
export function chartSVG(
    t: BootTimeline,
    units: readonly BootUnit[],
    o: {pxPerSecond: number; nameWidth: number; rowHeight: number; chain: boolean; colors: ChartColors; title: string},
): string {
    const c = o.colors;
    const end = timelineEnd(t, units);
    const axis = 24;
    const plot = Math.ceil(end * o.pxPerSecond) + 12;
    const width = o.nameWidth + plot;
    const height = axis + units.length * o.rowHeight + 6;
    const x = (s: number) => o.nameWidth + s * o.pxPerSecond;
    const chain = new Set(o.chain ? t.critical_chain : []);
    const step = tickStep(o.pxPerSecond);
    const parts: string[] = [];
    parts.push(`<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" font-family="system-ui, sans-serif" font-size="11">`);
    parts.push(`<title>${xml(o.title)}</title>`);
    parts.push(`<rect width="${width}" height="${height}" fill="${c.bg}"/>`);
    for (const s of ticks(end, step)) {
        parts.push(`<line x1="${x(s)}" y1="${axis - 4}" x2="${x(s)}" y2="${height}" stroke="${c.grid}" stroke-width="1"/>`);
        parts.push(`<text x="${x(s) + 3}" y="${axis - 8}" fill="${c.muted}">${s} s</text>`);
    }
    for (const m of [t.timestamps.userspace, t.timestamps.multi_user, t.timestamps.finish]) {
        if (m) parts.push(`<line x1="${x(m)}" y1="${axis}" x2="${x(m)}" y2="${height}" stroke="${c.muted}" stroke-dasharray="4 3"/>`);
    }
    units.forEach((u, i) => {
        const y = axis + i * o.rowHeight;
        const dim = o.chain && !chain.has(u.id);
        const opacity = dim ? ' opacity="0.3"' : '';
        parts.push(`<text x="4" y="${y + o.rowHeight - 6}" fill="${c.fg}"${opacity}>${xml(u.id)}</text>`);
        const x0 = x(u.activating);
        const w = Math.max(1, x(unitEnd(u)) - x0);
        const plain = u.state !== 'failed' && !chain.has(u.id);
        const fill = u.state === 'failed' ? c.failed : chain.has(u.id) ? c.chain : c.activating;
        // the page draws the span of a plain unit in the accent colour, see-through (BootTimeline.svelte)
        parts.push(`<rect x="${x0}" y="${y + 4}" width="${w}" height="${o.rowHeight - 8}" rx="2" fill="${fill}"${plain ? ' fill-opacity="0.4"' : ''}${opacity}/>`);
        if (u.active !== undefined && u.active >= u.activating) parts.push(`<rect x="${x(u.active) - 1}" y="${y + 2}" width="2" height="${o.rowHeight - 4}" fill="${c.active}"${opacity}/>`);
    });
    parts.push('</svg>');
    return parts.join('\n');
}

/** How a unit of this boot differs from the same unit in the boot it is compared with, in ms. */
export interface UnitDelta {
    startMs: number;
    tookMs: number;
    /** the unit of the other boot, for its ghost bar */
    other: BootUnit;
}

/** The units of two boots paired by id: a unit only one of them started has no delta. */
export function compareUnits(current: readonly BootUnit[], previous: readonly BootUnit[]): Map<string, UnitDelta> {
    const before = new Map(previous.map((u) => [u.id, u]));
    const out = new Map<string, UnitDelta>();
    for (const u of current) {
        const o = before.get(u.id);
        if (!o) continue;
        out.set(u.id, {startMs: Math.round((u.activating - o.activating) * 1000), tookMs: u.duration_ms - o.duration_ms, other: o});
    }
    return out;
}

/**
 * A difference as the reader sees it: `+12.3 s`, `−340 ms`, `±0`. Below `quietMs` it is `±0`: a
 * few milliseconds either way is the noise of any two boots.
 */
export function deltaLabel(ms: number, lang: string, quietMs = 10): string {
    if (Math.abs(ms) < quietMs) return '±0';
    const sign = ms > 0 ? '+' : '−';
    return `${sign}${duration(Math.abs(ms), lang)}`;
}

/** Whether a difference is worth a colour: slower (`worse`) or faster (`better`) by a second or 10 %. */
export function deltaKind(ms: number, base: number): 'worse' | 'better' | '' {
    const big = Math.abs(ms) >= 1000 || (base > 0 && Math.abs(ms) >= base * 0.1 && Math.abs(ms) >= 100);
    if (!big) return '';
    return ms > 0 ? 'worse' : 'better';
}

/** B-153: when a unit restarted after the boot, on the wall clock - null without the boot's start or a restart. */
export function restartedAt(t: BootTimeline, u: BootUnit): Date | null {
    if (u.restarted === undefined || !t.started) return null;
    const began = Date.parse(t.started);
    return Number.isNaN(began) ? null : new Date(began + u.restarted * 1000);
}

/** B-153: the names in the note on the units started after the boot - at most `max`, and how many more there are. */
export function laterNames(t: BootTimeline, max = 8): {names: string[]; more: number} {
    const all = t.later_units ?? [];
    return {names: all.slice(0, max), more: Math.max(0, all.length - max)};
}
