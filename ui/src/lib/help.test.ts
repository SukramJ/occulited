import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {CLOSE_DELAY, HelpState, OPEN_DELAY, place} from './help';

// task 51: the popup of lib/Help.svelte - its placement and its open/pinned state

const PHONE = {width: 360, height: 780};
const DESKTOP = {width: 1280, height: 800};
const POPUP = {width: 331, height: 120}; // min(360px, 92vw) on a 360 px phone
const at = (left: number, top: number, size = 16) => ({left, top, right: left + size, bottom: top + size});

describe('place', () => {
    it('puts the popup below the trigger when it fits, the arrow under the trigger', () => {
        const p = place(at(100, 100), {width: 360, height: 120}, DESKTOP);
        expect(p.side).toBe('below');
        expect(p.top).toBe(116 + 8);
        expect(p.left + p.arrow).toBe(108);
    });

    it('puts it above when there is no room below', () => {
        const p = place(at(100, 700), {width: 360, height: 200}, DESKTOP);
        expect(p.side).toBe('above');
        expect(p.top).toBe(700 - 8 - 200);
    });

    it('stays below when neither side has room but below has more, never above the viewport', () => {
        const p = place(at(100, 200), {width: 360, height: 900}, DESKTOP);
        expect(p.side).toBe('below');
        const q = place(at(100, 600), {width: 360, height: 900}, DESKTOP);
        expect(q.side).toBe('above');
        expect(q.top).toBe(8);
    });

    it('keeps the popup inside a 360 px phone for a ? at the right edge, in the middle and at the left', () => {
        for (const x of [4, 120, 180, 300, 340]) {
            const p = place(at(x, 100), POPUP, PHONE);
            expect(p.left, `x=${x}`).toBeGreaterThanOrEqual(8);
            expect(p.left + POPUP.width, `x=${x}`).toBeLessThanOrEqual(PHONE.width - 8);
            // the arrow stays on the popup, off its corners
            expect(p.arrow).toBeGreaterThanOrEqual(12);
            expect(p.arrow).toBeLessThanOrEqual(POPUP.width - 12);
        }
    });

    it('moves the arrow with the trigger when the popup is shifted back on screen', () => {
        const p = place(at(330, 100), POPUP, PHONE);
        expect(p.left).toBe(PHONE.width - 8 - POPUP.width);
        expect(p.left + p.arrow).toBe(338);
    });

    it('keeps the popup inside what is visible when a phone has zoomed out and panned a wide page', () => {
        // the Services page at 360 px with the timer dialog open, as measured: the visible part
        // starts at x 325, y 442, and the dialog title's ? is at 434,741
        const visible = {left: 325, top: 442, width: 360, height: 760};
        const small = {width: 331, height: 79};
        const p = place(at(434, 741), small, visible);
        expect(p.left).toBeGreaterThanOrEqual(visible.left + 8);
        expect(p.left + small.width).toBeLessThanOrEqual(visible.left + visible.width - 8);
        expect(p.left + p.arrow).toBe(442);
        expect(p.side).toBe('below');
        expect(p.top + small.height).toBeLessThanOrEqual(visible.top + visible.height);
        // above, it stops at the top of what is visible, not at the top of the page
        const q = place(at(434, 460), {width: 331, height: 900}, visible);
        expect(q.top).toBeGreaterThanOrEqual(visible.top);
    });
});

describe('HelpState', () => {
    let changes: [boolean, boolean][];
    const make = () => new HelpState((o, p) => changes.push([o, p]));
    beforeEach(() => {
        vi.useFakeTimers();
        changes = [];
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it('opens on hover after the delay, not before', () => {
        const h = make();
        h.hoverEnter();
        vi.advanceTimersByTime(OPEN_DELAY - 1);
        expect(h.open).toBe(false);
        vi.advanceTimersByTime(1);
        expect(h.open).toBe(true);
        expect(h.pinned).toBe(false);
        h.close();
    });

    it('does not open when the pointer leaves before the delay', () => {
        const h = make();
        h.hoverEnter();
        vi.advanceTimersByTime(OPEN_DELAY / 2);
        h.hoverLeave();
        vi.advanceTimersByTime(OPEN_DELAY * 2);
        expect(h.open).toBe(false);
        expect(changes).toEqual([]);
    });

    it('closes after the close delay when the pointer leaves, and stays when it enters the popup in time', () => {
        const h = make();
        h.hoverEnter();
        vi.advanceTimersByTime(OPEN_DELAY);
        h.hoverLeave(); // off the trigger ...
        vi.advanceTimersByTime(CLOSE_DELAY - 50);
        h.hoverEnter(); // ... into the popup
        vi.advanceTimersByTime(CLOSE_DELAY * 3);
        expect(h.open).toBe(true);
        h.hoverLeave(); // and out of the popup
        vi.advanceTimersByTime(CLOSE_DELAY - 1);
        expect(h.open).toBe(true);
        vi.advanceTimersByTime(1);
        expect(h.open).toBe(false);
    });

    it('pins on click: leaving no longer closes it, a second click does', () => {
        const h = make();
        h.click();
        expect([h.open, h.pinned]).toEqual([true, true]);
        h.hoverLeave();
        vi.advanceTimersByTime(CLOSE_DELAY * 5);
        expect(h.open).toBe(true);
        h.click();
        expect(h.open).toBe(false);
    });

    it('pins a popup that hovering opened instead of closing it', () => {
        const h = make();
        h.hoverEnter();
        vi.advanceTimersByTime(OPEN_DELAY);
        h.click();
        expect([h.open, h.pinned]).toEqual([true, true]);
        h.close();
    });

    it('keeps one popup open at a time', () => {
        const a = make();
        const b = make();
        a.click();
        b.click();
        expect(a.open).toBe(false);
        expect(b.open).toBe(true);
        b.close();
    });

    it('does not let a hover steal a pinned popup, but a hover-opened one gives way', () => {
        const a = make();
        const b = make();
        a.click();
        b.hoverEnter();
        vi.advanceTimersByTime(OPEN_DELAY * 2);
        expect(a.open).toBe(true);
        expect(b.open).toBe(false);
        a.close();
        a.hoverEnter();
        vi.advanceTimersByTime(OPEN_DELAY);
        b.hoverEnter();
        vi.advanceTimersByTime(OPEN_DELAY);
        expect(a.open).toBe(false);
        expect(b.open).toBe(true);
        b.close();
    });

    it('fires no timer into a disposed component', () => {
        const h = make();
        h.hoverEnter();
        h.dispose();
        vi.advanceTimersByTime(OPEN_DELAY * 2);
        expect(changes).toEqual([]);
    });
});
