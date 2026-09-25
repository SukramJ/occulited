/*
 * Task 98: the motion of the parts of a page that open in place - a Disclosure's panel (*USB
 * devices*, *Set key*, *Add user*, ...), the boot timeline's body on the Services page, the
 * silenced warnings on Status, a certificate request's text, the LED page's <details>. One motion
 * for all of them, so they move alike: the part grows from its trigger's place to its full height
 * and fades in (200 ms, ease-out); closing is the reverse and a little faster (150 ms). The content
 * below moves along instead of jumping by the part's height.
 *
 * Popovers and menus (the top bar's menus, the power menu, Select, TimeRange, Help's ?, the ⋯ and
 * boot and download menus) float over the page and move nothing; they keep their own behaviour.
 * The metadata tree's collapsing rows are rows of a table, not a panel, and stay instant too.
 *
 * With `prefers-reduced-motion: reduce` nothing moves: the part is there at once and gone at once.
 * The media query is read at the start of each opening, so a change of the setting counts at once.
 *
 * Why not Svelte's `slide`: it clips with `overflow: hidden`, and `hidden` makes the element a block
 * formatting context for the length of the animation - a child's margin that collapses through the
 * element's edge the rest of the time stays inside it, and the content below jumps by that margin
 * when the motion ends. `overflow: clip` clips the same without that.
 */
import type {TransitionConfig} from 'svelte/transition';
import {cubicOut} from 'svelte/easing';

export const OPEN_MS = 200;
export const CLOSE_MS = 150;
/** cubicOut as a CSS timing function, for the <details> that the Web Animations API moves */
const EASE_OUT = 'cubic-bezier(0.33, 1, 0.68, 1)';

export function reducedMotion(): boolean {
    return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/** the box dimensions the motion takes from 0 to the element's own (border-box `height` included) */
const DIMENSIONS = [
    ['height', 'height'],
    ['padding-top', 'paddingTop'],
    ['padding-bottom', 'paddingBottom'],
    ['margin-top', 'marginTop'],
    ['margin-bottom', 'marginBottom'],
    ['border-top-width', 'borderTopWidth'],
    ['border-bottom-width', 'borderBottomWidth'],
] as const;

/**
 * The transition: `in:reveal` and `out:reveal={{duration: CLOSE_MS}}` on the element an `{#if}`
 * adds. Svelte runs it only when that block itself toggles, not when the page around it mounts.
 */
export function reveal(node: Element, {duration = OPEN_MS}: {duration?: number} = {}): TransitionConfig {
    if (reducedMotion()) return {duration: 0};
    const style = getComputedStyle(node);
    const sizes = DIMENSIONS.map(([css, key]) => [css, parseFloat(style[key]) || 0] as const);
    return {
        duration,
        easing: cubicOut,
        // no trailing `;`: Svelte turns the string into a keyframe and stops at an empty part
        css: (t) => [`overflow: clip`, `min-height: 0`, `opacity: ${t}`, ...sizes.map(([css, v]) => `${css}: ${t * v}px`)].join('; '),
    };
}

/**
 * The same motion for a native <details>: its summary's click - a mouse, Enter or Space - is taken
 * over, the element moves between its closed and its open height, and a closing one gives up
 * `open` when the motion ends, so the `toggle` event still reports the state. A click during the
 * motion turns it round from where it is. Without motion (or without the Web Animations API) the
 * click is left to the browser.
 */
export function revealDetails(details: HTMLDetailsElement): {destroy(): void} {
    const summary = details.querySelector(':scope > summary');
    let motion: Animation | null = null;
    let closing = false;
    const height = () => details.getBoundingClientRect().height;
    function onClick(ev: Event) {
        if (reducedMotion() || typeof details.animate !== 'function') return;
        ev.preventDefault();
        const from = height();
        motion?.cancel();
        const close = details.open && !closing;
        let to: number;
        if (close) {
            // the closed height, measured without a frame in between
            details.open = false;
            to = height();
            details.open = true;
        } else {
            details.open = true;
            to = height();
        }
        closing = close;
        details.style.overflow = 'clip';
        const m = details.animate([{height: `${from}px`}, {height: `${to}px`}], {duration: close ? CLOSE_MS : OPEN_MS, easing: EASE_OUT});
        motion = m;
        m.onfinish = () => {
            if (motion !== m) return;
            motion = null;
            if (closing) details.open = false;
            closing = false;
            details.style.overflow = '';
        };
    }
    summary?.addEventListener('click', onClick);
    return {
        destroy() {
            summary?.removeEventListener('click', onClick);
            motion?.cancel();
        },
    };
}
