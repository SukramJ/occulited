<script lang="ts">
    /*
     * The popup panel both menus of the tab bar open (task 139, the maintainer 2026-09-22: the
     * Addons popup gets the System popup's look and motion, and there is one implementation of
     * it, not a copy). Extracted from lib/SystemMenu.svelte (task 57, D-68; the motion task 98's
     * FLIP, lib/morph.ts): on a desktop a popover under the anchor, grown out of the anchor's
     * rectangle - drawn at its corner, size, padding, radius, surface and shadow first, then its
     * own - about 200 ms, the content fading in towards the end, and gone at once when it closes
     * (task 210: only the opening moves); on a phone (below SHEET_MAX_WIDTH) an iOS bottom sheet with a drag handle over a
     * dimmed page, slid up, dragged down to dismiss. Under reduced motion nothing moves.
     *
     * The panel owns: the phases (data-phase: closed, opening, open, closing), the placement under
     * the anchor and its following of a scroll or a resize, the backdrop, the handle's swipe, the
     * click outside (onrequestclose(false)), Escape when the content did not take it
     * (onrequestclose(true)), the focus on open (the first text field when `keyboard`, else the
     * panel) and back to the anchor on a close that asks for it. The content is the caller's
     * snippet: the System menu's filter and rows, the Addons menu's rows and its foot.
     */
    import {tick, untrack, type Snippet} from 'svelte';
    import {reducedMotion} from './reveal';
    import {CONTENT_IN, MORPH_EASING, boxFrame, lookOf} from './morph';

    const OPEN_MS = 200;
    const CLOSE_MS = 160;
    /** below this width the menu is a bottom sheet: the shell's own phone breakpoint (app.css) */
    export const SHEET_MAX_WIDTH = 700;
    /** how far a swipe must go down to close the sheet */
    const SWIPE_PX = 80;

    type Phase = 'closed' | 'opening' | 'open' | 'closing';
    interface Props {
        open: boolean;
        anchor: HTMLElement | null;
        /** the nav landmark's name */
        label: string;
        /** the panel's extra class (its width rule) */
        class?: string;
        /** the menu was opened by keyboard: the first text field gets the focus, else the panel */
        keyboard?: boolean;
        /** whether a close gives the focus back to the anchor (not after a row was chosen) */
        giveBack?: boolean;
        /** the panel wants to close: a click outside, a swipe, a resize across the breakpoint (false), Escape (true) */
        onrequestclose: (giveBack: boolean) => void;
        /** the content's own keyboard, before the panel's Escape */
        onkeydown?: (ev: KeyboardEvent) => void;
        /** the panel element, for the content's focus handling */
        panel?: HTMLElement | null;
        /** the current phase, for the caller */
        phase?: Phase;
        /** whether the panel is a sheet now */
        sheet?: boolean;
        children: Snippet;
    }
    let {open, anchor, label, class: extra = '', keyboard = false, giveBack = true, onrequestclose, onkeydown, panel = $bindable(null), phase = $bindable('closed'), sheet = $bindable(false), children}: Props = $props();

    let body = $state<HTMLElement | null>(null);
    let backdrop = $state<HTMLElement | null>(null);
    /** the popover's place under its anchor */
    let pos = $state({left: 0, top: 0});
    /** the sheet's offset while a finger drags it */
    let drag = $state<number | null>(null);
    let running: Animation[] = [];
    let generation = 0;
    let wanted = false;
    let placedAnchor: HTMLElement | null = null;

    // ---- placement ---------------------------------------------------------------------------
    function isSheet(): boolean {
        return typeof matchMedia === 'function' && matchMedia(`(max-width: ${SHEET_MAX_WIDTH}px)`).matches;
    }
    /** under the anchor, its left edge, kept 8 px inside the window */
    function place() {
        if (sheet || !placedAnchor || !panel) return;
        const a = placedAnchor.getBoundingClientRect();
        const w = panel.getBoundingClientRect().width;
        const left = Math.max(8, Math.min(a.left, window.innerWidth - w - 8));
        pos = {left, top: a.bottom + 4};
    }

    // ---- the motion --------------------------------------------------------------------------
    function stop() {
        for (const a of running) a.cancel();
        running = [];
        release();
    }
    /*
     * B-159: while the box morphs, the content keeps its own size and the clipping panel reveals it.
     * The content is a flex column whose list scrolls (min-height: 0, overflow-y: auto); in a panel
     * still short from the anchor's height it was squeezed and showed its own scrollbar until the
     * box was full size - a flash on every open and close. Held at its measured size (and out of
     * the flex shrink), nothing inside reflows or scrolls; released when the motion settles.
     */
    function hold(b: HTMLElement) {
        const r = b.getBoundingClientRect();
        b.style.flex = '0 0 auto';
        b.style.width = `${r.width}px`;
        b.style.height = `${r.height}px`;
    }
    function release() {
        for (const prop of ['flex', 'width', 'height']) body?.style.removeProperty(prop);
    }
    function run(el: Element, frames: Keyframe[], duration: number): Animation {
        const a = el.animate(frames, {duration, easing: MORPH_EASING});
        running.push(a);
        return a;
    }
    async function settled(): Promise<void> {
        const mine = running;
        await Promise.all(mine.map((a) => a.finished)).catch(() => {});
        if (running === mine) running = [];
    }
    function focusOnOpen() {
        if (!panel) return;
        const input = panel.querySelector<HTMLInputElement>('input:not([type="checkbox"])');
        if (keyboard && input) input.focus({preventScroll: true});
        else panel.focus({preventScroll: true});
    }

    async function grow() {
        const mine = ++generation;
        stop();
        placedAnchor = anchor;
        sheet = isSheet();
        drag = null;
        phase = 'opening';
        await tick();
        if (mine !== generation) return;
        place();
        await tick();
        if (mine !== generation) return;
        focusOnOpen();
        if (!panel || !body || reducedMotion() || typeof panel.animate !== 'function') {
            phase = 'open';
            return;
        }
        const p = panel;
        if (sheet) {
            run(p, [{transform: 'translateY(100%)'}, {transform: 'translateY(0)'}], OPEN_MS);
            if (backdrop) run(backdrop, [{opacity: 0}, {opacity: 1}], OPEN_MS);
        } else if (placedAnchor && placedAnchor.isConnected) {
            const end = lookOf(p);
            // task 210: the box keeps the panel's own border colour all the way - drawn in the tab's,
            // it showed a bright (dark mode) or dark (light mode) frame while it grew
            const start = {...lookOf(placedAnchor), borderColor: end.borderColor};
            hold(body);
            p.style.overflow = 'clip';
            // task 210: a panel whose width rule has a floor (the Addons menu's min-width: 300px)
            // grew only downwards, the width clamped to that floor from the first frame; the floor
            // is lifted while the box morphs, so both menus grow out of their tab in both axes
            p.style.minWidth = '0';
            p.style.maxWidth = 'none';
            run(p, [boxFrame(start, end), boxFrame(end, end)], OPEN_MS);
            run(body, CONTENT_IN, OPEN_MS);
        } else {
            run(p, [{opacity: 0}, {opacity: 1}], OPEN_MS);
        }
        await settled();
        if (mine !== generation) return;
        for (const prop of ['overflow', 'min-width', 'max-width']) p.style.removeProperty(prop);
        release();
        phase = 'open';
    }

    async function shrink(giveFocusBack: boolean) {
        const mine = ++generation;
        stop();
        if (panel) for (const prop of ['overflow', 'min-width', 'max-width']) panel.style.removeProperty(prop);
        const back = giveFocusBack && placedAnchor && placedAnchor.isConnected ? placedAnchor : null;
        // task 210 (the maintainer, 2026-09-23: "animation only on opening"): the popover closes at
        // once; only the phone's sheet still slides away, as a sheet pulled down does
        if (!panel || !body || !sheet || reducedMotion() || typeof panel.animate !== 'function') {
            phase = 'closed';
            back?.focus({preventScroll: true});
            return;
        }
        phase = 'closing';
        const p = panel;
        const from = drag ?? 0;
        run(p, [{transform: `translateY(${from}px)`}, {transform: 'translateY(100%)'}], CLOSE_MS);
        if (backdrop) run(backdrop, [{opacity: 1}, {opacity: 0}], CLOSE_MS);
        await settled();
        if (mine !== generation) return;
        release();
        phase = 'closed';
        drag = null;
        back?.focus({preventScroll: true});
    }

    $effect(() => {
        const want = open;
        untrack(() => {
            if (want === wanted) return;
            wanted = want;
            void (want ? grow() : shrink(giveBack));
        });
    });

    // a menu re-anchored while open (the System tab, then a title): it moves, without closing in between
    $effect(() => {
        const a = anchor;
        untrack(() => {
            if (!open || a === placedAnchor) return;
            placedAnchor = a;
            place();
        });
    });

    // while open: a click outside closes; the popover follows its anchor through a scroll or a
    // resize (the anchor is in the sticky bar, or in a page that scrolls under it)
    $effect(() => {
        if (!open) return;
        const onDown = (ev: MouseEvent) => {
            const target = ev.target as Node;
            if (panel?.contains(target) || placedAnchor?.contains(target)) return;
            onrequestclose(false);
        };
        const onScroll = () => place();
        const onResize = () => {
            if (isSheet() !== sheet) onrequestclose(false);
            else place();
        };
        document.addEventListener('mousedown', onDown);
        document.addEventListener('scroll', onScroll, true);
        window.addEventListener('resize', onResize);
        return () => {
            document.removeEventListener('mousedown', onDown);
            document.removeEventListener('scroll', onScroll, true);
            window.removeEventListener('resize', onResize);
        };
    });
    // a page left with the menu open: nothing settles into a component that is gone
    $effect(() => () => {
        generation++;
        stop();
    });

    function onPanelKey(ev: KeyboardEvent) {
        onkeydown?.(ev);
        if (ev.defaultPrevented) return;
        if (ev.key === 'Escape') {
            ev.preventDefault();
            onrequestclose(true);
        }
    }

    // ---- the sheet's swipe -------------------------------------------------------------------
    let dragStart: {y: number; id: number} | null = null;
    function onPointerDown(ev: PointerEvent) {
        if (!sheet || phase !== 'open') return;
        dragStart = {y: ev.clientY, id: ev.pointerId};
        (ev.currentTarget as HTMLElement).setPointerCapture(ev.pointerId);
    }
    function onPointerMove(ev: PointerEvent) {
        if (!dragStart || ev.pointerId !== dragStart.id) return;
        drag = Math.max(0, ev.clientY - dragStart.y);
    }
    function onPointerUp(ev: PointerEvent) {
        if (!dragStart || ev.pointerId !== dragStart.id) return;
        dragStart = null;
        const d = drag ?? 0;
        if (d > SWIPE_PX) {
            onrequestclose(false);
            return;
        }
        // not far enough: back into place
        if (panel && d > 0 && !reducedMotion() && typeof panel.animate === 'function') {
            const a = panel.animate([{transform: `translateY(${d}px)`}, {transform: 'translateY(0)'}], {duration: CLOSE_MS, easing: MORPH_EASING});
            void a.finished.catch(() => {});
        }
        drag = null;
    }
</script>

{#if phase !== 'closed'}
    {#if sheet}
        <!-- the dimmed page; a tap on it closes the sheet (the mousedown listener above) -->
        <div class="ol-sysbackdrop" class:ol-sysbackdrop-leaving={phase === 'closing'} aria-hidden="true" bind:this={backdrop}></div>
    {/if}
    <!-- the keydown is the menu's own keyboard - the arrow keys over its rows, Escape - on the container,
         as the menu pattern has it; the rows themselves are the interactive parts -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <nav
        class="ol-sysmenu {extra}"
        class:ol-syssheet={sheet}
        class:ol-syspop={!sheet}
        aria-label={label}
        tabindex="-1"
        inert={phase === 'closing'}
        style={sheet ? (drag !== null ? `transform: translateY(${drag}px)` : '') : `left: ${pos.left}px; top: ${pos.top}px`}
        data-phase={phase}
        bind:this={panel}
        onkeydown={onPanelKey}
    >
        <div class="ol-sysbody" bind:this={body}>
            {#if sheet}
                <!-- the drag handle: the sheet follows a finger on it, and lets go when pulled down far enough -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="ol-syshandle-row" onpointerdown={onPointerDown} onpointermove={onPointerMove} onpointerup={onPointerUp} onpointercancel={onPointerUp}>
                    <div class="ol-syshandle" aria-hidden="true"></div>
                </div>
            {/if}
            {@render children()}
        </div>
    </nav>
{/if}

<style>
    /* the panel: the card's surface, radius and shadow (task 98); above the sticky bar it grows out of */
    .ol-sysmenu {
        position: fixed; z-index: 46; display: flex; flex-direction: column;
        padding: 8px; border: 1px solid var(--hmm-border-muted); border-radius: var(--hmm-radius-card);
        background: var(--hmm-card-bg); box-shadow: var(--hmm-shadow-card-hover); color: var(--hmm-fg);
    }
    .ol-sysmenu:focus { outline: none; }
    .ol-syspop { width: min(300px, calc(100vw - 16px)); max-height: calc(100vh - 16px); }
    .ol-sysbody { display: flex; flex-direction: column; min-height: 0; flex: 1 1 auto; }
    /* the sheet: the window's bottom edge, rounded at the top, the handle above the content */
    .ol-syssheet {
        left: 0; right: 0; bottom: 0; width: auto; max-height: 80dvh;
        border-radius: 16px 16px 0 0; border-bottom: 0; padding: 0 12px calc(12px + env(safe-area-inset-bottom, 0px));
    }
    .ol-syshandle-row { flex: 0 0 auto; padding: 8px 0 10px; touch-action: none; cursor: grab; }
    .ol-syshandle { width: 36px; height: 5px; margin: 0 auto; border-radius: 3px; background: var(--hmm-border-strong); }
    .ol-sysbackdrop { position: fixed; inset: 0; z-index: 46; background: var(--hmm-backdrop); }
    .ol-sysbackdrop-leaving { pointer-events: none; }
</style>
