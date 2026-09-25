// The browser half of the reboot countdown (lib/bootbar.ts): the localStorage entry, the health
// route's polls and the checkpoints they show. Used by occulited's page and by lighttpd's waiting
// page, so it imports nothing of the shell.

import {defaultExpect, ENTRY_KEY, newEntry, observe, parseEntry, classifyHealth, uptimeOf, validExpect, type BootEntry, type BootKind, type Expect} from './bootbar';

/** The entry, when there is a current one; a stale or broken one is removed on the way. */
export function readEntry(now = Date.now()): BootEntry | null {
    let raw: string | null = null;
    try {
        raw = localStorage.getItem(ENTRY_KEY);
    } catch {
        return null;
    }
    const e = parseEntry(raw, now);
    if (!e && raw !== null) removeEntry();
    return e;
}

export function writeEntry(e: BootEntry): void {
    try {
        localStorage.setItem(ENTRY_KEY, JSON.stringify(e));
    } catch {
        /* the bar still runs from the page's own copy */
    }
}

export function removeEntry(): void {
    try {
        localStorage.removeItem(ENTRY_KEY);
    } catch {
        /* ignore */
    }
}

/** How long one health poll may take before it counts as no answer. */
export const PROBE_TIMEOUT_MS = 5000;

/** One poll of the health route: the status, null for no answer, and the body when it was JSON. */
export async function probeHealth(timeoutMs = PROBE_TIMEOUT_MS): Promise<{status: number | null; body: unknown}> {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), timeoutMs);
    try {
        const r = await fetch('/api/system/v1/health', {cache: 'no-store', signal: ctl.signal});
        let body: unknown = null;
        try {
            body = await r.json();
        } catch {
            /* not JSON: lighttpd's own page, or none */
        }
        return {status: r.status, body};
    } catch {
        return {status: null, body: null};
    } finally {
        clearTimeout(timer);
    }
}

/**
 * Writes the entry right before a reboot is requested: the box's own expectation when `load`
 * answers in time (GET /api/system/v1/boot-expect), the built-in figures otherwise.
 */
export async function beginBoot(kind: BootKind, load: () => Promise<{expect?: unknown} | null | undefined>, platform?: string | null, timeoutMs = 3000): Promise<BootEntry> {
    let expect: Expect | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
        const late = new Promise<null>((resolve) => (timer = setTimeout(() => resolve(null), timeoutMs)));
        const answer = await Promise.race([Promise.resolve().then(load), late]);
        expect = validExpect(answer?.expect);
    } catch {
        /* the built-in figures */
    } finally {
        clearTimeout(timer);
    }
    const entry = newEntry(kind, Date.now(), expect ?? defaultExpect(platform, kind));
    writeEntry(entry);
    return entry;
}

export interface WatchOptions {
    /** the entry the page wrote; the stored one is preferred while it exists (the other page may have added to it) */
    entry: BootEntry;
    /** occulited's uptime before the request, -1 when unknown */
    before: number;
    pollMs?: number;
    /** every change of the entry */
    onUpdate?: (e: BootEntry) => void;
    /** the box stopped answering (a halt stops here) */
    onDown?: (e: BootEntry) => boolean | void;
    /** occulited answers again after the reboot */
    onBack: (e: BootEntry) => void;
}

/**
 * Polls the health route until the box is back and records the checkpoints on the way. `onDown`
 * returning true stops the polling; the function returned stops it too.
 */
export function watchBoot(o: WatchOptions): () => void {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let local = o.entry;
    const tick = async () => {
        const {status, body} = await probeHealth();
        if (stopped) return;
        const cls = classifyHealth(status, body);
        const current = readEntry() ?? local;
        const {entry, back} = observe(current, cls, uptimeOf(body), o.before, Date.now());
        if (entry !== current) {
            writeEntry(entry);
            o.onUpdate?.(entry);
        }
        local = entry;
        if (cls === 'down' && o.onDown?.(entry) === true) {
            stopped = true;
            return;
        }
        if (back) {
            stopped = true;
            o.onBack(entry);
            return;
        }
        timer = setTimeout(tick, o.pollMs ?? 2000);
    };
    timer = setTimeout(tick, o.pollMs ?? 2000);
    return () => {
        stopped = true;
        clearTimeout(timer);
    };
}
