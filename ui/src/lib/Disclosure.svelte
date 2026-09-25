<script lang="ts">
    /*
     * A block of inputs for an occasional action is not on the page until it is asked for
     * (maintainer, 2026-09-07: "such complex stuff should only appear when clicking on an 'add
     * gateway' button and not be visible from start"). The page opens showing what *is* configured
     * and offers to add another; the form appears on demand and can be cancelled.
     *
     * One control for all of them, so the pages do not each invent one. Nothing here is new visual
     * language (D-21): the trigger is `hmm-button`, the panel is `ol-disclosure` in app.css - the
     * card's surface, radius and shadow (task 98) - and the form inside it is the `ol-form` every
     * page already uses.
     *
     * `label` renders the button that opens it. Leave it out when the trigger belongs somewhere
     * else - the Radio page opens the key-change panel from a button in the gateway's own row, so
     * that the panel is about *that* gateway - and open the panel by setting `open` from there.
     *
     * Task 51: `help` is the explanation of the panel as a whole, behind a ? after its heading
     * (a lib/Help.svelte), the way a page section has one after its h2.
     *
     * Task 98, as the maintainer refined it: the button grows into the panel and the panel shrinks
     * back into the button (lib/morph.ts). The panel is put in its real place and drawn from the
     * button's box - its corner, size, padding, radius, surface and shadow - to its own; the label
     * fades out early, the heading and the form fade in towards the end. The slot around both is what
     * the page below rests on: its height and margins move in the same motion, so nothing below jumps,
     * neither at the start nor at the end. Closing runs it backwards: the panel leaves the flow, the
     * button takes its place unseen, and the panel shrinks onto it before the button shows. A panel
     * opened from elsewhere (the gateway row's *Change key*) grows out of that trigger and shrinks
     * back into it; without a trigger to measure it fades in while the slot grows. Under reduced
     * motion there is no motion at all.
     *
     * The focus: opening moves it to the panel's first field (the panel itself while it has none -
     * the USB list is still loading); closing gives it back to the button that opened the panel -
     * this one, or the caller's own - unless the reader has since moved on to something else.
     */
    import type {Snippet} from 'svelte';
    import {tick, untrack} from 'svelte';
    import {t} from './i18n.svelte';
    import Help from './Help.svelte';
    import {reducedMotion} from './reveal';
    import {CONTENT_IN, CONTENT_OUT, LABEL_IN, LABEL_OUT, MORPH_CLOSE_MS, MORPH_EASING, MORPH_OPEN_MS, boxFrame, lookOf, moved, type Look} from './morph';

    interface Props {
        /** the button that opens the panel, e.g. "Add gateway"; omitted when the caller opens it */
        label?: string;
        /** the open panel's heading; defaults to the button's label */
        title?: string;
        open?: boolean;
        disabled?: boolean;
        /** what the panel is about, behind a ? after the heading (task 51) */
        help?: Snippet;
        /** a view without an action to drop (task 249, the maintainer): its button says Close, not Cancel */
        readOnly?: boolean;
        children: Snippet;
    }
    let {label, title, open = $bindable(false), disabled = false, help, readOnly = false, children}: Props = $props();
    const uid = $props.id();

    type Phase = 'closed' | 'opening' | 'open' | 'closing';
    let phase = $state<Phase>(untrack(() => open) ? 'open' : 'closed');
    /** the trigger's label, drawn on the panel while it still is - or is becoming again - the button */
    let ghost = $state('');
    let slot = $state<HTMLElement | null>(null);
    let panel = $state<HTMLElement | null>(null);
    let body = $state<HTMLElement | null>(null);
    let trigger = $state<HTMLButtonElement | null>(null);
    /** the focused element outside the panel when a caller opened it: its trigger, if it has one */
    let opener: HTMLElement | null = null;
    let wanted = untrack(() => open);
    /** counts the motions: one that finds a newer one started after its await gives up */
    let generation = 0;
    let running: {animations: Animation[]; settle: () => void} | null = null;

    /** the first field a reader fills in, or the panel itself while there is none */
    function firstField(el: HTMLElement): HTMLElement {
        const fields = el.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>('input:not([type="hidden"]), select, textarea');
        return Array.from(fields).find((f) => !f.disabled && f.getClientRects().length > 0) ?? el;
    }
    /** an element other than the page itself holds the focus, and it is not inside the panel */
    function elsewhere(active: Element | null): active is HTMLElement {
        return active instanceof HTMLElement && active !== document.body && !panel?.contains(active);
    }
    function measurable(el: Element | null | undefined): el is HTMLElement {
        return el instanceof HTMLElement && el.isConnected && el.getClientRects().length > 0;
    }
    /** reads the slot as it is without its open margins - where the page below starts or ends */
    function withoutMargins<T>(el: HTMLElement, read: () => T): T {
        el.style.marginTop = el.style.marginBottom = '0px';
        try {
            return read();
        } finally {
            el.style.marginTop = el.style.marginBottom = '';
        }
    }
    function run(el: Element, frames: Keyframe[], duration: number): Animation {
        return el.animate(frames, {duration, easing: MORPH_EASING});
    }
    function play(animations: Animation[], settle: () => void) {
        const r = {animations, settle};
        running = r;
        Promise.all(animations.map((a) => a.finished)).then(
            () => {
                if (running !== r) return;
                running = null;
                settle();
            },
            () => {
                /* cancelled: whoever cancelled it settles */
            },
        );
    }
    /** a motion still running ends where it was going, at once */
    function stop() {
        const r = running;
        running = null;
        if (!r) return;
        for (const a of r.animations) a.cancel();
        r.settle();
    }
    function unstyle() {
        slot?.style.removeProperty('overflow');
        panel?.style.removeProperty('overflow');
        panel?.style.removeProperty('width');
        body?.style.removeProperty('width');
    }

    function settleOpen() {
        unstyle();
        ghost = '';
        phase = 'open';
    }
    function settleClosed(focus: HTMLElement | null) {
        unstyle();
        ghost = '';
        phase = 'closed';
        if (focus)
            void tick().then(() => {
                if (!open && focus.isConnected) focus.focus({preventScroll: true});
            });
    }

    async function grow() {
        const mine = ++generation;
        stop();
        const active = document.activeElement;
        opener = !label && elsewhere(active) ? active : null;
        const from = label ? trigger : opener;
        const start: Look | null = measurable(from) ? lookOf(from) : null;
        const closedHeight = slot?.getBoundingClientRect().height ?? 0;
        ghost = start ? (label ?? from?.textContent?.trim() ?? '') : '';
        phase = 'opening';
        await tick();
        if (mine !== generation) return;
        if (panel) firstField(panel).focus({preventScroll: true});
        if (!slot || !panel || !body || reducedMotion() || typeof panel.animate !== 'function') {
            settleOpen();
            return;
        }
        const s = slot;
        const p = panel;
        const closedSlot = withoutMargins(s, () => s.getBoundingClientRect());
        const openSlot = s.getBoundingClientRect();
        const {marginTop, marginBottom} = getComputedStyle(s);
        const end = lookOf(p);
        const animations = [
            run(s, [{height: `${closedHeight}px`, marginTop: '0px', marginBottom: '0px'}, {height: `${openSlot.height}px`, marginTop, marginBottom}], MORPH_OPEN_MS),
        ];
        if (start) {
            // the content keeps its final width while the box is narrower, so no line reflows
            body.style.width = `${body.getBoundingClientRect().width}px`;
            p.style.overflow = 'clip';
            animations.push(run(p, [boxFrame(start, moved(end, openSlot, closedSlot)), boxFrame(end, end)], MORPH_OPEN_MS));
            animations.push(run(body, CONTENT_IN, MORPH_OPEN_MS));
            const g = p.querySelector('.ol-disclosure-ghost');
            if (g) animations.push(run(g, LABEL_OUT, MORPH_OPEN_MS));
        } else {
            s.style.overflow = 'clip';
            animations.push(run(p, [{opacity: 0}, {opacity: 1}], MORPH_OPEN_MS));
        }
        play(animations, settleOpen);
    }

    async function shrink() {
        const mine = ++generation;
        stop();
        if (!slot || !panel || !body) {
            settleClosed(null);
            return;
        }
        // read before anything moves: a closing panel turns inert, and the browser lets go of a focus in it
        const giveBack = !elsewhere(document.activeElement);
        const s = slot;
        const openSlot = s.getBoundingClientRect();
        const start = lookOf(panel);
        const {marginTop, marginBottom} = getComputedStyle(s);
        const bodyWidth = body.getBoundingClientRect().width;
        const back = label ? null : opener;
        opener = null;
        ghost = label ?? (measurable(back) ? (back.textContent?.trim() ?? '') : '');
        phase = 'closing';
        await tick();
        if (mine !== generation) return;
        const to = label ? trigger : back;
        const focus = giveBack ? to : null;
        if (!panel || !body || reducedMotion() || typeof s.animate !== 'function') {
            settleClosed(focus);
            return;
        }
        const p = panel;
        let end: Look | null = null;
        const closedSlot = withoutMargins(s, () => {
            if (measurable(to)) end = lookOf(to);
            return s.getBoundingClientRect();
        });
        // out of the flow now (.ol-disclosure-leaving): held at its width, the button in its place unseen
        p.style.width = `${start.width}px`;
        body.style.width = `${bodyWidth}px`;
        const animations = [
            run(s, [{height: `${openSlot.height}px`, marginTop, marginBottom}, {height: `${closedSlot.height}px`, marginTop: '0px', marginBottom: '0px'}], MORPH_CLOSE_MS),
        ];
        if (end) {
            p.style.overflow = 'clip';
            animations.push(run(p, [boxFrame(start, openSlot), boxFrame(end, closedSlot)], MORPH_CLOSE_MS));
            animations.push(run(body, CONTENT_OUT, MORPH_CLOSE_MS));
            const g = p.querySelector('.ol-disclosure-ghost');
            if (g) animations.push(run(g, LABEL_IN, MORPH_CLOSE_MS));
        } else {
            ghost = '';
            s.style.overflow = 'clip';
            animations.push(run(p, [{opacity: 1}, {opacity: 0}], MORPH_CLOSE_MS));
        }
        play(animations, () => settleClosed(focus));
    }

    $effect(() => {
        const want = open;
        untrack(() => {
            if (want === wanted) return;
            wanted = want;
            void (want ? grow() : shrink());
        });
    });
    // a page left in the middle of a motion: nothing settles into a component that is gone
    $effect(() => () => {
        generation++;
        const r = running;
        running = null;
        for (const a of r?.animations ?? []) a.cancel();
    });
</script>

{#if label || phase !== 'closed'}
    <div class="ol-disclosure-slot" class:ol-disclosure-open={phase !== 'closed'} bind:this={slot}>
        {#if phase !== 'closed'}
            <div
                class="ol-disclosure"
                class:ol-disclosure-leaving={phase === 'closing'}
                role="group"
                aria-labelledby={`${uid}-title`}
                tabindex="-1"
                inert={phase === 'closing'}
                bind:this={panel}
            >
                {#if ghost}<span class="ol-disclosure-ghost" aria-hidden="true">{ghost}</span>{/if}
                <div class="ol-disclosure-body" bind:this={body}>
                    <div class="ol-disclosure-head">
                        <h3 id={`${uid}-title`}>{title ?? label ?? ''}{#if help}<Help>{@render help()}</Help>{/if}</h3>
                        <button class="hmm-button" type="button" onclick={() => (open = false)}>{readOnly ? t('Close') : t('Cancel')}</button>
                    </div>
                    {@render children()}
                </div>
            </div>
        {/if}
        {#if label && (phase === 'closed' || phase === 'closing')}
            <div class="ol-disclosure-trigger" class:ol-disclosure-waiting={phase === 'closing'}>
                <div class="ol-actions"><button class="hmm-button" type="button" {disabled} bind:this={trigger} onclick={() => (open = true)}>{label}</button></div>
            </div>
        {/if}
    </div>
{/if}
