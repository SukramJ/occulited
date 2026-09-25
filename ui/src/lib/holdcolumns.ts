// B-156: a table keeps its column widths while a filter narrows it. In the browser's automatic
// layout every column is as wide as its widest remaining cell, so fewer rows meant other widths
// at every keystroke. `use:holdColumns={filtering}` on the <table>:
// - while `filtering` is false it records the header cells' widths as fractions of the table,
//   whenever the table's size or its rows change - so the widths it pins are the ones the visitor
//   saw before typing, never ones measured after the first filtered render;
// - when `filtering` turns true it pins those fractions (`table-layout: fixed`, a percentage width
//   on each header cell, and the table's width relative to its container when it was wider than
//   that - a table in a scroll box), which keeps the proportions when the window is resized;
// - when `filtering` turns false it lets go, and the table takes its natural widths again.
// A table the phone rules turn into stacked blocks (`ol-stack`, no visible head) has no widths to
// hold; nothing is pinned then.

/** each cell's share of the total, or null when there is nothing to measure */
export function fractions(widths: number[]): number[] | null {
    const total = widths.reduce((a, b) => a + b, 0);
    if (widths.length === 0 || total <= 0 || widths.some((w) => !(w >= 0))) return null;
    return widths.map((w) => w / total);
}

function headCells(table: HTMLTableElement): HTMLTableCellElement[] {
    const row = table.tHead?.rows[0];
    return row ? [...row.cells] : [];
}

export function holdColumns(table: HTMLTableElement, filtering: boolean) {
    let saved: number[] | null = null;
    // the table's width over its container's, > 1 for a table that overflows into a scroll box
    let ratio = 1;
    let pinned = false;
    let frame = 0;

    const measure = () => {
        frame = 0;
        if (pinned) return;
        const cells = headCells(table);
        // a hidden head (the stacked phone layout) measures 0 in total: keep what was recorded
        // before; a hidden column (display: none) is a 0 and gets no width when pinned
        const f = fractions(cells.map((c) => c.getBoundingClientRect().width));
        if (!f) return;
        saved = f;
        const box = table.parentElement?.clientWidth ?? 0;
        ratio = box > 0 ? table.getBoundingClientRect().width / box : 1;
    };
    const schedule = () => {
        if (!frame && !pinned) frame = requestAnimationFrame(measure);
    };
    const pin = () => {
        const cells = headCells(table);
        const f = saved;
        if (!f || f.length !== cells.length) return;
        cells.forEach((c, i) => {
            const share = f[i] ?? 0;
            if (share > 0) c.style.width = `${(share * 100).toFixed(3)}%`;
        });
        if (ratio > 1.001) table.style.width = `${(ratio * 100).toFixed(3)}%`;
        table.style.tableLayout = 'fixed';
        pinned = true;
    };
    const release = () => {
        if (!pinned) return;
        headCells(table).forEach((c) => (c.style.width = ''));
        table.style.tableLayout = '';
        table.style.width = '';
        pinned = false;
        schedule();
    };

    const resize = typeof ResizeObserver === 'function' ? new ResizeObserver(schedule) : null;
    resize?.observe(table);
    const rows = typeof MutationObserver === 'function' ? new MutationObserver(schedule) : null;
    rows?.observe(table, {childList: true, subtree: true, characterData: true});
    measure();
    if (filtering) pin();

    return {
        update(next: boolean) {
            if (next && !pinned) {
                // the rows may already be filtered in this very update: pin what was recorded
                if (frame) {
                    cancelAnimationFrame(frame);
                    frame = 0;
                }
                pin();
            } else if (!next) release();
        },
        destroy() {
            resize?.disconnect();
            rows?.disconnect();
            if (frame) cancelAnimationFrame(frame);
        },
    };
}
