/**
 * Task 162: one reorderable list or table - the drag by its handle, the drop line, the scrolling at
 * an edge, Escape, the keyboard and what a screen reader hears after a move. Every list ordered by
 * hand uses it with lib/SortHandle.svelte as the handle: the addon dropdown (task 59, where it was
 * written first), the firewall's rules and the LED states; there are no ↑/↓ buttons beside it.
 *
 * - The drag: pointer events, so a mouse and a finger work alike. Only the handle starts one - a
 *   press on a field or a button in the row is that control's own - and the handle captures the
 *   pointer, so the drag goes on outside the list. `drag.to` is the index the row would land at,
 *   `line` the row the drop line stands above (lib/sortable.ts). Near an edge of the scrolling
 *   area - the list's own, or the page - it scrolls; Escape puts the row back where it was.
 * - The keyboard: ↑/↓ on the focused handle, Alt+↑/↓ anywhere else in the row (not on a select,
 *   where Alt+↓ opens it). The keyed each of the page moves the row's element rather than building
 *   it anew, but a browser drops the focus from an element that is taken out of the document and
 *   put back - so the control that was pressed gets it back, and a second press moves on.
 */
import {tick} from 'svelte';
import {dropLine, dropTarget, edgeStep, keyTarget} from './sortable';
import {t} from './i18n.svelte';

export interface SortableOptions {
    /** the rows that can move, as elements in the order shown */
    rows: () => HTMLElement[];
    /** how many rows can move */
    length: () => number;
    /** the row's name, for the screen reader's "{name} is now at position {n} of {total}" */
    name: (index: number) => string;
    /** put the row at `from` in at `to` (an index in the resulting list) */
    commit: (from: number, to: number) => void;
    /** whether the order can be changed now; false leaves every key and press to the page */
    enabled?: () => boolean;
}

export class Sortable {
    drag = $state<{key: string; from: number; to: number} | null>(null);
    /** what a screen reader hears after a move: the text of the page's live region */
    live = $state('');
    line = $derived(this.drag ? dropLine(this.opts.length(), this.drag.from, this.drag.to) : -1);

    #opts: SortableOptions;
    #y = 0;
    #moved = false;
    #frame = 0;
    #grip: HTMLElement | null = null;
    #pointer = -1;
    #scroller: HTMLElement | null = null;

    constructor(opts: SortableOptions) {
        this.#opts = opts;
    }
    get opts(): SortableOptions {
        return this.#opts;
    }
    #enabled(): boolean {
        return this.#opts.enabled?.() ?? true;
    }

    /** whether the row with this key is the one being dragged */
    dragging(key: string): boolean {
        return this.drag?.key === key;
    }
    /** whether the drop line stands above the row at this index */
    before(index: number): boolean {
        return this.line === index;
    }
    /** whether the drop line stands below the row at this index: the last one */
    after(index: number): boolean {
        const n = this.#opts.length();
        return this.line === n && index === n - 1;
    }

    #announce(from: number, to: number) {
        const name = this.#opts.name(from);
        const n = this.#opts.length();
        this.#opts.commit(from, to);
        this.live = t('{name} is now at position {n} of {total}', {name, n: Math.max(0, Math.min(to, n - 1)) + 1, total: n});
    }

    // ---- the pointer ------------------------------------------------------------------------
    down(ev: PointerEvent, index: number, key: string) {
        if (!this.#enabled() || this.drag) return;
        if (ev.pointerType === 'mouse' && ev.button !== 0) return;
        const grip = ev.currentTarget as HTMLElement;
        grip.setPointerCapture?.(ev.pointerId);
        this.#grip = grip;
        this.#pointer = ev.pointerId;
        this.#y = ev.clientY;
        this.#moved = false;
        this.#scroller = scrollParent(grip);
        this.drag = {key, from: index, to: index};
        ev.preventDefault();
        window.addEventListener('keydown', this.#onEscape, true);
        this.#frame = requestAnimationFrame(this.#step);
    }
    move(ev: PointerEvent) {
        if (!this.drag || ev.pointerId !== this.#pointer) return;
        if (Math.abs(ev.clientY - this.#y) > 2) this.#moved = true;
        this.#y = ev.clientY;
        this.#aim();
    }
    up(ev?: PointerEvent) {
        if (!this.drag || (ev && ev.pointerId !== this.#pointer)) return;
        const {from, to} = this.drag;
        this.#end();
        if (from !== to) this.#announce(from, to);
    }
    /** the drag ends without a move: Escape, or the browser took the pointer (pointercancel) */
    cancel() {
        if (this.drag) this.#end();
    }
    #end() {
        this.drag = null;
        cancelAnimationFrame(this.#frame);
        window.removeEventListener('keydown', this.#onEscape, true);
        if (this.#grip?.hasPointerCapture?.(this.#pointer)) this.#grip.releasePointerCapture(this.#pointer);
        this.#grip = null;
        this.#pointer = -1;
        this.#scroller = null;
    }
    #aim() {
        if (!this.drag) return;
        const mids = this.#opts.rows().map((el) => {
            const r = el.getBoundingClientRect();
            return r.top + r.height / 2;
        });
        this.drag.to = dropTarget(mids, this.drag.from, this.#y);
    }
    // Escape during a drag ends it and nothing else: a panel around the list stays open
    #onEscape = (ev: KeyboardEvent) => {
        if (ev.key !== 'Escape' || !this.drag) return;
        ev.preventDefault();
        ev.stopPropagation();
        this.cancel();
    };
    // near an edge the list or the page scrolls, once the pointer has moved: a press at the edge
    // alone scrolls nothing
    #step = () => {
        if (!this.drag) return;
        if (this.#moved) {
            const el = this.#scroller;
            const top = el ? Math.max(0, el.getBoundingClientRect().top) : 0;
            const bottom = el ? Math.min(window.innerHeight, el.getBoundingClientRect().bottom) : window.innerHeight;
            const by = edgeStep(this.#y, top, bottom);
            if (by !== 0) {
                if (el) el.scrollTop += by;
                else window.scrollBy(0, by);
                this.#aim();
            }
        }
        this.#frame = requestAnimationFrame(this.#step);
    };

    // ---- the keyboard -----------------------------------------------------------------------
    /** a key on the handle: ↑/↓ move the row, with or without Alt */
    handleKey(ev: KeyboardEvent, index: number) {
        if (ev.key === 'Escape' && this.drag) return; // #onEscape has it
        this.#key(ev, index);
    }
    /** a key anywhere in the row: Alt+↑/↓ move it, except on a select */
    rowKey(ev: KeyboardEvent, index: number) {
        if (!ev.altKey || (ev.target as HTMLElement | null)?.tagName === 'SELECT') return;
        this.#key(ev, index);
    }
    #key(ev: KeyboardEvent, index: number) {
        if (ev.key !== 'ArrowUp' && ev.key !== 'ArrowDown') return;
        if (!this.#enabled() || this.drag || ev.ctrlKey || ev.metaKey || ev.shiftKey) return;
        ev.preventDefault();
        ev.stopPropagation();
        const to = keyTarget(ev.key, index, this.#opts.length());
        if (to < 0) return;
        const active = document.activeElement as HTMLElement | null;
        this.#announce(index, to);
        void tick().then(() => {
            if (active && active.isConnected && document.activeElement !== active) active.focus();
        });
    }
}

/** The nearest ancestor that scrolls vertically, or null for the page itself. */
function scrollParent(el: HTMLElement): HTMLElement | null {
    for (let p = el.parentElement; p && p !== document.body && p !== document.documentElement; p = p.parentElement) {
        const oy = getComputedStyle(p).overflowY;
        if ((oy === 'auto' || oy === 'scroll' || oy === 'overlay') && p.scrollHeight > p.clientHeight) return p;
    }
    return null;
}
