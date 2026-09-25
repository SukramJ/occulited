/*
 * Task 98, refined by the maintainer (2026-09-12, late evening): "buttons that open in-line panels like
 * eg usb devices button should do this nicely animated. the button should grow into the panel, on close
 * vice versa. and the content below should be moved with this smooth animation".
 *
 * The geometry of that morph, apart from lib/Disclosure.svelte so that it can be tested without a
 * browser. A FLIP motion: the panel is put in its real place first, then drawn where the button was -
 * translated to the button's corner, the button's size, padding, radius, surface, border colour and
 * shadow - and moved to its own; closing is the same towards the button's box. The Web Animations API
 * runs it, so every engine gets it (no View Transitions needed); the button's label fades out early in
 * the opening and in late in the closing, the panel's content the other way round.
 */

export const MORPH_OPEN_MS = 240;
export const MORPH_CLOSE_MS = 180;
/** cubicOut, as a CSS timing function */
export const MORPH_EASING = 'cubic-bezier(0.33, 1, 0.68, 1)';

/** a box as the reader sees it: where it is (viewport coordinates) and what it looks like */
export interface Look {
    left: number;
    top: number;
    width: number;
    height: number;
    padding: string;
    borderRadius: string;
    backgroundColor: string;
    borderColor: string;
    boxShadow: string;
}

export interface Point {
    left: number;
    top: number;
}

export function lookOf(el: Element): Look {
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    return {
        left: r.left,
        top: r.top,
        width: r.width,
        height: r.height,
        padding: `${s.paddingTop} ${s.paddingRight} ${s.paddingBottom} ${s.paddingLeft}`,
        borderRadius: s.borderTopLeftRadius,
        backgroundColor: s.backgroundColor,
        borderColor: s.borderTopColor,
        boxShadow: s.boxShadow,
    };
}

/**
 * The keyframe that draws an element as `look` while its own layout box is at `origin`: the translation
 * is the difference, the rest is the look itself.
 */
export function boxFrame(look: Look, origin: Point): Keyframe {
    return {
        transform: `translate(${look.left - origin.left}px, ${look.top - origin.top}px)`,
        width: `${look.width}px`,
        height: `${look.height}px`,
        padding: look.padding,
        borderRadius: look.borderRadius,
        backgroundColor: look.backgroundColor,
        borderColor: look.borderColor,
        boxShadow: look.boxShadow,
    };
}

/** `p` moved by `to - from`: where a box offset from one reference sits against another */
export function moved(p: Point, from: Point, to: Point): Point {
    return {left: p.left - from.left + to.left, top: p.top - from.top + to.top};
}

/** the panel's content: invisible while the box is still mostly the button, there towards the end */
export const CONTENT_IN: Keyframe[] = [
    {opacity: 0, offset: 0},
    {opacity: 0, offset: 0.4},
    {opacity: 1, offset: 1},
];
export const CONTENT_OUT: Keyframe[] = [
    {opacity: 1, offset: 0},
    {opacity: 0, offset: 0.5},
    {opacity: 0, offset: 1},
];
/** the button's label on the growing box: gone early; on the shrinking box: back at the end */
export const LABEL_OUT: Keyframe[] = [
    {opacity: 1, offset: 0},
    {opacity: 0, offset: 0.35},
    {opacity: 0, offset: 1},
];
export const LABEL_IN: Keyframe[] = [
    {opacity: 0, offset: 0},
    {opacity: 0, offset: 0.65},
    {opacity: 1, offset: 1},
];
