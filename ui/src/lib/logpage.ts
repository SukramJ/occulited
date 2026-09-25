// Task 93: the Log page's state in its URL - the source (all, system, kernel), the unit, the boot -
// and the pieces of its boot menu and kernel lines. The page's path is this one constant: when the
// page moves under /system (task 57) this line and the old path's alias in the router change,
// nothing else. Pure, so vitest runs it. (Task 57 moved it: the alias lives in lib/systemmenu.ts.)

export const LOG_BASE = '/system/log';

/** The tabs of the Log page's settings sheet behind the gear. */
export type LogSettingsTab = 'levels' | 'journal' | 'history';

/**
 * `settings=journal`, `settings=levels` or `settings=history` opens the sheet on that tab when the
 * page is reached by a link - the journal's Status warnings point there (task 85), the Interfaces
 * page's history there (task 214); anything else opens nothing.
 */
export function settingsParam(raw: string | null | undefined): LogSettingsTab | null {
    return raw === 'journal' || raw === 'levels' || raw === 'history' ? raw : null;
}

/**
 * What the viewer shows: every message, the system's without the kernel's (`kernel=0`), or the
 * kernel's alone (`kernel=1`, journald's kernel transport or dmesg).
 */
export type LogSource = 'all' | 'system' | 'kernel';

export function sourceParam(raw: string | null | undefined): LogSource {
    return raw === 'system' || raw === 'kernel' ? raw : 'all';
}

/** The API's `kernel` for a source: '' for all. */
export function kernelParam(source: LogSource): string {
    return source === 'kernel' ? '1' : source === 'system' ? '0' : '';
}

/**
 * The page's path with its state. The unit is the journal's filter (27.5) and has no place beside
 * the kernel's lines, which carry none; the boot stays with every source.
 */
export function logPath(q: {source?: LogSource; unit?: string; boot?: string; run?: string} = {}): string {
    const p = new URLSearchParams();
    if (q.source && q.source !== 'all') p.set('source', q.source);
    if (q.unit && q.source !== 'kernel') p.set('unit', q.unit);
    if (q.boot) p.set('boot', q.boot);
    if (q.run) p.set('run', q.run);
    const s = p.toString();
    return s ? `${LOG_BASE}?${s}` : LOG_BASE;
}

/**
 * A run as the route carries it (task 102): the id a firmware flash or an ACME attempt names, the
 * shape the API takes (runlog.ValidID). Anything else is dropped, so a mangled link shows the log.
 */
export function runParam(raw: string | null | undefined): string {
    const s = (raw ?? '').trim();
    return /^[0-9]{8}T[0-9]{6}-[0-9a-f]{8}$/.test(s) ? s : '';
}

/** A kernel line's time since the boot began, as dmesg prints it: `[   12.345678]`. */
export function kernelStamp(us: number | undefined): string {
    const v = Math.max(0, Math.floor(us ?? 0));
    const sec = Math.floor(v / 1_000_000);
    const frac = String(v % 1_000_000).padStart(6, '0');
    return `[${String(sec).padStart(5, ' ')}.${frac}]`;
}

/**
 * What a line's time column shows: a kernel line's time since the boot as dmesg prints it, or the
 * wall clock the box put on it (task 93). For a kernel line the box's wall clock is its boot's start
 * plus the kernel's own stamp (B-116), and a stamp of 0 is sent as no stamp at all, which is 0 too.
 * A kernel line without a time shows its stamp whatever the choice; every other line its time.
 */
export function lineStamp(l: {time: string; monotonic_us?: number}, kernel: boolean, clock: 'boot' | 'wall'): string {
    return kernel && (clock === 'boot' || !l.time) ? kernelStamp(l.monotonic_us) : l.time;
}

/**
 * A boot as the route carries it: a boot id of 32 hex digits, 0 for this boot or a negative
 * offset - what the API takes. Anything else is dropped (''), so a mangled link shows every boot
 * instead of an error.
 */
export function bootParam(raw: string | null | undefined): string {
    let s = (raw ?? '').trim().toLowerCase();
    if (s.length === 36 && s.split('-').length === 5) s = s.replaceAll('-', '');
    return /^[0-9a-f]{32}$/.test(s) || /^(0|-[1-9][0-9]{0,3})$/.test(s) ? s : '';
}

/** One boot of GET /boots. */
export interface BootInfo {
    /** journalctl's offset; absent for a boot kept on the Services page that the journal no longer holds */
    index?: number;
    boot_id: string;
    first?: string;
    last?: string;
    current: boolean;
    /** in the journal: the Log page has lines of it */
    journal?: boolean;
    /** its boot timeline is kept (the Services page) */
    snapshot?: boolean;
    total_ms?: number;
}

export interface BootList {
    current: string;
    source: 'journald' | 'syslog';
    persistent: boolean;
    boots: BootInfo[];
    error?: string;
}

/** Whether a boot of the route is an earlier one: not every boot, not this one. */
export function isEarlierBoot(boot: string, current: string): boolean {
    return boot !== '' && boot !== '0' && boot !== current;
}

/** The boot of the list a route's boot names: by id, or by offset. */
export function findBoot(list: readonly BootInfo[], boot: string): BootInfo | undefined {
    if (boot === '') return undefined;
    if (boot === '0') return list.find((b) => b.current);
    if (/^-\d+$/.test(boot)) return list.find((b) => b.index === Number(boot));
    return list.find((b) => b.boot_id === boot);
}

/** How long a boot ran as far as the journal knows it: first to last entry, in ms; null without both. */
export function bootSpanMs(b: Pick<BootInfo, 'first' | 'last'>): number | null {
    if (!b.first || !b.last) return null;
    const ms = Date.parse(b.last) - Date.parse(b.first);
    return Number.isFinite(ms) && ms >= 0 ? ms : null;
}

export interface DurationUnits {
    d: string;
    h: string;
    min: string;
    s: string;
}
const UNITS: DurationUnits = {d: 'd', h: 'h', min: 'min', s: 's'};

/** A duration in its two largest units: `45 s`, `4 min 5 s`, `13 h 28 min`, `2 d 3 h`. */
export function durationLabel(ms: number, u: DurationUnits = UNITS): string {
    const total = Math.max(0, Math.round(ms / 1000));
    const d = Math.floor(total / 86400);
    const h = Math.floor((total % 86400) / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    if (d > 0) return h ? `${d} ${u.d} ${h} ${u.h}` : `${d} ${u.d}`;
    if (h > 0) return m ? `${h} ${u.h} ${m} ${u.min}` : `${h} ${u.h}`;
    if (m > 0) return s ? `${m} ${u.min} ${s} ${u.s}` : `${m} ${u.min}`;
    return `${s} ${u.s}`;
}
