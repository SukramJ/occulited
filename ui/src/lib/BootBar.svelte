<script lang="ts">
    /*
     * The reboot countdown (task 94) as occulited's page draws it: a bar that starts full and
     * empties from left to right until the box is back, with the time left beside it, or - thin -
     * the interfaces' bar after the reload. The figures come from lib/bootbar.ts, which the
     * waiting page lighttpd serves during the boot runs as well, so the two draw the same bar.
     *
     * role="progressbar" with aria-valuenow (how far along; left out while indeterminate) and the
     * text as aria-valuetext. Under prefers-reduced-motion nothing animates: the bar is redrawn
     * every three seconds and the stripe stands still.
     */
    import {barView, bootText, readyView, type BootEntry} from './bootbar';
    import {i18n} from './i18n.svelte';

    // `waiting`: the interfaces the ready bar still waits for (lib/bootbar.ts startingInterfaces), null
    // before the services list answered
    let {entry, mode = 'boot', url = '', hint = true, waiting = null}: {entry: BootEntry; mode?: 'boot' | 'ready'; url?: string; hint?: boolean; waiting?: readonly string[] | null} = $props();

    const reduced = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
    let now = $state(Date.now());
    $effect(() => {
        const id = setInterval(() => (now = Date.now()), reduced ? 3000 : 200);
        return () => clearInterval(id);
    });

    const lang = $derived(i18n.language);
    const view = $derived(mode === 'ready' ? {...readyView(entry, now, lang, waiting), hint: ''} : barView(entry, now, lang, url));
    // B-104: the interfaces' bar is named by the heading above it, and its value is the sentence under it
    const label = $derived(mode === 'ready' ? bootText('readyTitle', lang) : bootText('barLabel', lang));
</script>

<div class="ol-bootbar" class:ol-bootbar-thin={mode === 'ready'} class:ol-bootbar-still={reduced} data-mode={mode}>
    <div class="ol-bootbar-track" class:indeterminate={view.indeterminate} role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={view.percent ?? undefined} aria-valuetext={view.text}>
        <div class="ol-bootbar-fill" style:width={view.indeterminate ? '100%' : `${(view.fill * 100).toFixed(2)}%`}></div>
    </div>
    <div class="ol-bootbar-text">{view.text}</div>
    {#if hint && view.hint}<div class="ol-bootbar-hint">{view.hint}</div>{/if}
</div>

<style>
    .ol-bootbar { margin: 12px 0 4px; text-align: center; }
    /* the fill stands at the right and shrinks: the bar empties from left to right */
    .ol-bootbar-track { display: flex; justify-content: flex-end; height: 8px; border-radius: 4px; overflow: hidden; background: var(--hmm-accent-bg); }
    .ol-bootbar-thin .ol-bootbar-track { height: 4px; border-radius: 2px; }
    .ol-bootbar-fill { height: 100%; background: var(--hmm-accent); border-radius: inherit; }
    .indeterminate .ol-bootbar-fill { opacity: 0.35; background: repeating-linear-gradient(-45deg, var(--hmm-accent) 0 8px, transparent 8px 16px); background-size: 22.63px 100%; animation: ol-bootbar-stripe 1.6s linear infinite; }
    .ol-bootbar-text { margin-top: 6px; color: var(--hmm-fg-muted); font-size: var(--hmm-font-size-small); font-variant-numeric: tabular-nums; }
    .ol-bootbar-hint { margin-top: 8px; overflow-wrap: anywhere; }
    .ol-bootbar-still .ol-bootbar-fill { animation: none; }
    @keyframes ol-bootbar-stripe { to { background-position: 22.63px 0; } }
    @media (prefers-reduced-motion: reduce) {
        .ol-bootbar-fill { animation: none !important; transition: none !important; }
    }
</style>
