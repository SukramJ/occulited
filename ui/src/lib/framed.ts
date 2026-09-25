/*
 * B-132: the shell must never render itself inside its own addon frame.
 *
 * Found on the Charly (2026-09-13): opening Homematic Manager from the addon menu showed the whole
 * openccu-lite shell - top bar and all - inside the addon's frame, nested several levels deep (three
 * top bars stacked in the maintainer's screenshot). The trigger is on the addon's side (its session
 * check failed and it sent the frame to the box's own page), but a frame that gets `/` back must not
 * grow a second shell inside the first: every level of it fetches the whole UI again, answers theme
 * requests, opens its own session, and the reader is left with a page that looks broken and cannot say
 * why.
 *
 * So the shell asks, before it mounts, whether it is being framed *by itself*, and shows a notice in
 * the frame instead (lib/FramedNotice.svelte). It also tells the shell above it, which names the addon
 * in the browser console - where such a loop is diagnosed.
 */

/** the message a framed shell sends its parent; the parent logs it (App.svelte) */
export const FRAMED_BACK = 'openccu-lite:framed-back';

interface Framed {
    self: unknown;
    top: unknown;
    parent: {location: {origin: string}; document: {querySelector(selector: string): unknown}};
    location: {origin: string};
}

/**
 * Whether this page is running inside a frame of the shell's own page.
 *
 * Three things have to hold, and each of them alone would be wrong to act on: the page is framed at
 * all; the parent is the same origin, which is the only case in which a frame of this shell can be
 * meant (a cross-origin parent throws on `location.origin`, and the page carries on as usual); and
 * the parent's document *is* a shell - it has the shell's root element. Without the last test any
 * same-origin page that embeds the shell on purpose would be refused.
 */
export function framedByShell(win: Framed = window as unknown as Framed): boolean {
    try {
        if (win.self === win.top) return false;
        const parent = win.parent;
        if (!parent || parent === (win.self as unknown)) return false;
        if (parent.location.origin !== win.location.origin) return false;
        return parent.document.querySelector('.ol-shell') !== null;
    } catch {
        // a cross-origin parent: reading its location or its document throws. Not our own shell.
        return false;
    }
}
