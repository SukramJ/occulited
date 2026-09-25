<script lang="ts">
    // Task 91's emergency kit as a printable page (D-64): the kit text and a QR code of the recovery
    // key, black on white in either theme, like task 154's key sheet. The key lives only in this
    // component's props: closing drops it, and nothing goes into the browser's storage.
    import {t} from './i18n.svelte';
    import {qrSvg} from './qrencode';

    interface Props {
        text: string;
        code: string;
        onclose: () => void;
    }
    let {text, code, onclose}: Props = $props();

    const qr = $derived(qrSvg(code));

    function onkey(e: KeyboardEvent) {
        if (e.key === 'Escape') onclose();
    }
</script>

<svelte:window onkeydown={onkey} />

<div class="ks-backdrop" role="dialog" aria-modal="true" aria-label={t('Emergency kit')} data-sheet="kit">
    <div class="ks-toolbar">
        <button type="button" class="hmm-button primary" onclick={() => window.print()} data-sheet="print">{t('Print')}</button>
        <button type="button" class="hmm-button" onclick={onclose} data-sheet="close">{t('Close')}</button>
        <span class="ks-toolbar-note">{t('Printing to a PDF file works from the same dialog. Closing this view drops the key from the page.')}</span>
    </div>
    <article class="ks-sheet">
        <div class="ek-row">
            <pre class="ek-text">{text}</pre>
            <svg class="ek-qr" viewBox="0 0 {qr.size} {qr.size}" shape-rendering="crispEdges" role="img" aria-label={t('Recovery key')}>
                <rect width={qr.size} height={qr.size} fill="#fff" />
                <path d={qr.path} fill="#000" />
            </svg>
        </div>
    </article>
</div>

<style>
    .ks-backdrop { position: fixed; inset: 0; z-index: 1000; overflow: auto; background: var(--ol-paper-backdrop); padding: 16px; }
    .ks-toolbar { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; max-width: 210mm; margin: 0 auto 12px; }
    .ks-toolbar-note { color: var(--ol-paper-backdrop-ink); font-size: 0.9em; }
    .ks-sheet { max-width: 210mm; margin: 0 auto; background: var(--ol-paper); color: var(--ol-paper-ink); padding: 12mm; box-sizing: border-box; font-family: system-ui, sans-serif; }
    .ek-row { display: flex; gap: 6mm; align-items: flex-start; flex-wrap: wrap; }
    .ek-text { flex: 1 1 120mm; margin: 0; font-family: ui-monospace, monospace; font-size: 8.5pt; white-space: pre-wrap; overflow-wrap: anywhere; color: inherit; background: none; }
    .ek-qr { flex: 0 0 auto; width: 40mm; height: 40mm; }
    @media (max-width: 600px) {
        .ks-backdrop { padding: 8px; }
        .ks-sheet { padding: 6mm; }
    }
    @media print {
        :global(html), :global(body) { background: var(--ol-paper) !important; }
        :global(body *) { visibility: hidden; }
        .ks-backdrop, .ks-backdrop :global(*) { visibility: visible; }
        .ks-backdrop { position: absolute; inset: 0; background: none; padding: 0; overflow: visible; }
        .ks-toolbar { display: none; }
        .ks-sheet { max-width: none; padding: 0; }
    }
</style>
