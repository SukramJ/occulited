/*
 * Task 101: the Log settings' levels tab beyond the four daemons of 27.8 - occulited's own level
 * with its debug areas, and multimacd's own level: Debug or Info (task 297).
 */
import type {LogLevels} from './api';

/** occulited's levels, lowest first (internal/logctl.Levels). */
export const OCCULITED_LEVELS = ['debug', 'info', 'warn', 'error'] as const;

/** The parts of occulited whose debug lines can be switched on alone, in logctl.Areas' order. */
export const OCCULITED_AREAS: {id: string; label: string}[] = [
    {id: 'acme', label: 'Certificate (ACME)'},
    {id: 'radio-firmware', label: 'Radio firmware'},
    {id: 'addons', label: 'Addons'},
    {id: 'metadata', label: 'Metadata'},
    {id: 'http', label: 'HTTP requests'},
    {id: 'auth', label: 'Logins'},
    {id: 'led', label: 'Status LED'},
    {id: 'rpc', label: 'lite-rpc'},
];

/**
 * Task 297: multimacd's levels. Only Debug and Info: at a quieter level it logs nothing at its start,
 * and the system reads that start to tell whether the radio module answered (B-275).
 */
export const MULTIMACD_LEVELS = [
    {v: 1, k: 'Debug'},
    {v: 2, k: 'Info'},
] as const;

/** multimacd's level as the page shows it: 1 is Debug, anything else (none, an older 3-7) Info - as the system reads it. */
export function multimacdLevel(n: number | null | undefined): 1 | 2 {
    return n === 1 ? 1 : 2;
}

/** An area switched on or off; the list stays in OCCULITED_AREAS' order, each area once. */
export function toggleArea(areas: string[], id: string, on: boolean): string[] {
    const set = new Set(areas.filter((a) => a !== id));
    if (on) set.add(id);
    return OCCULITED_AREAS.map((a) => a.id).filter((a) => set.has(a));
}

/** What the form holds, for the "not saved yet" question: every setting, nothing of a PUT's answer. */
export function levelsKey(l: LogLevels): string {
    return JSON.stringify({
        rfd: l.rfd,
        hs485d: l.hs485d,
        multimacd: multimacdLevel(l.multimacd),
        hmip: l.hmip,
        loghost: l.loghost,
        lighttpd: l.lighttpd,
        occulited: l.occulited ? {level: l.occulited.level, debug_areas: toggleArea(l.occulited.debug_areas, '', false)} : null,
    });
}

/** Whether the debug note belongs on the page: occulited at debug, or an area at debug. */
export function occulitedDebugOn(l: Pick<LogLevels, 'occulited'>): boolean {
    return !!l.occulited && (l.occulited.level === 'debug' || l.occulited.debug_areas.length > 0);
}
