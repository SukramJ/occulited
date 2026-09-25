<script lang="ts">
    /*
     * A ring meter: one quantity, drawn as the share of a capacity it has taken.
     *
     * It looks like the "Tortengrafik" the maintainer asked for and it is deliberately *not* a pie.
     * A pie chart divides a whole into categories that stand next to each other as equals; every
     * proportion this UI has - memory against the RAM, a filesystem against its size, the duty
     * cycle against the 1 % it is allowed - is one value against one limit, and a two-slice pie of
     * "used" and "free" spends a legend and a second colour on a number the reader already has.
     * The honest form for a ratio against a limit is a meter: an arc that fills, a track behind it
     * in a lighter step of the same colour, and the number in the middle. Nothing here is
     * encoded in colour alone - the percentage is text in the centre of the ring, the absolute
     * figures are text beside it, and a ring that has gone into warning says so in words with an
     * icon, because the ring turning amber is a hint and never the message.
     *
     * The colours come from the theme's own tokens (D-21): `--hmm-accent` while the value is
     * unremarkable, `--hmm-warn` and `--hmm-error` as it climbs. All three clear 3:1 against both
     * card surfaces (the card tokens of task 53), which is the contrast a chart mark owes its
     * background. The track is that same colour at 16 % over the card, so it reads as the empty
     * part of one scale rather than as a second series.
     *
     * Task 53 gave the tile the card's head row - the icon in its tinted circle, the label as
     * the title, a code (a radio address) under it - and the bigger ring; task 54 uses it for
     * the radio load cards of the Interfaces page, with the connection dot in the head and the
     * history graph in the foot. The ring logic lives here and nowhere else.
     */
    import type {Snippet} from 'svelte';
    import Icon, {type IconName} from './Icon.svelte';

    interface Props {
        /** what is being measured, e.g. "Memory" */
        label: string;
        /** the head row's icon */
        icon?: IconName;
        /** a code under the label - a mount point, a radio address - in the address font */
        sub?: string;
        /** the filled share, 0..1; clamped */
        value: number;
        /** the number inside the ring - the reader's copy of the value, e.g. "33 %" */
        display: string;
        /** the absolute figures beside the ring, e.g. "662 / 1985 MB" */
        detail?: string;
        /** a tooltip of the detail line alone - where its figure comes from */
        detailTitle?: string;
        /** why the ring is amber or red; shown with a warning icon. Ignored while level is 'ok'. */
        note?: string;
        /** small muted lines under the figures - a breakdown, a remark; one line per entry, empty ones dropped */
        meta?: string | string[];
        level?: 'ok' | 'warn' | 'error';
        /** the full sentence for a screen reader and the tooltip */
        title?: string;
        /**
         * task 152: the highest value of the graph's span, 0..1 like value - a second arc behind the
         * current one in a lighter tint, in the colour of its own level; drawn only above the value
         */
        peak?: number | null;
        peakLevel?: 'ok' | 'warn' | 'error';
        /** the peak in words beside the ring, e.g. "max 21 % (1 h)" - the arc is never the only copy */
        peakText?: string;
        /** what sits at the right end of the head row - a connection dot, a badge */
        head?: Snippet;
        /** a trend line, a breakdown, a link - rendered across the foot of the tile */
        children?: Snippet;
    }
    let {label, icon, sub = '', value, display, detail = '', detailTitle = '', note = '', meta = '', level = 'ok', title = '', peak = null, peakLevel = 'ok', peakText = '', head, children}: Props = $props();

    // r and the stroke live on a 100 × 100 grid; the tile decides how many pixels that is
    const R = 44;
    const CIRC = 2 * Math.PI * R;
    const frac = $derived(Math.max(0, Math.min(1, Number.isFinite(value) ? value : 0)));
    const dash = $derived(`${(frac * CIRC).toFixed(2)} ${CIRC.toFixed(2)}`);
    // a round cap is the 4 px data-end of the bar specs; at zero it would draw a lone dot on the
    // twelve o'clock mark and claim a value that is not there
    const cap = $derived(frac > 0.005 ? 'round' : 'butt');
    const peakFrac = $derived(peak === null || !Number.isFinite(peak) ? 0 : Math.max(0, Math.min(1, peak)));
    const showPeak = $derived(peakFrac > frac + 0.001);
    const peakDash = $derived(`${(peakFrac * CIRC).toFixed(2)} ${CIRC.toFixed(2)}`);
    const metaLines = $derived((Array.isArray(meta) ? meta : [meta]).filter(Boolean));
</script>

<div class="ol-card ol-gauge" class:warn={level === 'warn'} class:err={level === 'error'} class:peakwarn={peakLevel === 'warn'} class:peakerr={peakLevel === 'error'} title={title || undefined}>
    <div class="ol-card-head">
        {#if icon}<span class="ol-card-icon"><Icon name={icon} size={14} /></span>{/if}
        <div class="ol-card-titles">
            <div class="ol-card-title">{label}</div>
            {#if sub}<div class="ol-card-sub">{sub}</div>{/if}
        </div>
        {#if head}<div class="headend">{@render head()}</div>{/if}
    </div>
    <div class="row">
        <div class="ring">
            <svg viewBox="0 0 100 100" aria-hidden="true">
                <circle class="track" cx="50" cy="50" r={R} />
                {#if showPeak}<circle class="peak" cx="50" cy="50" r={R} stroke-dasharray={peakDash} stroke-linecap="round" transform="rotate(-90 50 50)" />{/if}
                <circle class="arc" cx="50" cy="50" r={R} stroke-dasharray={dash} stroke-linecap={cap} transform="rotate(-90 50 50)" />
            </svg>
            <span class="num">{display}</span>
        </div>
        <div class="body">
            {#if detail}<div class="det" title={detailTitle || undefined}>{detail}</div>{/if}
            {#if peakText}<div class="peaktext"><span class="swatch" aria-hidden="true"></span>{peakText}</div>{/if}
            {#if note && level !== 'ok'}
                <div class="note"><Icon name="alert" size={13} />{note}</div>
            {/if}
            {#each metaLines as line, i (i)}<div class="meta">{line}</div>{/each}
        </div>
    </div>
    {#if children}<div class="ol-card-foot">{@render children()}</div>{/if}
</div>

<style>
    .ol-gauge {
        --ol-gauge-hue: var(--hmm-accent);
        display: flex;
        flex-direction: column;
        gap: 10px;
    }
    .ol-gauge.warn {
        --ol-gauge-hue: var(--hmm-warn);
    }
    .ol-gauge.err {
        --ol-gauge-hue: var(--hmm-error);
    }
    /* task 152: the span's peak in the colour of its own level, lighter than the current arc */
    .ol-gauge { --ol-gauge-peak: var(--hmm-accent); }
    .ol-gauge.peakwarn { --ol-gauge-peak: var(--hmm-warn); }
    .ol-gauge.peakerr { --ol-gauge-peak: var(--hmm-error); }
    .headend { flex: 0 0 auto; margin-left: auto; display: flex; align-items: center; }

    .row {
        display: flex;
        align-items: center;
        gap: 14px;
    }

    .ring {
        position: relative;
        flex: 0 0 auto;
        width: 88px;
        height: 88px;
    }
    .ring svg {
        display: block;
        width: 100%;
        height: 100%;
    }
    .track {
        fill: none;
        stroke: var(--ol-gauge-hue);
        stroke-width: 10;
        opacity: 0.16;
    }
    .peak {
        fill: none;
        stroke: var(--ol-gauge-peak);
        stroke-width: 10;
        opacity: 0.42;
    }
    .arc {
        fill: none;
        stroke: var(--ol-gauge-hue);
        stroke-width: 10;
    }
    /* The value, in the ink of the page and not in the colour of the ring: a light hue is a bad
       text colour, and the coloured arc around the number already carries the identity. */
    .num {
        position: absolute;
        inset: 0;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 18px;
        font-weight: 600;
        color: var(--hmm-fg);
        letter-spacing: -0.01em;
    }

    .body {
        min-width: 0;
    }
    .det {
        font-size: 15px;
        overflow-wrap: anywhere;
    }
    .peaktext {
        display: flex;
        align-items: center;
        gap: 5px;
        margin-top: 2px;
        font-size: var(--hmm-font-size-small);
        color: var(--hmm-fg-muted);
    }
    .swatch {
        flex: 0 0 auto;
        width: 10px;
        height: 10px;
        border-radius: 50%;
        background: var(--ol-gauge-peak);
        opacity: 0.42;
    }
    .note {
        display: flex;
        align-items: center;
        gap: 4px;
        margin-top: 4px;
        font-size: var(--hmm-font-size-small);
        color: var(--ol-gauge-hue);
    }
    .meta {
        margin-top: 4px;
        font-size: var(--hmm-font-size-small);
        color: var(--hmm-fg-muted);
        overflow-wrap: anywhere;
    }
</style>
