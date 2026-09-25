/**
 * Task 162: the arithmetic of a reorderable list or table - the drag by a handle, the drop line and
 * the keyboard's moves - for every list that is ordered by hand (the addon dropdown's first group,
 * the firewall's rules, the LED states). Pure, so it is tested without a browser; the pointer and
 * the keys are lib/sortable.svelte.ts, the handle lib/SortHandle.svelte. Task 59 wrote the first
 * three for the addon dropdown (lib/addonorder.ts).
 */

/** The list with the entry at `from` taken out and put in at `to` (an index in the resulting list). */
export function moved<T>(list: readonly T[], from: number, to: number): T[] {
    const out = list.slice();
    if (from < 0 || from >= out.length) return out;
    const [item] = out.splice(from, 1);
    out.splice(Math.max(0, Math.min(to, out.length)), 0, item!);
    return out;
}

/**
 * Where a dragged row lands: its index in the resulting list is the number of *other* rows whose
 * middle lies above the pointer. `mids` are the rows' vertical middles in the order shown, `from`
 * the dragged row.
 */
export function dropTarget(mids: readonly number[], from: number, y: number): number {
    let n = 0;
    mids.forEach((m, i) => {
        if (i !== from && m < y) n++;
    });
    return n;
}

/**
 * The row - by its index as shown now - above which the drop line is drawn while the dragged row
 * `from` would land at `to`; `length` (one past the last row) means below the last row, and -1
 * means the drop would change nothing, so no line.
 */
export function dropLine(length: number, from: number, to: number): number {
    if (to === from) return -1;
    return to < from ? to : Math.min(to + 1, length);
}

/**
 * Where a key moves the row at `index` of `length`: ↑ one up, ↓ one down; -1 for any other key
 * and at either end, where nothing moves.
 */
export function keyTarget(key: string, index: number, length: number): number {
    const to = key === 'ArrowUp' ? index - 1 : key === 'ArrowDown' ? index + 1 : -1;
    return to >= 0 && to < length && index >= 0 && index < length ? to : -1;
}

/** How near an edge of the scrolling area (px) the pointer starts it scrolling during a drag. */
export const EDGE_PX = 48;
/** The fastest it scrolls (px per frame), with the pointer at the edge or beyond it. */
export const EDGE_MAX_STEP = 18;

/**
 * How far to scroll per frame while a drag holds the pointer at `y` in an area that is visible
 * from `top` to `bottom`: negative near the top, positive near the bottom, faster the nearer the
 * edge, 0 in between. An area lower than two edge zones scrolls from its halves.
 */
export function edgeStep(y: number, top: number, bottom: number, edge = EDGE_PX, max = EDGE_MAX_STEP): number {
    const zone = Math.max(1, Math.min(edge, (bottom - top) / 2));
    if (y < top + zone) return -Math.ceil(max * Math.min(1, (top + zone - y) / zone));
    if (y > bottom - zone) return Math.ceil(max * Math.min(1, (y - (bottom - zone)) / zone));
    return 0;
}
