/*
 * Task 101: the Log settings' levels tab beyond the four daemons of 27.8 - occulited's own level
 * with its debug areas, and multimacd's level, which falls back to rfd's while it has none.
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

/** The level multimacd starts with: its own, or rfd's while it has none. */
export function multimacdLevel(l: Pick<LogLevels, 'rfd' | 'multimacd'>): number {
    return l.multimacd ?? l.rfd;
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
        multimacd: l.multimacd ?? null,
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
