/*
 * Task 51: the logic of lib/Help.svelte, kept out of the component so it can be tested without a
 * browser - where the popup goes, and when it opens and closes.
 *
 * The rules (task 51's "Shape"):
 *
 * - hover opens after OPEN_DELAY and closes CLOSE_DELAY after the pointer has left both the
 *   trigger and the popup, so the pointer can travel from the ? into the popup and click a link
 *   there; the component only feeds hover in on a device that can hover at all;
 * - a click (or a tap, or Enter/Space on the button) opens at once and *pins*: leaving no longer
 *   closes it, a second click, Escape or a click outside does;
 * - one popup at a time - opening one closes the other. A hover does not steal a popup the user
 *   has pinned: sweeping the pointer across a page on the way to a button must not close the text
 *   someone is reading.
 */

export const OPEN_DELAY = 300;
export const CLOSE_DELAY = 200;

/** The rectangle of the trigger, as getBoundingClientRect() gives it. */
export interface Box {
    readonly left: number;
    readonly top: number;
    readonly right: number;
    readonly bottom: number;
}

export interface Placement {
    /** viewport coordinates of the popup's top left corner (it is `position: fixed`) */
    top: number;
    left: number;
    /** below the trigger, or above it when there is no room below */
    side: 'below' | 'above';
    /** where the arrow sits, from the popup's left edge: under the trigger's centre */
    arrow: number;
}

/**
 * Where the popup goes: below the trigger when it fits there (or when there is at least as much
 * room below as above - a popup taller than both halves scrolls inside itself), above otherwise.
 * Horizontally it starts a little left of the trigger, so the arrow sits under the ?, and is
 * shifted back inside the viewport when that would run off an edge - the same idea as
 * lib/popover.ts `onscreen`, which flips a menu to its trigger's right edge, but a popup of
 * 360 px next to a 16 px icon has to be clamped rather than flipped: flipping would still leave
 * it off screen on a phone when the ? is in the middle.
 *
 * `viewport` is the part of the page the reader sees, in the coordinates a fixed element is placed
 * in. That is 0,0 and the window's size, except on a phone that zoomed out a page wider than its
 * screen and panned it: there the visible part starts at the visual viewport's offset (measured on
 * the Services page at 360 px with a dialog open: x 325, y 442), and a popup clamped into 0..360
 * sat at x 21, off the screen.
 */
export function place(trigger: Box, popup: {width: number; height: number}, viewport: {left?: number; top?: number; width: number; height: number}, gap = 8, margin = 8): Placement {
    const vl = viewport.left ?? 0;
    const vt = viewport.top ?? 0;
    const below = vt + viewport.height - trigger.bottom - gap - margin;
    const above = trigger.top - vt - gap - margin;
    const side = popup.height <= below || below >= above ? 'below' : 'above';
    const top = side === 'below' ? trigger.bottom + gap : Math.max(vt + margin, trigger.top - gap - popup.height);

    const centre = (trigger.left + trigger.right) / 2;
    let left = centre - 18;
    if (left + popup.width > vl + viewport.width - margin) left = vl + viewport.width - margin - popup.width;
    if (left < vl + margin) left = vl + margin;
    // the arrow stays under the trigger, but never on the popup's rounded corners
    const arrow = Math.min(Math.max(centre - left, 12), Math.max(12, popup.width - 12));
    return {top, left, side, arrow};
}

// the one popup that is open, whichever component it belongs to
let current: HelpState | null = null;

/**
 * The open/pinned state of one Help and its two timers. `onchange` is called with every change,
 * and the component copies the two flags into its reactive state.
 */
export class HelpState {
    open = false;
    pinned = false;
    private openTimer: ReturnType<typeof setTimeout> | undefined;
    private closeTimer: ReturnType<typeof setTimeout> | undefined;

    constructor(private readonly onchange: (open: boolean, pinned: boolean) => void) {}

    /** The pointer entered the trigger or the popup (a device that can hover only). */
    hoverEnter(): void {
        this.cancelClose();
        if (this.open) return;
        if (current && current !== this && current.pinned) return;
        if (this.openTimer === undefined) this.openTimer = setTimeout(() => this.show(false), OPEN_DELAY);
    }

    /** The pointer left the trigger or the popup. */
    hoverLeave(): void {
        this.cancelOpen();
        if (!this.open || this.pinned) return;
        this.cancelClose();
        this.closeTimer = setTimeout(() => this.close(), CLOSE_DELAY);
    }

    /**
     * A click, a tap, or Enter/Space on the button. A popup opened by hovering is pinned by the
     * click rather than closed: the pointer was already on the ? and the reader asked to keep it.
     */
    click(): void {
        this.cancelOpen();
        this.cancelClose();
        if (this.open && this.pinned) this.close();
        else this.show(true);
    }

    close(): void {
        this.cancelOpen();
        this.cancelClose();
        if (current === this) current = null;
        if (!this.open && !this.pinned) return;
        this.open = false;
        this.pinned = false;
        this.onchange(false, false);
    }

    /** The component is gone: no timer may fire into it, and it is no longer the open one. */
    dispose(): void {
        this.cancelOpen();
        this.cancelClose();
        if (current === this) current = null;
    }

    private show(pinned: boolean): void {
        this.openTimer = undefined;
        if (current && current !== this) current.close();
        current = this;
        this.open = true;
        this.pinned = pinned;
        this.onchange(true, pinned);
    }

    private cancelOpen(): void {
        if (this.openTimer !== undefined) clearTimeout(this.openTimer);
        this.openTimer = undefined;
    }

    private cancelClose(): void {
        if (this.closeTimer !== undefined) clearTimeout(this.closeTimer);
        this.closeTimer = undefined;
    }
}
