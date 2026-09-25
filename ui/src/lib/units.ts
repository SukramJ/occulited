// Task 49: what systemd's UnitFileState means for a reader of the Services page. The API carries
// the raw state beside its yes/no verdict (`enabled`), and the finer states are exactly what tells
// whether the page's switch means anything: an addon unit is `generated` (it comes from the addon's
// rc.d script), a `static` unit has no [Install] section and cannot be switched at all, and
// `masked-runtime` is what Disable on the page does (B-26's runtime mask). The mapping lives here,
// apart from the page, so it is tested; the page only translates the kind into its words.
export type EnabledKind = 'yes' | 'addon' | 'static' | 'no' | 'off';

const KINDS: Record<string, EnabledKind> = {
    enabled: 'yes',
    'enabled-runtime': 'yes',
    alias: 'yes',
    indirect: 'yes',
    generated: 'addon',
    static: 'static',
    disabled: 'no',
    masked: 'off',
    'masked-runtime': 'off',
};

/**
 * The cell a unit gets. A known state decides; without one (busybox has none, the rc.d script's
 * executable bit is the verdict there) or with a state systemd adds later, the verdict does.
 */
export function enabledKind(state: string | undefined, enabled: boolean | undefined): EnabledKind {
    return (state && KINDS[state]) || (enabled === false ? 'no' : 'yes');
}

/** A raw state the table does not know - shown beside the verdict, so nothing is hidden. */
export function unknownState(state: string | undefined): string {
    return state && !KINDS[state] ? state : '';
}

/** The column's order: on, on through an addon's script, static, off, switched off. */
export const ENABLED_RANK: Record<EnabledKind, number> = {yes: 0, addon: 1, static: 2, no: 3, off: 4};

/** systemctl refuses enable and disable on a static unit, so the page does not offer them. */
export function switchable(state: string | undefined): boolean {
    return state !== 'static';
}

// B-65: the Status cell and its dot. Red was `!running && enabled`, and `static` counts as enabled,
// so every static unit, every unit a timer starts and every unit a condition keeps off this box
// (hs485d without a wired interface, hmlangw outside LAN gateway mode) looked failed. Red is for a
// failure now: systemd's `failed`, or a one-shot whose result is not success. A unit skipped by a
// condition says so with a neutral dot; anything else that is not running is plainly stopped.
// Task 94: a unit systemd is still starting (activating) is `starting` - hmipserver's JVM for about
// 40 s after the web UI is up at boot - neither running nor stopped, with the accent's dot.
// openccu-lite B-158: `ended` is an addon that keeps a daemon whose unit is empty - the daemon
// died, the unit (a oneshot) still says active. Red, with its own word (Exited).
export type StatusKind = 'running' | 'starting' | 'completed' | 'ended' | 'failed' | 'skipped' | 'stopped';

/** The part of a service the Status cell reads. */
export interface UnitState {
    running: boolean;
    oneshot?: boolean;
    result?: string;
    failed?: boolean;
    skipped?: boolean;
    starting?: boolean;
    ended?: boolean;
}

export function statusKind(s: UnitState): StatusKind {
    if (s.ended) return 'ended';
    if (s.oneshot) return s.failed || (s.result && s.result !== 'success') ? 'failed' : 'completed';
    if (s.running) return 'running';
    if (s.starting) return 'starting';
    if (s.failed) return 'failed';
    if (s.skipped) return 'skipped';
    return 'stopped';
}

/** The dot's classes (app.css `.ol-dot`; `skipped` is the page's own hollow grey ring). */
export function statusDot(s: UnitState): string {
    const k = statusKind(s);
    if (k === 'ended') return 'err';
    if (s.oneshot) return k === 'failed' ? 'once failed' : 'once';
    return k === 'running' ? 'ok' : k === 'starting' ? 'starting' : k === 'failed' ? 'err' : k === 'skipped' ? 'skipped' : '';
}

/** The Status column's order: running first, then what starts, what completed, whose daemon ended, what failed, what was skipped, what is stopped. */
export const STATUS_RANK: Record<StatusKind, number> = {running: 0, starting: 1, completed: 2, ended: 3, failed: 4, skipped: 5, stopped: 6};

/** A span of seconds as the page writes it: `42 s`, `17 min`, `2 h 3 min`, `7 d 1 h`. */
export function span(seconds: number): string {
    const s = Math.max(0, Math.floor(seconds));
    if (s < 60) return `${s} s`;
    if (s < 3600) return `${Math.floor(s / 60)} min`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ${Math.floor((s % 3600) / 60)} min`;
    return `${Math.floor(s / 86400)} d ${Math.floor((s % 86400) / 3600)} h`;
}
