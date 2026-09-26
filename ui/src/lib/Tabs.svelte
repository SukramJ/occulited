<script lang="ts" generics="T extends string">
    /*
     * A row of tabs that shows one part of a page or a sheet at a time: the `ol-seg` segmented row
     * (app.css) as a WAI-ARIA tablist - the arrow keys, Home and End move between the tabs and choose
     * them. First the Log settings' three tabs (task 228), then the Trust stores page's four stores
     * (openccu-lite task 267, the maintainer: "a subnav for those 4 truststores on top of page"), one
     * implementation for both. A tab may carry a count after its label. The panel is the caller's:
     * `role="tabpanel"`, `id={panelId(id)}`, `aria-labelledby={tabId(id)}`.
     */
    interface Tab {
        id: T;
        label: string;
        /** shown after the label, e.g. how many entries the part holds */
        count?: number;
    }
    interface Props {
        tabs: Tab[];
        value: T;
        /** the tablist's name for a screen reader */
        label: string;
        /** the prefix of the tabs' and panels' element ids */
        idPrefix: string;
        /** called with the tab chosen (after `value` changed) */
        onchange?: (id: T) => void;
        class?: string;
    }
    let {tabs, value = $bindable(), label, idPrefix, onchange, class: cls = ''}: Props = $props();
    let buttons = $state<HTMLButtonElement[]>([]);

    function choose(id: T) {
        if (value === id) return;
        value = id;
        onchange?.(id);
    }
    function key(ev: KeyboardEvent, i: number) {
        const n = tabs.length;
        const to = ev.key === 'ArrowRight' ? (i + 1) % n : ev.key === 'ArrowLeft' ? (i - 1 + n) % n : ev.key === 'Home' ? 0 : ev.key === 'End' ? n - 1 : -1;
        if (to < 0) return;
        ev.preventDefault();
        choose(tabs[to]!.id);
        buttons[to]?.focus();
    }
</script>

<div class="ol-seg ol-tabs {cls}" role="tablist" aria-label={label}>
    {#each tabs as tb, i (tb.id)}
        <button
            type="button"
            role="tab"
            id={`${idPrefix}-tab-${tb.id}`}
            aria-selected={value === tb.id}
            aria-controls={`${idPrefix}-panel-${tb.id}`}
            tabindex={value === tb.id ? 0 : -1}
            data-tab={tb.id}
            bind:this={buttons[i]}
            onclick={() => choose(tb.id)}
            onkeydown={(ev) => key(ev, i)}
            >{tb.label}{#if tb.count !== undefined}<span class="ol-tab-count">{tb.count}</span>{/if}</button
        >
    {/each}
</div>
