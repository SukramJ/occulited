<script lang="ts">
    /*
     * task 40: a single-value select with a filter input - the Log page's Unit and Tag
     * dropdowns, which on a box with forty units were a native popup nobody could search.
     *
     * Ported from homematic-manager's packages/ui/src/lib/components/MultiSelect.svelte in its
     * `multiple: false` mode (one kit, D-21): the trigger shows the chosen value, the popover has
     * the filter at the top and the options under it, substring-filtered case-insensitively.
     * Stripped: the checkboxes, check all / uncheck all, the count summary. Added, and fed back
     * into the original in the same pass: Escape closes without choosing and puts the focus back
     * on the trigger; ArrowUp/ArrowDown move a highlight, Enter chooses it, Tab closes.
     *
     * The popover is the shell's own `.ol-menupop` (the addon menu, the row actions, the time
     * range), closed on an outside click and on Escape the way TimeRange.svelte does it - a
     * `svelte:window` handler, not a document listener that outlives the component. Nothing here
     * asks anything, and nothing here is a browser dialog (house rule).
     *
     * Contract for the Log page (B-59): the component never touches the option list it is given
     * and calls `onchange` exactly once per choice; the caller decides what a choice does.
     */
    import {t} from './i18n.svelte';
    import {filterOptions, step, type SelectOption} from './select';
    import {onscreen} from './popover';

    interface Props {
        /** the chosen value; '' is "all" */
        value: string;
        options: readonly SelectOption[];
        /** the prefix on the trigger and the popover's accessible name: "Unit", "Tag" */
        label: string;
        /** the label of the '' entry; default "all" */
        allLabel?: string;
        onchange?: ((value: string) => void) | undefined;
        disabled?: boolean;
    }

    let {value, options, label, allLabel = undefined, onchange = undefined, disabled = false}: Props = $props();

    let open = $state(false);
    let filter = $state('');
    let highlight = $state(0);
    let root = $state<HTMLDivElement | undefined>(undefined);
    let trigger = $state<HTMLButtonElement | undefined>(undefined);
    let list = $state<HTMLUListElement | undefined>(undefined);

    // one id per instance, for aria-controls / aria-activedescendant
    const uid = `ol-select-${Math.random().toString(36).slice(2, 8)}`;
    const all = $derived<SelectOption>({value: '', label: allLabel ?? t('all')});
    const withAll = $derived<SelectOption[]>([all, ...options]);
    const shown = $derived(filterOptions(withAll, filter));
    const chosen = $derived(withAll.find((o) => o.value === value)?.label ?? value);
    const activeId = $derived(open && shown[highlight] ? `${uid}-${highlight}` : undefined);

    function show() {
        filter = '';
        const i = withAll.findIndex((o) => o.value === value);
        highlight = i < 0 ? 0 : i;
        open = true;
    }
    function hide(refocus: boolean) {
        open = false;
        if (refocus) trigger?.focus();
    }
    function toggle() {
        if (open) hide(true);
        else show();
    }
    function choose(o: SelectOption) {
        if (o.disabled === true) return;
        hide(true);
        if (o.value !== value) onchange?.(o.value);
    }
    // the filter narrows the list; the highlight goes back to its top
    $effect(() => {
        void filter;
        highlight = 0;
    });
    // the highlighted row stays in view while the arrow keys walk a long list
    $effect(() => {
        if (!open || !list) return;
        const el = list.querySelector<HTMLElement>(`#${uid}-${highlight}`);
        el?.scrollIntoView({block: 'nearest'});
    });
    function onKey(e: KeyboardEvent) {
        switch (e.key) {
            case 'ArrowDown':
                e.preventDefault();
                highlight = step(shown, highlight, 1);
                break;
            case 'ArrowUp':
                e.preventDefault();
                highlight = step(shown, highlight, -1);
                break;
            case 'Enter': {
                e.preventDefault();
                const o = shown[highlight];
                if (o) choose(o);
                break;
            }
            case 'Escape':
                e.preventDefault();
                e.stopPropagation();
                hide(true);
                break;
            case 'Tab':
                hide(false);
                break;
        }
    }
    // the trigger takes the arrows too: ↓ on a closed select opens it, as a native one does
    function onTriggerKey(e: KeyboardEvent) {
        if (!open && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
            e.preventDefault();
            show();
        }
    }
    function onWindowClick(e: MouseEvent) {
        if (open && root && !root.contains(e.target as Node)) hide(false);
    }
    function onWindowKey(e: KeyboardEvent) {
        if (e.key === 'Escape' && open) hide(true);
    }
    // the input gets the focus the moment the popover opens: typing is what the popover is for
    function autofocus(el: HTMLInputElement) {
        el.focus();
    }
</script>

<svelte:window onclick={onWindowClick} onkeydown={onWindowKey} />

<div class="ol-select" bind:this={root}>
    <button
        type="button"
        class="hmm-button ol-select-btn"
        bind:this={trigger}
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={`${uid}-list`}
        aria-activedescendant={activeId}
        aria-label={label}
        title={`${label}: ${chosen}`}
        {disabled}
        onclick={toggle}
        onkeydown={onTriggerKey}
    >
        <span class="ol-select-text">{label}: {chosen}</span>
        <span class="ol-caret" aria-hidden="true">▾</span>
    </button>
    {#if open}
        <div class="ol-menupop ol-select-pop" use:onscreen>
            <input
                class="hmm-input ol-select-filter"
                type="search"
                bind:value={filter}
                placeholder={t('Filter')}
                aria-label={t('Filter')}
                aria-controls={`${uid}-list`}
                aria-activedescendant={activeId}
                autocomplete="off"
                use:autofocus
                onkeydown={onKey}
            />
            <ul class="ol-select-list" role="listbox" id={`${uid}-list`} aria-label={label} bind:this={list}>
                {#each shown as o, i (o.value)}
                    <!-- svelte-ignore a11y_click_events_have_key_events -->
                    <li
                        id={`${uid}-${i}`}
                        role="option"
                        class="ol-select-opt"
                        class:hl={i === highlight}
                        aria-selected={o.value === value}
                        aria-disabled={o.disabled === true || undefined}
                        title={o.label}
                        onmousedown={(e) => e.preventDefault()}
                        onmousemove={() => (highlight = i)}
                        onclick={() => choose(o)}
                    >
                        {o.label}
                    </li>
                {/each}
                {#if shown.length === 0}
                    <li class="ol-select-empty" role="presentation">{t('no match')}</li>
                {/if}
            </ul>
        </div>
    {/if}
</div>
