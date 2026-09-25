// openccu-lite task 228, phase 2: the network shares of System → Storage - the shape the API
// answers, and the checks the add/edit dialog runs before it sends (the daemon checks again).

export type ShareKind = 'nfs' | 'cifs';

export interface ShareState {
    state: string;
    detail?: string;
    checked_at?: string;
    next_retry_at?: string;
    mounted: boolean;
    source?: string;
    options?: string;
    free_bytes?: number;
    total_bytes?: number;
    unsupported?: string;
}

export interface Share {
    id: string;
    kind: ShareKind;
    server: string;
    path: string;
    version: string;
    seal?: boolean;
    read_only: boolean;
    user?: string;
    domain?: string;
    has_password: boolean;
    created?: string;
    where: string;
    state: ShareState;
    uses?: {kind: string; name?: string; id?: string}[];
}

export interface SharesView {
    container: string;
    kinds: Record<ShareKind, string>;
    shares: Share[];
}

export interface ShareTest {
    ok: boolean;
    state: string;
    step: string;
    error?: string;
    free_bytes: number;
    total_bytes: number;
    write_mbps?: number;
    fs_type?: string;
    read_only?: boolean;
}

/** The name is the mount point's last part and the location id: a letter, then letters and
 * digits, lower case, 16 at most (a dash would be \x2d in the mount unit's name). */
export function nameError(name: string): string {
    if (!name) return 'A name is required.';
    if (name.length > 16) return 'At most 16 characters.';
    if (!/^[a-z][a-z0-9]*$/.test(name)) return 'Lower-case letters and digits, starting with a letter.';
    return '';
}

/** A suggestion for the name from the server: its first label, cleaned, or "nas". */
export function suggestName(server: string, taken: string[]): string {
    let base = (server.split('.')[0] ?? '').toLowerCase().replace(/[^a-z0-9]/g, '');
    if (!/^[a-z]/.test(base) || /^\d/.test(server)) base = 'nas';
    base = base.slice(0, 14);
    if (!taken.includes(base)) return base;
    for (let i = 2; i < 100; i++) {
        const n = `${base}${i}`;
        if (!taken.includes(n)) return n;
    }
    return base;
}

/** The path as the kind wants it: an NFS export absolute, an SMB share without slashes around it
 * (a backslash as Windows writes it becomes a slash). */
export function normalisePath(kind: ShareKind, path: string): string {
    const p = path.trim().replace(/\\/g, '/');
    if (kind === 'cifs') return p.replace(/^\/+|\/+$/g, '');
    return p === '' ? '' : '/' + p.replace(/^\/+/, '').replace(/\/+$/, '');
}

/** What a share is on the server: server:/export or //server/share. */
export function source(s: Pick<Share, 'kind' | 'server' | 'path'>): string {
    return s.kind === 'cifs' ? `//${s.server}/${s.path}` : `${s.server}:${s.path}`;
}
