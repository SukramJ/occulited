<script lang="ts">
    import {dialog, settle, type SelectOption} from './dialog.svelte';
    import {t} from './i18n.svelte';
    import ModalDialog from './ModalDialog.svelte';

    /*
     * The shell's one question dialog: renders whatever dialog.svelte.ts has pending - a yes/no,
     * a text, a choice from a list, or several choices - and settles it. Built on ModalDialog
     * (task 45), on layer 100 so it sits over a page's own modal.
     *
     * The list of askSelect is a native listbox (type-ahead, arrows, a double click confirms -
     * the browser's own behaviour); it shows up to twenty rows, grouped under an optgroup per
     * taxonomy, and shrinks to the viewport on a phone. askMultiSelect's list is drawn by hand:
     * a native multiple select has no checkboxes and a plain click there replaces the choice,
     * which nobody expects when ticking rooms - so every row is a checkbox, a click toggles it,
     * the arrows move, Space toggles, Enter confirms, and typing jumps as a listbox does.
     */
    let text = $state('');
    let field: HTMLInputElement | undefined = $state();
    let choice = $state('');
    let chosen = $state<string[]>([]);
    /** the row the keyboard is on in the multi list */
    let active = $state('');
    let listbox: HTMLDivElement | undefined = $state();
    let selectBox: HTMLSelectElement | undefined = $state();
    let okButton: HTMLButtonElement | undefined = $state();
    let cancelButton: HTMLButtonElement | undefined = $state();
    // task 76: an ask() with `run` does its act inside the dialog - the buttons are off while it
    // runs, and a text it resolves to (or the error it throws) is shown under the message
    let running = $state(false);
    let outcome = $state<{text: string; error: boolean} | null>(null);
    const p = $derived(dialog.pending);
    /** the act ran and said why it did not do what was asked: only Close is left */
    const finished = $derived(outcome !== null && !outcome.error);
    const select = $derived(p?.options.select);
    const multi = $derived(select?.multi === true);
    const canConfirm = $derived(p?.options.input ? text.trim().length >= (p.options.input.minLength ?? 1) : multi ? chosen.length > 0 : true);
    /** what the confirming button hands back: the text, the chosen entry or entries, or '' for a plain yes */
    const answer = () => (p?.options.input ? text.trim() : multi ? select!.options.map((o) => o.value).filter((v) => chosen.includes(v)) : select ? choice : '');

    /** the options under their group headings, in the order they came - a taxonomy stays together */
    const groups = $derived.by(() => {
        const out: {label: string; options: SelectOption[]}[] = [];
        for (const o of select?.options ?? []) {
            const label = o.group ?? '';
            const last = out[out.length - 1];
            if (last && last.label === label) last.options.push(o);
            else out.push({label, options: [o]});
        }
        return out;
    });
    /**
     * The depth of a native option as leading no-break spaces: an option cannot hold an element,
     * and ordinary spaces are collapsed by one browser and kept by the next - these never are.
     */
    const indent = (o: SelectOption): string => '\u00a0\u00a0\u00a0'.repeat(Math.max(0, (o.depth ?? 1) - 1));
    /**
     * Twenty entries at once (task 45), or fewer for a short list but never a squeezed one; a
     * group heading takes a line of the native listbox, so the headings come on top of the twenty.
     */
    const headings = $derived(groups.filter((g) => g.label).length);
    const rows = $derived(select?.size ?? Math.max(4, Math.min(20, select?.options.length ?? 0) + headings));

    $effect(() => {
        if (!p) return;
        text = p.options.input?.initial ?? '';
        choice = p.options.select?.initial ?? p.options.select?.options[0]?.value ?? '';
        chosen = [];
        press = {key: '', n: 0};
        active = p.options.select?.options[0]?.value ?? '';
        running = false;
        outcome = null;
        // focus the input when there is one, the list when there is one, else the confirming
        // button - Enter then answers; a rename starts with its name selected, so typing replaces it.
        // focusCancel makes Cancel the default: Enter on it cancels (a removal nobody should
        // confirm by pressing Enter out of habit)
        const focusCancel = p.options.focusCancel === true;
        queueMicrotask(() => {
            // (on the list, the arrows and type-ahead work at once; Enter still confirms)
            (field ?? listbox ?? selectBox ?? (focusCancel ? cancelButton : okButton))?.focus();
            field?.select();
        });
    });
    async function confirm() {
        const pending = p;
        if (!pending || running || finished) return;
        const run = pending.options.run;
        if (!run) {
            settle(answer());
            return;
        }
        const value = answer();
        running = true;
        outcome = null;
        try {
            const text = await run();
            if (dialog.pending !== pending) return;
            if (typeof text === 'string' && text) outcome = {text, error: false};
            else settle(value);
        } catch (e) {
            if (dialog.pending === pending) outcome = {text: (e as Error).message, error: true};
        } finally {
            running = false;
            // the buttons were off while it ran; the focus goes back to the one that is left
            queueMicrotask(() => (outcome && !outcome.error ? cancelButton : okButton)?.focus());
        }
    }
    function onKey(ev: KeyboardEvent) {
        if (!p) return;
        // Escape is ModalDialog's; a button other than OK answers its own click, not Enter
        if (ev.key === 'Enter' && canConfirm && !running && !finished && !(ev.target instanceof HTMLButtonElement && ev.target !== okButton)) {
            ev.preventDefault();
            void confirm();
        }
    }

    // ---- the multi list ---------------------------------------------------------------------
    /*
     * A double click confirms - but only one whose two presses both landed on the same entry.
     * On a phone the tap on "Assign" and a quick tap on a row of the dialog that opened under the
     * finger make a double tap for the browser, which would confirm a choice only just begun.
     */
    let press = {key: '', n: 0};
    function pressed(key: string) {
        press = press.key === key ? {key, n: press.n + 1} : {key, n: 1};
    }
    const doubled = (key: string): boolean => press.key === key && press.n >= 2;

    function toggle(value: string) {
        active = value;
        chosen = chosen.includes(value) ? chosen.filter((v) => v !== value) : [...chosen, value];
    }
    let typed = '';
    let typedAt = 0;
    function onListKey(ev: KeyboardEvent) {
        const values = select?.options.map((o) => o.value) ?? [];
        if (values.length === 0) return;
        const at = Math.max(0, values.indexOf(active));
        const go = (i: number) => {
            active = values[Math.min(values.length - 1, Math.max(0, i))] ?? '';
            listbox?.querySelector<HTMLElement>(`[data-value="${CSS.escape(active)}"]`)?.scrollIntoView({block: 'nearest'});
        };
        if (ev.key === 'ArrowDown') go(at + 1);
        else if (ev.key === 'ArrowUp') go(at - 1);
        else if (ev.key === 'Home') go(0);
        else if (ev.key === 'End') go(values.length - 1);
        else if (ev.key === 'PageDown') go(at + rows);
        else if (ev.key === 'PageUp') go(at - rows);
        else if (ev.key === ' ') toggle(active);
        else if (ev.key.length === 1 && !ev.ctrlKey && !ev.altKey && !ev.metaKey) {
            // type-ahead as a native listbox has it: the letters typed within a moment form the
            // prefix, the next row (after the active one) whose name starts with it is jumped to
            const now = Date.now();
            typed = now - typedAt < 700 ? typed + ev.key.toLowerCase() : ev.key.toLowerCase();
            typedAt = now;
            const options = select?.options ?? [];
            const start = typed.length === 1 ? at + 1 : at;
            for (let i = 0; i < options.length; i += 1) {
                const o = options[(start + i) % options.length];
                if (o && o.label.trim().toLowerCase().startsWith(typed)) {
                    go((start + i) % options.length);
                    break;
                }
            }
        } else return;
        ev.preventDefault();
    }
</script>

<svelte:window onkeydown={onKey} />

<ModalDialog open={p !== null} title={p?.options.title ?? t('Please confirm')} size={select ? 'medium' : 'small'} layer={100} autofocus={false} onclose={() => { if (!running) settle(null); }}>
    {#if p}
        <!-- a longer message comes in paragraphs, separated by a blank line -->
        {#each p.options.message.split('\n\n') as para, i (i)}<p>{para}</p>{/each}
        {#if outcome}<div class="ol-notice ol-dialog-outcome" class:error={outcome.error} role="status">{outcome.text}</div>{/if}
        {#if p.options.input}
            <label class="ol-dialog-field">
                {#if p.options.input.label}<span>{p.options.input.label}</span>{/if}
                <input class="hmm-input" type={p.options.input.type ?? 'text'} bind:value={text} bind:this={field} placeholder={p.options.input.placeholder ?? ''} autocomplete={p.options.input.type === 'password' ? 'new-password' : 'off'} />
            </label>
        {/if}
        {#if select && !multi}
            <label class="ol-dialog-field ol-dialog-list">
                {#if select.label}<span>{select.label}</span>{/if}
                <select class="hmm-select ol-dialog-select" size={rows} bind:value={choice} bind:this={selectBox} aria-label={select.label ?? ''} onpointerdown={() => pressed('select')} ondblclick={() => { if (canConfirm && doubled('select')) settle(answer()); }}>
                    {#each groups as g, i (i)}
                        {#if g.label}
                            <optgroup label={g.label}>
                                {#each g.options as o (o.value)}<option value={o.value}>{indent(o)}{o.label}</option>{/each}
                            </optgroup>
                        {:else}
                            {#each g.options as o (o.value)}<option value={o.value}>{indent(o)}{o.label}</option>{/each}
                        {/if}
                    {/each}
                </select>
            </label>
        {/if}
        {#if select && multi}
            <div class="ol-dialog-field ol-dialog-list">
                {#if select.label}<span id="ol-dialog-list-label">{select.label}</span>{/if}
                <div class="ol-listbox" role="listbox" aria-multiselectable="true" tabindex="0" aria-label={select.label ?? p.options.title ?? ''} aria-activedescendant={active ? `ol-opt-${select.options.findIndex((o) => o.value === active)}` : undefined} bind:this={listbox} onkeydown={onListKey}>
                    {#each groups as g, gi (gi)}
                        <!-- a taxonomy is an ARIA group named by its heading: a screen reader says where a row belongs -->
                        <div role={g.label ? 'group' : 'presentation'} aria-labelledby={g.label ? `ol-grp-${gi}` : undefined}>
                        {#if g.label}<div class="ol-listbox-group" id={`ol-grp-${gi}`} role="presentation">{g.label}</div>{/if}
                        {#each g.options as o (o.value)}
                            <!-- svelte-ignore a11y_click_events_have_key_events (the keys are handled on the listbox) -->
                            <div class="ol-listbox-row" role="option" tabindex="-1" id={`ol-opt-${select.options.indexOf(o)}`} data-value={o.value} aria-selected={chosen.includes(o.value)} class:active={active === o.value} style={`padding-left:${8 + Math.max(0, (o.depth ?? 1) - 1) * 16}px`} onclick={() => toggle(o.value)} onpointerdown={() => pressed(o.value)} ondblclick={() => { if (!doubled(o.value)) return; if (!chosen.includes(o.value)) chosen = [...chosen, o.value]; settle(answer()); }}>
                                <input type="checkbox" tabindex="-1" checked={chosen.includes(o.value)} aria-hidden="true" />
                                <span>{o.label}</span>
                            </div>
                        {/each}
                        </div>
                    {/each}
                </div>
            </div>
        {/if}
    {/if}
    {#snippet footer()}
        {#if multi}<span class="ol-muted ol-dialog-count">{t('{n} selected', {n: chosen.length})}</span>{/if}
        {#if finished || p?.options.cancel !== ''}
            <button class="hmm-button" type="button" bind:this={cancelButton} disabled={running} onclick={() => settle(null)}>{finished ? t('Close') : (p?.options.cancel ?? t('Cancel'))}</button>
        {/if}
        {#if !finished}
            <button class="hmm-button primary" class:danger={p?.options.danger} type="button" bind:this={okButton} disabled={!canConfirm || running} onclick={() => void confirm()}>{p?.options.confirm ?? t('OK')}</button>
        {/if}
    {/snippet}
</ModalDialog>

<style>
    p { margin: 0 0 12px; white-space: pre-line; flex: 0 0 auto; }
    .ol-dialog-field { display: block; margin: 0 0 4px; flex: 0 0 auto; }
    .ol-dialog-field > span { display: block; margin-bottom: var(--ol-label-gap); color: var(--hmm-fg-muted); }
    /* the text field only - a row's checkbox stretched to the full width squeezes its name onto several lines */
    .ol-dialog-field input.hmm-input { width: 100%; }
    /* the list takes the free height of the dialog and scrolls inside it; the buttons stay */
    .ol-dialog-list { display: flex; flex-direction: column; flex: 0 1 auto; min-height: 0; }
    /* task 45: the listbox gets its rows back - .hmm-select's 24px is for a dropdown, not a list */
    .ol-dialog-select { width: 100%; height: auto; min-height: 0; flex: 0 1 auto; white-space: pre; font-family: inherit; padding: 2px 0; }
    .ol-dialog-select option { padding: 2px 8px; }
    .ol-dialog-select optgroup { color: var(--hmm-fg-muted); font-style: normal; font-weight: 600; }
    .ol-listbox { flex: 0 1 auto; min-height: 0; overflow-y: auto; border: 1px solid var(--hmm-border); border-radius: var(--hmm-radius); background: var(--hmm-input-bg); padding: 2px 0; }
    .ol-listbox:focus-visible { outline: none; border-color: var(--hmm-accent); }
    .ol-listbox-group { padding: 6px 8px 2px; color: var(--hmm-fg-muted); font-size: var(--hmm-font-size-small); font-weight: 600; text-transform: uppercase; letter-spacing: 0.04em; }
    .ol-listbox-row { display: flex; align-items: center; gap: 8px; min-height: 22px; padding-right: 8px; cursor: default; user-select: none; }
    .ol-listbox-row:hover { background: var(--hmm-row-hover); }
    .ol-listbox-row[aria-selected='true'] { background: var(--hmm-accent-bg); }
    .ol-listbox-row.active { outline: 1px solid var(--hmm-accent); outline-offset: -1px; }
    .ol-listbox-row input { margin: 0; pointer-events: none; }
    .ol-dialog-count { margin-right: auto; font-size: var(--hmm-font-size-small); }
    .primary { background: var(--hmm-accent); color: var(--hmm-bg); border-color: var(--hmm-accent); }
    /* B-110: a danger question's button is app.css's `.hmm-button.danger` - standard, red on hover and focus */
</style>
