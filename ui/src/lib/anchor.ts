/*
 * Task 132: a section of a page that renders after its data - the Users page's login settings and
 * API tokens, the Certificate page's HTTPS switches - is scrolled to when the URL's anchor names it.
 * The browser's own jump to the anchor happens at the load, before the section exists; the section
 * calls this once it is there. `/system/security#https`, a bookmark of the page before, lands on
 * the Certificate page's HTTPS section this way (lib/router.svelte.ts rewrites the path).
 */

/** whether the URL's anchor is `#<id>` (case as written: ids are lower case) */
export function anchoredAt(id: string, hash: string): boolean {
    return hash === `#${id}`;
}

/**
 * Scrolls the element `id` into view after the current render, when the URL's anchor names it - and
 * once more a moment later: a section above it may still be loading (the Remote access page's SSH
 * sessions and keys under the API tokens, 2026-09-19) and pushes it out of view as it grows.
 */
export function scrollToAnchor(id: string): void {
    if (typeof location === 'undefined' || !anchoredAt(id, location.hash)) return;
    const show = () => {
        if (anchoredAt(id, location.hash)) document.getElementById(id)?.scrollIntoView({block: 'start'});
    };
    setTimeout(show, 0);
    setTimeout(show, 400);
}
