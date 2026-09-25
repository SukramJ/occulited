<script lang="ts">
    /*
     * Task 57 (D-68): the System menu. The *System* tab and a system page's title switcher open it;
     * it takes no screen space while closed. On a desktop it is a popover anchored under whatever
     * opened it; on a phone an iOS bottom sheet with a drag handle over a dimmed page.
     *
     * The look is iOS Settings: 44 px rows, a small coloured square behind each icon, the label, a
     * checkmark on the current page, hairline separators inset after the icon; the panel has the
     * card's surface, radius and shadow. Above the rows a filter (lib/SearchInput.svelte) that
     * matches the labels and the keywords of lib/systemmenu.ts - typing narrows the list at once,
     * Enter opens the first match, an empty result says *Nothing found*. A row shows an amber or
     * red dot while its page has an active warning (lib/systemmenu.svelte.ts reads them).
     *
     * The panel itself - the popover's placement and its morph out of the anchor (task 98's FLIP,
     * lib/morph.ts), the phone's sheet with the drag handle, the backdrop, the click outside, the
     * focus - is lib/MenuPanel.svelte since task 139, shared with the Addons menu.
     *
     * The keyboard: the filter is focused whenever the open cannot bring up an on-screen keyboard -
     * the keyboard's own click (Enter or Space on the tab or the title), Ctrl/⌘+K, and a mouse or a
     * pen (task 188, lib/systemmenu.svelte.ts's opensFilter); a tap leaves the focus on the panel, so
     * a phone's keyboard does not cover the sheet it just opened. Arrow keys, Home and End move through the rows, ArrowUp from the first
     * row returns to the filter, a letter typed on a row goes into the filter, Enter opens, Escape
     * closes and gives the focus back to the anchor. Ctrl/⌘+K opens the menu from anywhere in the
     * shell with the filter focused - not while the focus is in a text field, where the browser's
     * or the field's own binding stands. It is written beside the filter and on the *System* tab
     * (its tooltip and `aria-keyshortcuts`), because until task 188 nothing in the UI said it existed. `role="menu"` with `menuitem`s, `aria-current="page"` on
     * the checked row, a `nav` landmark named *System menu*.
     */
    import {untrack} from 'svelte';
    import Icon from './Icon.svelte';
    import MenuPanel from './MenuPanel.svelte';
    import SearchInput from './SearchInput.svelte';
    import {i18n, t, translations} from './i18n.svelte';
    import {link, navigate, router} from './router.svelte';
    import {SYSTEM_PAGES, matchPages, shortcutLabel, step, systemPageAt, type SystemPage} from './systemmenu';
    import {closeSystemMenu, openSystemMenu, refreshDots, systemMenu} from './systemmenu.svelte';

    let filter = $state('');
    let panel = $state<HTMLElement | null>(null);
    /** the panel is the phone's bottom sheet: no Ctrl key to hint at there */
    let sheet = $state(false);
    let list = $state<HTMLElement | null>(null);
    /** whether the next close returns the focus to the anchor: not after a row was chosen */
    let giveBack = $state(true);

    const current = $derived(systemPageAt(router.path));
    /** the label in both languages, so the German one counts for an English reader too */
    const labelsOf = (p: SystemPage) => translations(p.label);
    const shown = $derived(matchPages(filter, labelsOf));

    function dotLabel(sev: 'error' | 'warning'): string {
        return sev === 'error' ? t('Error') : t('Warning');
    }

    // the panel (lib/MenuPanel.svelte) does the placement, the motion, the sheet, the click outside
    // and the focus; this menu resets its filter when it opens and closes on a chosen row
    $effect(() => {
        if (systemMenu.open) untrack(() => {
            filter = '';
            void refreshDots();
        });
    });
    function close(back = true) {
        giveBack = back;
        closeSystemMenu();
    }

    // the route changes - a row was chosen, a link, Back - and the menu goes; the focus stays where
    // the page puts it
    $effect(() => {
        void router.path;
        untrack(() => {
            if (systemMenu.open) close(false);
        });
    });

    // ---- the keyboard ------------------------------------------------------------------------
    function rows(): HTMLElement[] {
        return list ? Array.from(list.querySelectorAll<HTMLElement>('[role="menuitem"]')) : [];
    }
    function focusRow(i: number) {
        rows()[i]?.focus({preventScroll: false});
    }
    function isTextField(el: Element | null): boolean {
        if (!(el instanceof HTMLElement)) return false;
        if (el.isContentEditable) return true;
        if (el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement) return true;
        return el instanceof HTMLInputElement && !['checkbox', 'radio', 'button', 'submit', 'range', 'file', 'color'].includes(el.type);
    }
    function onPanelKey(ev: KeyboardEvent) {
        const target = ev.target as HTMLElement;
        const inFilter = target instanceof HTMLInputElement;
        const all = rows();
        const at = all.indexOf(target);
        switch (ev.key) {
            case 'Escape':
                // the filter's own Escape, with text in it, clears the text and stops here (SearchInput)
                ev.preventDefault();
                close(true);
                return;
            case 'ArrowDown':
            case 'ArrowUp':
            case 'Home':
            case 'End':
                ev.preventDefault();
                if (inFilter) {
                    if (all.length === 0) return;
                    // from the filter: down to the current page's row when it is listed, else the first
                    const cur = ev.key === 'ArrowUp' || ev.key === 'End' ? all.length - 1 : Math.max(0, all.findIndex((r) => r.getAttribute('aria-current') === 'page'));
                    focusRow(cur);
                    return;
                }
                if (ev.key === 'ArrowUp' && at === 0) {
                    panel?.querySelector<HTMLInputElement>('input')?.focus();
                    return;
                }
                focusRow(step(at, all.length, ev.key));
                return;
            case 'Enter':
                if (inFilter) {
                    ev.preventDefault();
                    const first = shown[0];
                    if (first) navigate(first.path);
                }
                return;
            case 'Tab':
                // a menu is left by Tab: the focus goes to the anchor first, so the browser's own Tab
                // carries on from there to the next control of the page, not into the rows
                if (systemMenu.anchor?.isConnected) systemMenu.anchor.focus({preventScroll: true});
                close(false);
                return;
        }
        // a letter typed on a row goes into the filter, where the reader meant it
        if (!inFilter && ev.key.length === 1 && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
            ev.preventDefault();
            filter += ev.key;
            panel?.querySelector<HTMLInputElement>('input')?.focus();
        }
    }
    function onWindowKey(ev: KeyboardEvent) {
        if (!(ev.ctrlKey || ev.metaKey) || ev.altKey || ev.shiftKey || ev.key.toLowerCase() !== 'k') return;
        if (isTextField(document.activeElement) && !panel?.contains(document.activeElement)) return;
        ev.preventDefault();
        if (systemMenu.open) {
            panel?.querySelector<HTMLInputElement>('input')?.focus();
            return;
        }
        openSystemMenu(systemMenu.tab, true);
    }

</script>

<svelte:window onkeydown={onWindowKey} />

<MenuPanel open={systemMenu.open} anchor={systemMenu.anchor} label={t('System menu')} keyboard={systemMenu.focusFilter} {giveBack} onrequestclose={close} onkeydown={onPanelKey} bind:panel bind:sheet>
    <div class="ol-sysfilter">
        <SearchInput bind:value={filter} placeholder={t('Filter')} label={t('Filter the System menu')} delay={0} />
        <!-- task 188: the shortcut was there since task 57 and nothing said so. The tab carries it as
             aria-keyshortcuts and in its tooltip; here it is written out beside the field it leads to,
             and hidden in the sheet, where there is no Ctrl key to press. aria-hidden: the tab's
             aria-keyshortcuts is what a screen reader should hear, not this line twice. -->
        {#if !sheet}<kbd class="ol-syskbd" data-shortcut aria-hidden="true">{shortcutLabel()}</kbd>{/if}
    </div>
    <div class="ol-syslist" role="menu" aria-label={t('System')} bind:this={list}>
        {#each shown as p (p.id)}
            {@const active = current?.id === p.id}
            {@const dot = systemMenu.dots[p.id]}
            <a
                role="menuitem"
                class="ol-sysrow"
                href={p.path}
                use:link
                tabindex="-1"
                aria-current={active ? 'page' : undefined}
                data-page={p.id}
                onclick={() => close(false)}
            >
                <span class="ol-systile"><Icon name={p.icon} size={16} /></span>
                <span class="ol-syslabel">{t(p.label)}</span>
                {#if dot}<span class="ol-sysdot {dot}" role="img" aria-label={dotLabel(dot)}></span>{/if}
                {#if active}
                    <svg class="ol-syscheck" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5" /></svg>
                {/if}
            </a>
        {/each}
        {#if shown.length === 0}
            <div class="ol-sysempty" role="note">{t('Nothing found')}</div>
        {/if}
    </div>
</MenuPanel>

<style>
    .ol-sysfilter { flex: 0 0 auto; display: flex; align-items: center; gap: 6px; padding: 2px 2px 8px; }
    /* the shortcut beside the filter: quiet, the same key cap the rest of the shell would use */
    .ol-syskbd {
        flex: 0 0 auto; font-family: var(--hmm-font); font-size: var(--hmm-font-size-small);
        color: var(--hmm-fg-muted); background: var(--hmm-bg-sunken); border: 1px solid var(--hmm-border);
        border-radius: 6px; padding: 2px 6px; white-space: nowrap;
    }
    .ol-sysfilter :global(.ol-search-input) { height: 32px; border-radius: 8px; font-size: 14px; }
    .ol-syslist { flex: 1 1 auto; min-height: 0; overflow-y: auto; }
    /* an iOS Settings row: 44 px, the square in the one pastel accent (task 210), the label, the check at the right end;
       the hairline between two rows starts after the square */
    .ol-sysrow {
        position: relative; display: flex; align-items: center; gap: 12px; min-height: 44px; padding: 0 10px 0 8px;
        border-radius: 8px; color: var(--hmm-fg); text-decoration: none; font-size: 14px; cursor: pointer;
    }
    .ol-sysrow + .ol-sysrow::before { content: ''; position: absolute; left: 48px; right: 8px; top: -0.5px; height: 1px; background: var(--hmm-border-muted); }
    .ol-sysrow:hover { background: var(--hmm-row-hover); }
    .ol-sysrow:hover::before, .ol-sysrow:hover + .ol-sysrow::before { opacity: 0; }
    .ol-sysrow:focus-visible { outline: 2px solid var(--hmm-focus); outline-offset: -2px; }
    .ol-sysrow[aria-current='page'] .ol-syslabel { font-weight: 600; }
    .ol-systile {
        flex: 0 0 auto; display: inline-flex; align-items: center; justify-content: center;
        width: 28px; height: 28px; border-radius: 7px; background: var(--ol-sys-tile-bg); color: var(--ol-sys-tile-ink);
    }
    .ol-syslabel { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .ol-syscheck { flex: 0 0 auto; width: 18px; height: 18px; fill: none; stroke: var(--hmm-accent); stroke-width: 2.4; stroke-linecap: round; stroke-linejoin: round; }
    .ol-sysempty { padding: 12px 10px; color: var(--hmm-fg-muted); font-size: 14px; }
</style>
