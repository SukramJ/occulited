<script lang="ts">
    /*
     * Task 95: a round swatch that plays an LED look in CSS - the colour, the blink rate, the
     * second colour of an alternating pattern, and the normal colour a blink shows in its dark phase
     * when it blinks over it (background). It stands next to every state on the status LED
     * page, so a colour on the box can be found on the page. With reduced motion it keeps still
     * and the text beside it says the pattern.
     */
    import {swatchOf, type LEDLook} from './led';

    // task 315: any #rrggbb beside the seven names, and a dim (the night's level in percent) that
    // fades the swatch the way the LED is dimmed
    let {look, background = '', size = 14, label = '', dim = 0}: {look: LEDLook; background?: string; size?: number; label?: string; dim?: number} = $props();
    const c1 = $derived(swatchOf(look.color));
    const c0 = $derived(background && background !== 'off' ? swatchOf(background) : undefined);
    const c2 = $derived(look.color2 ? swatchOf(look.color2) : 'transparent');
</script>

<span
    class="ol-led-swatch"
    class:dark={look.color === 'off'}
    data-pattern={look.color === 'off' ? 'solid' : look.pattern}
    data-color={look.color}
    style:--c1={c1}
    style:--c2={c2}
    style:--c0={c0}
    style:opacity={dim > 0 && dim < 100 && look.color !== 'off' ? Math.max(0.35, dim / 100) : undefined}
    data-background={c0 ? background : undefined}
    data-dim={dim > 0 && dim < 100 ? dim : undefined}
    style:width="{size}px"
    style:height="{size}px"
    role={label ? 'img' : undefined}
    aria-label={label || undefined}
    aria-hidden={label ? undefined : 'true'}
></span>

<style>
    .ol-led-swatch {
        display: inline-block;
        flex: 0 0 auto;
        vertical-align: middle;
        border-radius: 50%;
        border: 1px solid var(--hmm-border-strong);
        background: var(--c1);
        box-shadow: 0 0 6px color-mix(in srgb, var(--c1) 55%, transparent);
    }
    .ol-led-swatch.dark {
        background: var(--hmm-bg-sunken);
        box-shadow: none;
    }
    .ol-led-swatch[data-pattern='slow'] { animation: ol-led-blink 1s steps(1, end) infinite; }
    .ol-led-swatch[data-pattern='fast'] { animation: ol-led-blink 0.2s steps(1, end) infinite; }
    .ol-led-swatch[data-pattern='flash'] { animation: ol-led-flash 2s steps(1, end) infinite; }
    .ol-led-swatch[data-pattern='double'] { animation: ol-led-double 2s steps(1, end) infinite; }
    .ol-led-swatch[data-pattern='alternate'] { animation: ol-led-alternate 1s steps(1, end) infinite; }
    .ol-led-swatch[data-pattern='breathe'] { animation: ol-led-breathe 2s ease-in-out infinite; }
    @keyframes ol-led-blink {
        0% { background: var(--c1); }
        50% { background: var(--c0, var(--hmm-bg-sunken)); box-shadow: none; }
    }
    @keyframes ol-led-flash {
        0% { background: var(--c1); }
        5% { background: var(--c0, var(--hmm-bg-sunken)); box-shadow: none; }
    }
    @keyframes ol-led-double {
        0% { background: var(--c1); }
        7.5% { background: var(--c0, var(--hmm-bg-sunken)); box-shadow: none; }
        15% { background: var(--c1); box-shadow: 0 0 6px color-mix(in srgb, var(--c1) 55%, transparent); }
        22.5% { background: var(--c0, var(--hmm-bg-sunken)); box-shadow: none; }
    }
    @keyframes ol-led-breathe {
        0%, 100% { background: var(--c0, var(--hmm-bg-sunken)); box-shadow: none; }
        50% { background: var(--c1); box-shadow: 0 0 6px color-mix(in srgb, var(--c1) 55%, transparent); }
    }
    @keyframes ol-led-alternate {
        0% { background: var(--c1); }
        50% { background: var(--c2); box-shadow: 0 0 6px color-mix(in srgb, var(--c2) 55%, transparent); }
    }
    @media (prefers-reduced-motion: reduce) {
        .ol-led-swatch { animation: none !important; }
    }
</style>
