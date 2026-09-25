<script lang="ts">
    /*
     * Task 51: the help behind a ?. The maintainer's word: "we have a lot of help texts in our ui,
     * beside or beneath titles and so on. don't show them, hide them in a popup, put ?-icons
     * instead that show the popup on hover or click, make good ux". One component, so no page
     * invents its own.
     *
     * Two forms:
     *
     *   <Help>{explanation}</Help>
     *       a small round ? - after a section's heading, after an input's label, in a table's
     *       header cell;
     *
     *   <Help class="ol-badge warn">{#snippet trigger()}{badge}{/snippet}{why}</Help>
     *
     *   (the examples name variables, not t() calls: scripts/i18n-check.py reads comments too, and
     *   took the two example texts for untranslated keys)
     *       the badge itself is the trigger, marked by a dotted underline - what used to be a
     *       `title` on a badge or a state, which needed a mouse and a pause and could not be
     *       reached on a phone at all.
     *
     * The rules of when it opens and closes are lib/help.ts (tested there): hover opens after
     * 300 ms and only on a device that can hover, the popup survives the pointer travelling into
     * it, a click or tap pins it, a second click, Escape or a click outside closes it, and one is
     * open at a time. Escape gives the focus back to the trigger when the focus was there or in
     * the popup; a keyboard user who tabs on past the popup closes it.
     *
     * The popup is `position: fixed` and placed by lib/help.ts `place()`: that way no table that
     * scrolls sideways (`.ol-scroll`) and no menu with `overflow: auto` clips it, and it is kept
     * inside the viewport on a 360 px phone. It stays a DOM child of the trigger's wrapper rather
     * than moving to <body>: the tab order then runs from the ? into a link in the popup and on,
     * and an outside click is simply "not inside the wrapper". The price is that it inherits the
     * text styles of where it sits - an h2 is uppercase, 12 px and muted - so the popup resets
     * them.
     *
     * Inside a <label> two things matter. The label forwards a click on its plain text to its
     * control, which would tick a checkbox when the reader clicks a sentence in the popup; a click
     * in the popup that is not on a link or a control is therefore cancelled. And a label without
     * `for` names its *first* labelable descendant, which a ? placed before the input would be:
     * such a label needs `for`/`id` (the pages do that).
     */
    import type {Snippet} from 'svelte';
    import {onDestroy} from 'svelte';
    import Icon from './Icon.svelte';
    import {t} from './i18n.svelte';
    import {HelpState, place} from './help';

    interface Props {
        /** what the popup says: text, paragraphs, a list, links */
        children: Snippet;
        /** the badge form: this is the trigger instead of the ?, underlined dotted */
        trigger?: Snippet;
        /** classes of the trigger in the badge form, e.g. `ol-badge warn` */
        class?: string;
        /** the ?'s accessible name; *Help* by default. The badge form is named by its own text. */
        label?: string;
    }
    let {children, trigger, class: cls = '', label}: Props = $props();

    let open = $state(false);
    let pinned = $state(false);
    let wrap: HTMLSpanElement | undefined = $state();
    let button: HTMLButtonElement | undefined = $state();
    const ctl = new HelpState((o, p) => {
        open = o;
        pinned = p;
    });
    onDestroy(() => ctl.dispose());
    const id = `ol-help-${Math.random().toString(36).slice(2, 8)}`;

    // hover is for a mouse on a device that has one; a touch screen reports a tap as a pointer
    // entering too, and that must not start the 300 ms timer before the click that pins it
    const canHover = (e: PointerEvent) => e.pointerType === 'mouse' && matchMedia('(hover: hover) and (pointer: fine)').matches;
    const enter = (e: PointerEvent) => {
        if (canHover(e)) ctl.hoverEnter();
    };
    const leave = (e: PointerEvent) => {
        if (canHover(e)) ctl.hoverLeave();
    };

    // while open: Escape, a press outside, the focus leaving. Registered on the document in the
    // capture phase, so that the Escape which closes a popup inside the shell's dialog does not
    // also close the dialog (ConfirmDialog listens on the window).
    $effect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => {
            if (e.key !== 'Escape') return;
            e.preventDefault();
            e.stopPropagation();
            const focusInside = !!wrap && wrap.contains(document.activeElement);
            const wasPinned = pinned;
            ctl.close();
            if (focusInside || wasPinned) button?.focus();
        };
        const onDown = (e: PointerEvent) => {
            if (wrap && !wrap.contains(e.target as Node)) ctl.close();
        };
        const onFocusOut = (e: FocusEvent) => {
            // only a focus that went somewhere else: a click on the popup's plain text moves the
            // focus to nothing, and that keeps it open
            const to = e.relatedTarget as Node | null;
            if (to && wrap && !wrap.contains(to)) ctl.close();
        };
        document.addEventListener('keydown', onKey, true);
        document.addEventListener('pointerdown', onDown, true);
        wrap?.addEventListener('focusout', onFocusOut);
        return () => {
            document.removeEventListener('keydown', onKey, true);
            document.removeEventListener('pointerdown', onDown, true);
            wrap?.removeEventListener('focusout', onFocusOut);
        };
    });

    // placed when it mounts and again on every scroll (of the page or of a box around the ?) and
    // resize, since a fixed popup does not move with its trigger by itself
    let side = $state<'below' | 'above'>('below');
    // What the reader sees, in the coordinates a fixed element is placed in: the visual viewport.
    // On a phone that zoomed out a page wider than its screen (the Services table) and panned it,
    // that is not 0..clientWidth - a popup in the timer dialog landed at x -304 of the screen
    // (help.ts `place`). Without the API, the window.
    function visibleArea() {
        const vv = window.visualViewport;
        return vv ? {left: vv.offsetLeft, top: vv.offsetTop, width: vv.width, height: vv.height} : {left: 0, top: 0, width: document.documentElement.clientWidth, height: window.innerHeight};
    }
    function positioned(node: HTMLElement) {
        const update = () => {
            if (!button) return;
            const p = place(button.getBoundingClientRect(), {width: node.offsetWidth, height: node.offsetHeight}, visibleArea());
            node.style.top = `${p.top}px`;
            node.style.left = `${p.left}px`;
            node.style.setProperty('--ol-help-arrow', `${p.arrow}px`);
            side = p.side;
        };
        update();
        window.addEventListener('scroll', update, true);
        window.addEventListener('resize', update);
        // a pan or a pinch moves the visible part without scrolling anything
        window.visualViewport?.addEventListener('scroll', update);
        window.visualViewport?.addEventListener('resize', update);
        return {
            destroy() {
                window.removeEventListener('scroll', update, true);
                window.removeEventListener('resize', update);
                window.visualViewport?.removeEventListener('scroll', update);
                window.visualViewport?.removeEventListener('resize', update);
            },
        };
    }

    // the label rule of the header comment: plain text in the popup does not activate a label
    function guardLabel(e: MouseEvent) {
        if (!(e.target as Element).closest('a, button, input, select, textarea, summary')) e.preventDefault();
    }
</script>

<span class="ol-help" bind:this={wrap}>
    <button
        type="button"
        bind:this={button}
        class={trigger ? `ol-help-badge ${cls}` : 'ol-help-q'}
        aria-label={trigger ? undefined : (label ?? t('Help'))}
        aria-expanded={open}
        aria-describedby={open ? id : undefined}
        onclick={() => ctl.click()}
        onpointerenter={enter}
        onpointerleave={leave}
    >
        {#if trigger}{@render trigger()}{:else}<Icon name="help" size={16} />{/if}
    </button>
    {#if open}
        <span class="ol-help-pop" {id} role="tooltip" data-side={side} use:positioned>
            <span class="ol-help-arrow" aria-hidden="true"></span>
            <!-- the click handler only keeps a surrounding label from acting on a click in the
                 text; the keyboard reaches the popup's links by Tab and closes it with Escape -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span class="ol-help-body" onpointerenter={enter} onpointerleave={leave} onclick={guardLabel}>{@render children()}</span>
        </span>
    {/if}
</span>

<style>
    .ol-help {
        display: inline;
    }
    /* the ?: muted, a 16 px icon, the accent while open or hovered */
    .ol-help-q {
        position: relative;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        vertical-align: -3px;
        margin: 0 0 0 4px;
        padding: 0;
        border: 0;
        border-radius: 50%;
        background: none;
        color: var(--hmm-fg-muted);
        cursor: pointer;
        line-height: 1;
    }
    .ol-help-q:hover,
    .ol-help-q[aria-expanded='true'] {
        color: var(--hmm-accent);
    }
    .ol-help-q:focus-visible {
        outline-offset: 1px;
    }
    /* a finger needs about 32 px: the hit area grows around the icon, the layout does not */
    @media (pointer: coarse) {
        .ol-help-q::after {
            content: '';
            position: absolute;
            inset: -8px;
        }
    }
    /* the badge form: the page's own classes paint it (`ol-badge warn`, `ol-warn`); app.css already
       gives a button the surrounding font and colour. Without a badge class the button's face is
       taken off, so it reads as the text it is. The dotted underline says it opens something. */
    .ol-help-badge {
        cursor: pointer;
        font-weight: inherit;
        text-decoration: underline dotted;
        text-underline-offset: 2px;
    }
    .ol-help-badge:not(.ol-badge) {
        padding: 0;
        border: 0;
        background: none;
        line-height: inherit;
    }

    /* the popup: the page's surface and border, normal text, whatever heading or label it sits in */
    .ol-help-pop {
        position: fixed;
        top: 0;
        left: 0;
        z-index: 200;
        display: block;
        width: min(360px, 92vw);
        padding: 0;
        background: var(--hmm-bg);
        color: var(--hmm-fg);
        border: 1px solid var(--hmm-border);
        border-radius: 6px;
        box-shadow: var(--hmm-shadow-menu);
        font-family: var(--hmm-font);
        font-size: var(--hmm-font-size);
        font-weight: 400;
        font-style: normal;
        line-height: 1.45;
        letter-spacing: normal;
        text-transform: none;
        text-align: left;
        text-decoration: none;
        white-space: normal;
        overflow-wrap: anywhere;
        cursor: auto;
        user-select: text;
    }
    .ol-help-body {
        display: block;
        max-height: min(60vh, 480px);
        overflow: auto;
        padding: 10px 12px;
    }
    .ol-help-body :global(p) {
        margin: 0 0 6px;
    }
    .ol-help-body :global(p:last-child),
    .ol-help-body :global(ol:last-child),
    .ol-help-body :global(ul:last-child) {
        margin-bottom: 0;
    }
    .ol-help-body :global(ol),
    .ol-help-body :global(ul) {
        margin: 6px 0;
        padding-left: 20px;
    }
    .ol-help-body :global(li) {
        margin: 4px 0;
    }
    /* the arrow: a square turned by 45 degrees with two of its borders, over the popup's edge */
    .ol-help-arrow {
        position: absolute;
        left: calc(var(--ol-help-arrow, 18px) - 5px);
        width: 10px;
        height: 10px;
        background: var(--hmm-bg);
        border: 1px solid var(--hmm-border);
        transform: rotate(45deg);
        pointer-events: none;
    }
    .ol-help-pop[data-side='below'] .ol-help-arrow {
        top: -6px;
        border-right: 0;
        border-bottom: 0;
    }
    .ol-help-pop[data-side='above'] .ol-help-arrow {
        bottom: -6px;
        border-left: 0;
        border-top: 0;
    }
</style>
