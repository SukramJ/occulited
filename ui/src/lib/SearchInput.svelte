<script lang="ts">
    /*
     * Task 104: a free-text filter. It grows to the width it is given (the Log page's panel gives it the
     * rest of its first row), shows a magnifier while it is empty and an ✕ button once something is typed
     * - the ✕ empties it, applies the empty filter and keeps the focus in the field, and Escape in the
     * field does the same. The filter is applied while typing, once the typing pauses (300 ms by default),
     * and at once on Enter; text typed and taken back within the pause asks for nothing.
     *
     * The field keeps its text inset on both sides whether the magnifier or the ✕ shows, so the text does
     * not jump when the first letter replaces one with the other. No native `type="search"`: Chromium and
     * WebKit would draw a clear button of their own beside this one.
     *
     * Offered to the other pages with a free-text filter (Services, Metadata); this task changes only the
     * Log page.
     */
    import {onDestroy, untrack} from 'svelte';
    import Icon from './Icon.svelte';
    import {t} from './i18n.svelte';

    interface Props {
        value?: string;
        placeholder?: string;
        /** the field's accessible name */
        label: string;
        /** called with the text once the typing has paused, and at once on Enter, ✕ and Escape */
        onsearch?: (value: string) => void;
        /** the pause, in ms */
        delay?: number;
    }
    let {value = $bindable(''), placeholder = '', label, onsearch, delay = 300}: Props = $props();

    let input = $state<HTMLInputElement | null>(null);
    let timer: ReturnType<typeof setTimeout> | undefined;
    /** the text the filter was last applied with */
    let applied = untrack(() => value);

    function apply() {
        clearTimeout(timer);
        timer = undefined;
        if (value === applied) return;
        applied = value;
        onsearch?.(value);
    }
    function typed() {
        clearTimeout(timer);
        timer = setTimeout(apply, delay);
    }
    function clear() {
        value = '';
        apply();
        input?.focus();
    }
    function onkeydown(ev: KeyboardEvent) {
        if (ev.key === 'Enter') {
            apply();
        } else if (ev.key === 'Escape' && value !== '') {
            // the field's own Escape: a menu or a dialog listening on the document does not see it
            ev.preventDefault();
            ev.stopPropagation();
            clear();
        }
    }
    onDestroy(() => clearTimeout(timer));
</script>

<div class="ol-search" class:ol-search-filled={value !== ''}>
    {#if value === ''}
        <span class="ol-search-icon" aria-hidden="true"><Icon name="search" size={14} /></span>
    {/if}
    <input bind:this={input} class="hmm-input ol-search-input" type="text" {placeholder} aria-label={label} bind:value oninput={typed} {onkeydown} autocomplete="off" spellcheck="false" />
    {#if value !== ''}
        <button type="button" class="ol-search-clear" aria-label={t('Clear filter')} title={t('Clear filter')} onclick={clear}><Icon name="x" size={14} /></button>
    {/if}
</div>

<style>
    .ol-search { position: relative; display: flex; align-items: center; flex: 1 1 auto; min-width: 0; }
    .ol-search-input { width: 100%; min-width: 0; padding-left: 26px; padding-right: 28px; }
    .ol-search-icon { position: absolute; left: 7px; display: inline-flex; color: var(--hmm-fg-muted); pointer-events: none; }
    .ol-search-clear {
        position: absolute; right: 2px; display: inline-flex; align-items: center; justify-content: center;
        width: 22px; height: 20px; padding: 0; border: 0; border-radius: var(--hmm-radius);
        background: none; color: var(--hmm-fg-muted); cursor: pointer;
    }
    .ol-search-clear:hover, .ol-search-clear:focus-visible { color: var(--hmm-fg); background: var(--hmm-control-bg-hover); }
</style>
