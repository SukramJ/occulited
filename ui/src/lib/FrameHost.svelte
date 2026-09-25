<script lang="ts">
    // Task 39: the kept addon pages (lib/frames.svelte.ts). Every kept page is one iframe, rendered
    // here once and never moved - an iframe that is moved in the DOM loads again - and the route
    // decides which one shows. The shell's pages render beside the hidden ones.
    //
    // Hidden is `visibility: hidden` at the same size, not `display: none`: measured in Chromium, a
    // same-origin frame keeps its timers, animation frames and websocket either way, but
    // `display: none` takes the frame's viewport away and gives it back, and Node-RED's editor lays
    // itself out again on both resizes. While a shell page shows, this box is out of the flow
    // (fixed, invisible, behind everything) at the size it had last, so no page is resized by the
    // switch either.
    import {untrack} from 'svelte';
    import {frames, markBlocked, type KeptFrame} from './frames.svelte';
    import {postLook} from './theme.svelte';
    import {i18n} from './i18n.svelte';

    let {active}: {active: string} = $props();
    const shown = $derived(frames.list.some((f) => f.key === active && !f.blocked));

    let host = $state<HTMLDivElement | null>(null);
    let width = $state(0);
    let height = $state(0);

    function loaded(f: KeptFrame, e: Event) {
        const el = e.currentTarget as HTMLIFrameElement;
        postLook(el.contentWindow);
        if (!f.detectBlocked) return;
        // a page that refuses embedding (X-Frame-Options / frame-ancestors) is an error page of
        // another origin, detected by the access failure; AddonFrame offers the new tab (task 10)
        try {
            void el.contentWindow?.location.href;
        } catch {
            markBlocked(f.key);
        }
    }

    // A theme change reaches every iframe from theme.svelte.ts; a language change reached none,
    // which did not matter while no frame outlived the Settings page. A kept one does.
    let lastLanguage = i18n.language;
    $effect(() => {
        const l = i18n.language;
        if (l === lastLanguage) return;
        lastLanguage = l;
        untrack(() => {
            for (const el of Array.from(host?.querySelectorAll('iframe') ?? [])) postLook(el.contentWindow);
        });
    });
</script>

<div
    class="ol-kept"
    class:ol-kept-on={shown}
    style={shown ? undefined : `width:${width}px;height:${height}px`}
    aria-hidden={shown ? undefined : 'true'}
    bind:this={host}
    bind:clientWidth={width}
    bind:clientHeight={height}
>
    {#each frames.list as f (f.key)}
        {#if !f.blocked}
            <!-- data-addon: B-132 names the addon in the console when its frame comes back as the
                 shell's own page, and the frame is found by its contentWindow (App.svelte) -->
            <iframe class="ol-frame ol-kept-frame" class:ol-kept-shown={f.key === active} data-addon={f.addon} src={f.src} title={f.title} inert={f.key !== active} onload={(e) => loaded(f, e)}></iframe>
        {/if}
    {/each}
</div>

<style>
    .ol-kept { position: fixed; left: 0; top: 0; visibility: hidden; pointer-events: none; z-index: -1; overflow: hidden; }
    .ol-kept.ol-kept-on { position: relative; flex: 1; min-height: 70vh; visibility: visible; pointer-events: auto; z-index: auto; }
    .ol-kept-frame { position: absolute; inset: 0; width: 100%; height: 100%; min-height: 0; visibility: hidden; }
    .ol-kept-on .ol-kept-frame.ol-kept-shown { visibility: visible; }
</style>
