// openccu-lite task 228, phases 3 and 4: the one location picker - where the journal's copies, a
// backup or the database are kept, as a location id and a folder (internal/location):
//   userfs:<folder>, usb:<label>/<folder>, share:<name>/<folder>
// never a raw mount path, so a stick in another port or a share mounted again still resolves.

export type LocationUse = 'journal' | 'backup' | 'store';
export type LocationKind = 'userfs' | 'usb' | 'share';

export interface LocationUseOf {
    kind: 'journal' | 'backup' | 'store';
    name?: string;
    id?: string;
    folder?: string;
}

export interface Location {
    id: string;
    kind: LocationKind;
    name: string;
    detail?: string;
    state: string;
    state_detail?: string;
    free_bytes?: number;
    total_bytes?: number;
    read_only?: boolean;
    uses: LocationUseOf[];
    allowed: boolean;
    code?: string;
    reason?: string;
    userfs_prefix?: string;
    fixed?: boolean;
    default?: string;
}

export interface Parsed {
    id: string;
    kind: LocationKind;
    name: string;
    folder: string;
}

/** Parse a stored location; null for anything else. */
export function parseLocation(s: string): Parsed | null {
    const v = s.trim();
    const i = v.indexOf(':');
    if (i < 0) return null;
    const kind = v.slice(0, i);
    const rest = v.slice(i + 1);
    if (kind === 'userfs') return rest ? {id: 'userfs', kind, name: '', folder: rest} : null;
    if (kind === 'usb' || kind === 'share') {
        const j = rest.indexOf('/');
        const name = j < 0 ? rest : rest.slice(0, j);
        const folder = j < 0 ? '' : rest.slice(j + 1).replace(/\/+$/, '');
        if (!name) return null;
        return {id: `${kind}:${name}`, kind, name, folder};
    }
    return null;
}

/** The stored form of a location id and a folder. */
export function formatLocation(id: string, folder: string): string {
    const f = folder.replace(/^\/+|\/+$/g, '');
    if (id === 'userfs') return `userfs:${f}`;
    return f ? `${id}/${f}` : id;
}

const SEG = /^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$/;
export const MAX_DEPTH = 4;

/** Why a folder cannot be used ('' when it can): up to four levels of letters, digits, . _ -,
 * none hidden, no "..". Empty only where allowEmpty says (a share's root). */
export function folderError(folder: string, allowEmpty: boolean, prefix?: string, fixed?: boolean): string {
    const f = folder.replace(/\/+$/, '');
    if (!f) return allowEmpty ? '' : 'A folder is required.';
    const parts = f.split('/');
    if (parts.length > MAX_DEPTH) return 'At most four folder levels.';
    if (parts.some((p) => !SEG.test(p) || p === '..')) return 'Folders are letters, digits, . _ and -, not starting with a dot.';
    if (prefix && fixed && f !== prefix) return 'On the system storage this is always {prefix}.';
    if (prefix && f !== prefix && !f.startsWith(prefix + '/')) return 'On the system storage the folder is {prefix} or below it.';
    return '';
}

/** A location's state for its dot: good, idle or bad. */
export function stateTone(state: string): 'good' | 'idle' | 'bad' {
    if (state === 'present' || state === 'mounted' || state === 'writable') return 'good';
    if (state === 'idle' || state === 'unsupported') return 'idle';
    return 'bad';
}
