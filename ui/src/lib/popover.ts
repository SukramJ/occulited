// task 40: a popover anchored at its trigger's left edge (`.ol-menupop`, left: 0) runs off the
// right edge of a phone when the trigger sits in the right half of a wrapped toolbar - the page
// then scrolls sideways and a tap lands next to the button it aimed at. This action, applied to
// the popover element when it mounts, flips it to the trigger's right edge in that case. The
// left edge is not guarded: `max-width: 92vw` keeps the popover narrower than the viewport, and
// a trigger is never at the very right of the screen with nothing to its left.
export function onscreen(el: HTMLElement): void {
    const r = el.getBoundingClientRect();
    if (r.right > window.innerWidth - 4) {
        el.style.left = 'auto';
        el.style.right = '0';
    }
}
