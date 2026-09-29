// task 178: the Log page's pages and its windowed list - the arithmetic without the DOM, so it
// can be tested. The page holds up to LOG_KEEP entries in one contiguous run and renders only
// the rows in view plus a margin; a row's height is measured once it has been on screen, and
// until then it is the mean of the measured ones.

/** entries per page: about 0.4 s of journalctl on a Pi 3 (0.36 ms per entry) */
export const LOG_PAGE = 1000;
/** entries kept in the page at most; past that the far end is unloaded and reloads on scroll */
export const LOG_KEEP = 20_000;
/** the pixels beyond the view that are rendered above and below it */
export const LOG_OVERSCAN = 600;
/** how close to an end the view must be for the next page to load */
export const LOG_LOAD_AHEAD = 1500;

/** the top of every row and, at the end, the height of them all: offsets[i] is row i's top */
export function rowOffsets(n: number, height: (i: number) => number): Float64Array {
    const offsets = new Float64Array(n + 1);
    let y = 0;
    for (let i = 0; i < n; i++) {
        offsets[i] = y;
        y += height(i);
    }
    offsets[n] = y;
    return offsets;
}

/** the row at y: the first whose bottom is beyond it; n when y is past the last row */
export function rowAt(offsets: Float64Array, y: number): number {
    const n = offsets.length - 1;
    if (n <= 0 || y < 0) return 0;
    if (y >= (offsets[n] ?? 0)) return n;
    let lo = 0;
    let hi = n - 1;
    while (lo < hi) {
        const mid = (lo + hi) >> 1;
        if ((offsets[mid + 1] ?? 0) > y) hi = mid;
        else lo = mid + 1;
    }
    return lo;
}

/** the rows to render for a view of `viewport` px scrolled to `top`: [start, end) */
export function rowWindow(offsets: Float64Array, top: number, viewport: number, overscan = LOG_OVERSCAN): {start: number; end: number} {
    const n = offsets.length - 1;
    if (n <= 0) return {start: 0, end: 0};
    const start = rowAt(offsets, top - overscan);
    const end = Math.min(n, rowAt(offsets, top + viewport + overscan) + 1);
    return {start: Math.min(start, end), end};
}

/**
 * the run of entries with a page added at one end, cut to `keep` at the other: the page that
 * grew at the old end loses its newest entries and the other way round. `dropped` names the end
 * that lost entries, or null.
 */
export function joinPages<T>(lines: T[], page: T[], at: 'old' | 'new', keep = LOG_KEEP): {lines: T[]; dropped: 'old' | 'new' | null} {
    let out = at === 'old' ? [...page, ...lines] : [...lines, ...page];
    let dropped: 'old' | 'new' | null = null;
    if (out.length > keep) {
        out = at === 'old' ? out.slice(0, keep) : out.slice(out.length - keep);
        dropped = at === 'old' ? 'new' : 'old';
    }
    return {lines: out, dropped};
}

/** the number of entries as the count above the list says it, in the reader's locale */
export function countLabel(n: number, locale: string): string {
    return n.toLocaleString(locale);
}
