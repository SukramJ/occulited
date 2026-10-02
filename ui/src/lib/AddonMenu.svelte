<script lang="ts">
    /*
     * Task 59: the Addons control of the tab bar - a plain tab that opens the dropdown, and the
     * pinned addons as tabs of their own. Task 131 (D-86) moved them: they stand after *Status* and
     * before *Addons*, the tabs they belong to.
     *
     *   Status  [RedMatic]  [Homematic-Manager]  [Addons]  [System ▾]
     *
     * The dropdown has two groups with a divider between them: first the addons with a web
     * frontend of their own (a nav entry of source `addon`), which the user can pin to the tab bar
     * and put in an order by drag & drop or with the keyboard; then the addons without one - a
     * settings page only, or switched off - sorted by name, with no pin and no handle. The footer
     * with *Installed addons* and *Catalogue* stays under its own separator (task 26).
     *
     * A nav.d entry that is no addon - a link the box was configured with, like the stub's *CCU
     * WebUI* - stood as a tab after *System* until the maintainer's follow-up to task 131
     * (2026-09-16): the bar is Status, the pins, Addons and System, nothing else. Such entries are a
     * third group under the addons, in the box's order, with no pin, no handle and no ⚙: the name
     * opens the page in the shell's frame (or a new tab, for a `blank` entry), ↗ always a new tab.
     *
     * The button used to read as a breadcrumb ("Addons — Homematic-Manager") and reserved the
     * width of the widest one; it is the word *Addons* now, no caret, only its own width, marked
     * active on an addon page that has no tab of its own, on a settings page and on the two footer
     * pages. Pins and order are the user's and stored with the account on the box (D-54,
     * lib/prefs.svelte.ts); the list is reconciled against what is installed (lib/addonorder.ts).
     *
     * Below PHONE_BELOW_PX nothing folds: the bar is two rows there and the nav scrolls sideways
     * (task 191), so every pinned tab stands in it.
     */
    import {onMount, untrack} from 'svelte';
    import {link, navigate, router} from './router.svelte';
    import {onPage, settingsPath} from './routes';
    import {t, i18n} from './i18n.svelte';
    import type {Addon, NavEntry} from './api';
    import Icon from './Icon.svelte';
    import Help from './Help.svelte';
    import MenuPanel from './MenuPanel.svelte';
    import SearchInput from './SearchInput.svelte';
    import {addonHref, ensureLegacySid} from './auth.svelte';
    import {frames, closeAddon} from './frames.svelte';
    import {browserFaviconDeps, resolveFavicon} from './favicon';
    import {prefs, setAddonPreferences} from './prefs.svelte';
    import {arrange, withPin, PHONE_BELOW_PX, type AddonPref} from './addonorder';
    import {moved} from './sortable';
    import {Sortable} from './sortable.svelte';
    import SortHandle from './SortHandle.svelte';
    import {systemMenu} from './systemmenu.svelte';

    interface Props {
        /** the installed addons (GET /addons) and whether that list has answered or failed */
        installed: Addon[];
        installedLoaded: boolean;
        /** the nav entries of source `addon`: the frontends */
        nav: NavEntry[];
        /** the other nav entries: nav.d drop-ins that are no addon, in the box's order */
        links?: NavEntry[];
        /** the route's addon frontend (/nav/<id>) and settings page (/addon-settings/<id>), or '' */
        navId: string;
        addonId: string;
    }
    let {installed, installedLoaded, nav, links = [], navId, addonId}: Props = $props();

    // the dropdown's footer: administration *of* addons, not addons
    // task 139: one page - the catalogue and the installed addons together
    const ADDON_PAGES = [
        {path: '/addons', label: 'Manage addons'},
    ];

    // the preferences are loaded by the shell (App.svelte) since task 193: this menu is part of
    // the top bar, which the App may hide, and the choices must outlive it

    function navLabel(e: NavEntry): string {
        return e.label[i18n.language] ?? e.label.en ?? e.id;
    }

    // ---- the addon rows (task 55) ------------------------------------------------------------
    // Every installed addon is a row - the addon list merged with the frontend entries of the
    // nav list:
    //
    //   ⋮⋮ [icon] Homematic-Manager        ↗  📌  ⚙     a frontend: pin, drag, ↗ to a new tab
    //   ⋮⋮ [icon] RedMatic                 ↗  📌  ⚙
    //   ──────────────────────────────────────────
    //      [icon] Mosquitto (muted)      ? ↗̶     ⚙     a settings page only: no frontend of its own
    //      [icon] NEO Server · switched off ↗̶    ⚙     disabled: needs the ReGa
    type AddonRow = {
        id: string;
        name: string;
        version: string;
        /** the frontend; undefined for an addon with a settings page only */
        nav?: NavEntry;
        /** switched off, and why (the popup on its name) */
        off: boolean;
        offReason: string;
        /** a settings page of its own (config_url), which the shell frames at /addon-settings/<id> */
        hasSettings: boolean;
        /** where ⚙ leads: that settings page, or the addon's row on *Installed addons* */
        settingsHref: string;
    };
    const rows = $derived.by((): AddonRow[] => {
        // task 88: a frontend belongs to its addon by `addon`; its route id may be another name (/nav/red)
        const byAddon = new Map(nav.map((e) => [e.addon ?? e.id, e]));
        const out: AddonRow[] = installed.map((a) => {
            const e = byAddon.get(a.id);
            const hasSettings = !!(a.config_url || a.settings?.config_url);
            return {
                id: a.id,
                // the nav label is what occulited names the frontend by; without one, the same
                // order nav.go takes (settings name, name, id); the API already names an addon without a Name: of its own
                name: e ? navLabel(e) : a.settings?.name || a.name || a.id,
                version: a.version ?? '',
                nav: e,
                off: a.enabled === false,
                offReason: a.rega_reason || a.binary_reason || t('The rc.d script is not executable; nothing starts it.'),
                hasSettings,
                settingsHref: hasSettings ? settingsPath(a.id) : `/addons?addon=${encodeURIComponent(a.id)}`,
            };
        });
        // a frontend the addon list does not name: it has not answered yet, or failed
        for (const e of nav) {
            const id = e.addon ?? e.id;
            if (out.some((r) => r.id === id)) continue;
            out.push({id, name: navLabel(e), version: '', nav: e, off: false, offReason: '', hasSettings: false, settingsHref: `/addons?addon=${encodeURIComponent(id)}`});
        }
        return out.sort((x, y) => x.name.localeCompare(y.name, i18n.language, {sensitivity: 'base'}) || x.id.localeCompare(y.id));
    });
    // the first group: a live frontend; the second: everything else, by name
    const isFront = (r: AddonRow) => r.nav !== undefined && !r.off;
    const frontRows = $derived(rows.filter(isFront));
    const restRows = $derived(rows.filter((r) => !isFront(r)));
    // the first group in the user's order, with the pins (lib/addonorder.ts); by name until the
    // stored set has answered, so the rows never jump from one order to another after a moment
    const order = $derived(arrange(
        frontRows.map((r) => r.id),
        prefs.loaded ? prefs.addons : [],
    ));
    const group = $derived(order.map((p) => ({p, row: frontRows.find((r) => r.id === p.id)!})));
    const pinnedRows = $derived(prefs.loaded ? group.filter((g) => g.p.pinned).map((g) => g.row) : []);
    // what the popup lists: everything, or what the filter leaves
    const shownGroup = $derived(group.filter((g) => matches(g.row.name)));
    const shownRest = $derived(restRows.filter((r) => matches(r.name)));
    const shownLinks = $derived(links.filter((e) => matches(navLabel(e))));

    // ---- the fold (the width) ---------------------------------------------------------------
    // Below PHONE_BELOW_PX (a phone) every pinned tab is shown - the nav is its own row there and
    // scrolls sideways, so nothing has to fit. Above it, as many stand in the bar as
    // fit on its one row: every pinned tab is rendered, the ones past `shown` out of the flow and
    // invisible so they can be measured, and `fit` counts how many fit beside the fixed tabs -
    // the room is the nav's width plus the header spacer's, which is the slack (lib/addonorder.ts
    // says why no fixed breakpoint could do it). It runs again whenever the nav or the spacer
    // changes size: a resize, a label in the other language, a tab pinned or unpinned. Svelte
    // applies the count before the browser paints, so the bar never shows a wrapped row.
    let narrow = $state(false);
    $effect(() => {
        if (typeof matchMedia !== 'function') return;
        const mq = matchMedia(`(max-width: ${PHONE_BELOW_PX - 1}px)`);
        const read = () => (narrow = mq.matches);
        read();
        mq.addEventListener('change', read);
        return () => mq.removeEventListener('change', read);
    });
    let shown = $state(0);
    let tabEls = $state<Record<string, HTMLElement | undefined>>({});
    let fitTick = $state(0);
    function fit() {
        const navEl = menuEl?.parentElement;
        const spacer = menuEl?.closest('.ol-header')?.querySelector('.ol-spacer');
        if (!navEl || !spacer) {
            shown = pinnedRows.length;
            return;
        }
        const gap = parseFloat(getComputedStyle(navEl).columnGap) || 0;
        const room = navEl.getBoundingClientRect().width + spacer.getBoundingClientRect().width;
        let need = -gap;
        for (const el of Array.from(navEl.children)) {
            if (el.classList.contains('ol-pintab')) continue;
            need += el.getBoundingClientRect().width + gap;
        }
        let k = 0;
        for (const r of pinnedRows) {
            const w = tabEls[r.id]?.getBoundingClientRect().width ?? 0;
            if (need + w + gap > room) break;
            need += w + gap;
            k++;
        }
        shown = k;
    }
    $effect(() => {
        void fitTick;
        void pinnedRows;
        void i18n.language;
        if (narrow) {
            shown = pinnedRows.length;
            return;
        }
        untrack(fit);
    });
    $effect(() => {
        const navEl = menuEl?.parentElement;
        const spacer = menuEl?.closest('.ol-header')?.querySelector('.ol-spacer');
        if (!navEl || !spacer || typeof ResizeObserver !== 'function') return;
        const observer = new ResizeObserver(() => fitTick++);
        observer.observe(navEl);
        observer.observe(spacer);
        return () => observer.disconnect();
    });
    const tabsShown = $derived(pinnedRows.slice(0, shown));

    // ---- the favicons (task 55) --------------------------------------------------------------
    // A row's icon is its frontend's favicon, found in the browser and cached per addon and version
    // (lib/favicon.ts); without one, the letter. Nothing is fetched until the dropdown is opened
    // for the first time: a shell load must not knock on every addon's frontend - Node-RED's
    // editor page is not small, and a box that is only glanced at never needs the icons. A
    // pinned addon is the exception: its tab shows the icon from the start.
    let favicons = $state<Record<string, string>>({});
    let iconsWanted = $state(false);
    const iconsAsked = new Set<string>(); // not state: asking must not re-run the effect
    $effect(() => {
        if (!installedLoaded || !prefs.loaded) return;
        const pinned = new Set(pinnedRows.map((r) => r.id));
        for (const r of rows) {
            if (!r.nav || (!iconsWanted && !pinned.has(r.id))) continue;
            const key = `${r.id}@${r.version}@${r.nav.href}`;
            if (iconsAsked.has(key)) continue;
            iconsAsked.add(key);
            const id = r.id;
            void resolveFavicon({id, version: r.version, href: r.nav.href}, location.origin, browserFaviconDeps()).then((src) => {
                favicons[id] = src;
            });
        }
    });

    // ---- what is active ----------------------------------------------------------------------
    function isActive(p: string): boolean {
        return p === '/' ? router.path === '/' : onPage(router.path, p);
    }
    const selectedAddon = $derived(rows.find((r) => r.nav !== undefined && r.nav.id === navId));
    // an addon's settings page is not the management page, even though both are about addons
    const selectedAddonPage = $derived(addonId !== '' ? undefined : ADDON_PAGES.find((n) => isActive(n.path)));
    // a nav.d page that is no addon, shown in the shell's frame: it has no tab, so the button is active
    const selectedLink = $derived(navId === '' ? undefined : links.find((e) => e.id === navId));
    // the open addon has a tab of its own: that tab is active, not the button
    const buttonActive = $derived((selectedAddon !== undefined && !tabsShown.some((r) => r.id === selectedAddon.id)) || addonId !== '' || selectedAddonPage !== undefined || selectedLink !== undefined);

    // ---- the popup -----------------------------------------------------------------------------
    // A menu that only closes by clicking its own button is a nuisance: close on Escape and on a
    // click anywhere outside it. Listeners exist only while it is open. The System menu closes
    // itself the same way, so a click on either button closes the other's popup.
    let open = $state(false);
    // task 248: the dot of an addon update (or another warning that leads to /addons), read with System's
    const addonDot = $derived(systemMenu.addonDot);
    // task 139: the popup is the System menu's panel (lib/MenuPanel.svelte): the same surface, the
    // morph out of the button and back into it, the sheet with the drag handle on a phone, the click
    // outside and Escape. Opened by keyboard (Enter or Space on the button), the filter gets the focus
    // when there is one; a tap leaves it on the panel, so a phone's keyboard does not cover the sheet.
    let keyboardOpen = $state(false);
    let giveBack = $state(true);
    let panel = $state<HTMLElement | null>(null);
    let btnEl = $state<HTMLElement | null>(null);
    // the filter (the maintainer, 2026-09-22): only above FILTER_FROM rows, addons and links together;
    // it narrows every group by name, Enter opens the first row, and ordering by Alt+↑/↓ is suspended
    // while it is on (the order of a filtered list is meaningless)
    const FILTER_FROM = 9;
    let filter = $state('');
    const hasFilter = $derived(rows.length + links.length >= FILTER_FROM);
    const matches = (name: string) => filter === '' || name.toLowerCase().includes(filter.trim().toLowerCase());
    // task 125: the ↗ links carry the session's alias for an addon the box marks legacy_session; the
    // alias is asked for when the menu opens, and the links follow
    $effect(() => {
        if (open && (nav.some((e) => e.legacy_session) || links.some((e) => e.legacy_session))) void ensureLegacySid();
    });
    $effect(() => {
        if (open) untrack(() => (filter = ''));
    });
    let menuEl = $state<HTMLElement | null>(null);
    let popEl = $state<HTMLElement | null>(null);
    function requestClose(back: boolean) {
        giveBack = back;
        open = false;
    }
    // the keyboard inside the panel: the arrow keys over the rows, Enter on the filter opens the
    // first row, a letter typed on a row goes into the filter (as the System menu has it)
    function items(): HTMLElement[] {
        return popEl ? Array.from(popEl.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])')) : [];
    }
    function onPanelKey(ev: KeyboardEvent) {
        const target = ev.target as HTMLElement;
        const inFilter = target instanceof HTMLInputElement;
        const all = items();
        const at = all.indexOf(target);
        switch (ev.key) {
            case 'ArrowDown':
            case 'ArrowUp':
            case 'Home':
            case 'End':
                if (ev.altKey) return; // the row's own ordering (sort.rowKey)
                // the System menu's path only with the filter on the panel; without it the arrows on a
                // row stay the browser's (and the handle's own ↑/↓ move the row)
                if (!hasFilter) return;
                ev.preventDefault();
                if (all.length === 0) return;
                if (inFilter || at < 0) {
                    all[ev.key === 'ArrowUp' || ev.key === 'End' ? all.length - 1 : 0]?.focus();
                    return;
                }
                if (ev.key === 'ArrowUp' && at === 0 && hasFilter) {
                    panel?.querySelector<HTMLInputElement>('input')?.focus();
                    return;
                }
                all[ev.key === 'Home' ? 0 : ev.key === 'End' ? all.length - 1 : ev.key === 'ArrowDown' ? Math.min(at + 1, all.length - 1) : Math.max(at - 1, 0)]?.focus();
                return;
            case 'Enter':
                if (inFilter) {
                    ev.preventDefault();
                    all[0]?.click();
                }
                return;
        }
        if (hasFilter && !inFilter && ev.key.length === 1 && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
            ev.preventDefault();
            filter += ev.key;
            panel?.querySelector<HTMLInputElement>('input')?.focus();
        }
    }
    // the selection can change from anywhere (a link, the router); never leave the popup hanging
    $effect(() => {
        void router.path;
        open = false;
    });

    function chooseAddon(e: NavEntry) {
        open = false;
        if (e.target === 'blank') window.open(e.href, '_blank', 'noopener');
        else navigate(`/nav/${encodeURIComponent(e.id)}`);
    }
    // task 39: the addons with a kept page get a ✕ in the dropdown, which frees it (its frontend and
    // its settings page). Closing the page on screen leaves for Status - on its own route it would
    // only be opened again.
    const keptAddons = $derived(new Set(frames.list.map((f) => f.addon).filter((a) => a !== '')));
    function closeKept(r: AddonRow) {
        open = false;
        if (addonId === r.id || (r.nav !== undefined && navId === r.nav.id)) navigate('/');
        closeAddon(r.id);
    }

    // task 55: ⚙ of an addon without a settings page of its own leads to its row on *Installed
    // addons* (`/addons?addon=<id>`), where its details and the switch are. That page loads its
    // list after it mounts, so the row is looked for until it is there (five seconds at most); the
    // page itself only carries the row's id. A second click on the same ⚙ does not change the
    // route, so it ticks instead.
    let revealTick = $state(0);
    $effect(() => {
        void revealTick;
        if (router.path !== '/addons') return;
        const id = new URLSearchParams(router.search).get('addon');
        if (!id) return;
        let timer = 0;
        let tries = 0;
        const look = () => {
            const row = document.getElementById(`addon-row-${id}`);
            if (row) row.scrollIntoView({block: 'center'});
            else if (++tries < 100) timer = window.setTimeout(look, 50);
        };
        look();
        return () => clearTimeout(timer);
    });

    // ---- pins and order -----------------------------------------------------------------------
    // Toggling a pin does not close the menu: one pins a few in a row.
    function togglePin(id: string, pinned: boolean) {
        setAddonPreferences(withPin(order, id, pinned));
    }
    // task 162: the order by the shared handle (lib/sortable.svelte.ts) - drag it, ↑/↓ on it, or
    // Alt+↑/↓ anywhere in the row; the tabs follow once it is dropped. While the filter narrows the
    // list its order is meaningless, so the rows have no handle then.
    const sort = new Sortable({
        rows: () => Array.from(popEl?.querySelectorAll<HTMLElement>('.ol-menurow-front') ?? []),
        length: () => order.length,
        name: (i) => group[i]?.row.name ?? '',
        commit: (from, to) => setAddonPreferences(moved(order, from, to)),
        enabled: () => filter === '',
    });
    function letter(r: AddonRow): string {
        return r.name.slice(0, 1).toUpperCase();
    }
    function pinLabel(r: AddonRow, pinned: boolean): string {
        return pinned ? t('Unpin {name}', {name: r.name}) : t('Pin {name} to the tab bar', {name: r.name});
    }
</script>

<!-- task 55: a row's icon and name, the same in a live row (inside its button) and an inert one -->
{#snippet addonLabel(r: AddonRow)}
    <span class="ol-addonicon">
        {#if favicons[r.id]}
            <!-- a cached icon the frontend has since dropped is the letter again, not a broken image -->
            <img src={favicons[r.id]} alt="" width="18" height="18" onerror={() => (favicons[r.id] = '')} />
        {:else}
            <span class="ol-addonmono" aria-hidden="true">{letter(r)}</span>
        {/if}
    </span>
    <span class="ol-addonname">{r.name}</span>
    {#if r.off}<span class="ol-addonnote">· {t('switched off')}</span>{/if}
{/snippet}

<!-- the ↗, the ✕ and the ⚙ of a row; the same in both groups -->
{#snippet rowTail(r: AddonRow, live: boolean)}
    {#if live && r.nav}
        <a class="ol-newtab" href={addonHref(r.nav.href, r.nav.legacy_session)} target="_blank" rel="noopener" title={t('Open in new tab')} aria-label={t('Open in new tab')} onclick={() => (open = false)}>↗</a>
    {:else}
        <!-- task 51: why the row is inert was its `title`, out of reach on a phone; it is a ? between
             the name and ↗ now -->
        <span class="ol-menuhelp"><Help>{r.off ? `${t('switched off')}: ${r.offReason}` : t('No web interface of its own; its settings are behind ⚙.')}</Help></span>
        <!-- shown so the rows line up and read alike; the name already says it is inert, so a
             screen reader is spared a second time -->
        <span class="ol-newtab" aria-disabled="true" aria-hidden="true">↗</span>
    {/if}
{/snippet}
{#snippet rowEnd(r: AddonRow)}
    {@const settingsFor = t('Settings for {name}', {name: r.name})}
    <!-- task 39: ✕ frees the page the shell keeps loaded for this addon; while any addon has one,
         every row keeps the column, so ⚙ lines up -->
    {#if keptAddons.size > 0}
        {#if keptAddons.has(r.id)}
            {@const closeFor = t('Close {name}', {name: r.name})}
            <button type="button" class="ol-newtab ol-menuclose" title={closeFor} aria-label={closeFor} onclick={() => closeKept(r)}>✕</button>
        {:else}
            <span class="ol-newtab ol-menuclose" aria-hidden="true"></span>
        {/if}
    {/if}
    <a
        class="ol-newtab"
        class:active={addonId === r.id}
        href={r.settingsHref}
        use:link
        title={settingsFor}
        aria-label={settingsFor}
        onclick={() => {
            open = false;
            revealTick++;
        }}><Icon name="settings" size={14} /></a
    >
{/snippet}

<!-- the pinned addons, tabs of their own in the user's order, before the Addons button (task 131,
     D-86: after Status); the ones past `shown` are out of the flow and invisible - there to be
     measured - and on a phone every one of them is shown, the row scrolls (task 191). A tab shows
     the frontend's favicon at text size before the name, and the name alone without one: the
     letter badge stays in the dropdown (D-84) -->
{#each pinnedRows as r, i (r.id)}
    {@const e = r.nav!}
    {@const folded = i >= shown}
    {#if e.target === 'blank'}
        <a class="ol-pintab" class:ol-pintab-folded={folded} aria-hidden={folded} tabindex={folded ? -1 : undefined} href={e.href} target="_blank" rel="noopener" data-addon={r.id} bind:this={tabEls[r.id]}>{#if favicons[r.id]}<span class="ol-pintab-icon"><img src={favicons[r.id]} alt="" width="14" height="14" onerror={() => (favicons[r.id] = '')} /></span>{/if}<span class="ol-pintab-name">{r.name}</span> ↗</a>
    {:else}
        <a class="ol-pintab" class:ol-pintab-folded={folded} class:active={!folded && navId === e.id} aria-hidden={folded} tabindex={folded ? -1 : undefined} href={`/nav/${encodeURIComponent(e.id)}`} use:link data-addon={r.id} bind:this={tabEls[r.id]}>{#if favicons[r.id]}<span class="ol-pintab-icon"><img src={favicons[r.id]} alt="" width="14" height="14" onerror={() => (favicons[r.id] = '')} /></span>{/if}<span class="ol-pintab-name">{r.name}</span></a>
    {/if}
{/each}
<div class="ol-menu" bind:this={menuEl}>
    <!-- task 248: the word, then System's reserved dot and its caret (the dot's place is kept while
         there is none, so the bar does not hop when one arrives) -->
    <button
        type="button"
        class="ol-menubtn ol-addonsbtn ol-dottab"
        class:active={buttonActive}
        aria-haspopup="menu"
        aria-expanded={open}
        bind:this={btnEl}
        onclick={(ev) => {
            keyboardOpen = ev.detail === 0;
            giveBack = true;
            open = !open;
            iconsWanted = true; // task 55: the first opening fetches the favicons
        }}
        ><span class="ol-menubtn-live"
            ><span class="ol-menubtn-text">{t('Addons')}</span><span
                class="ol-sysdot"
                class:error={addonDot === 'error'}
                class:warning={addonDot === 'warning'}
                class:ol-sysdot-none={!addonDot}
                role={addonDot ? 'img' : undefined}
                aria-label={addonDot === 'error' ? t('Error') : addonDot === 'warning' ? t('Warning') : undefined}
                aria-hidden={addonDot ? undefined : 'true'}
                data-addon-dot={addonDot ?? ''}
            ></span><span class="ol-caret" aria-hidden="true">▾</span></span
        ></button
    >
    <MenuPanel {open} anchor={btnEl} label={t('Addons menu')} class="ol-addonpanel" keyboard={keyboardOpen && hasFilter} {giveBack} onrequestclose={requestClose} onkeydown={onPanelKey} bind:panel>
        {#if hasFilter}
            <div class="ol-sysfilter">
                <SearchInput bind:value={filter} placeholder={t('Filter')} label={t('Filter the Addons menu')} delay={0} />
            </div>
        {/if}
        <div class="ol-menupop ol-addonpop" role="menu" bind:this={popEl}>
            {#if shownGroup.length > 0}
                <div role="group" aria-label={t('Addons with a web interface of their own')}>
                    {#each shownGroup as {p, row: r} (r.id)}
                        {@const i = group.indexOf(group.find((g) => g.row.id === r.id)!)}
                        {@const e = r.nav!}
                        <!-- the row is the addon: the handle moves it, the name opens the frontend in the
                             shell's frame, ↗ in a new tab, the pin puts it into the tab bar, ⚙ its settings -->
                        <div
                            class="ol-menurow ol-menurow-front ol-sortrow"
                            class:active={navId === e.id}
                            class:ol-dragging={sort.dragging(r.id)}
                            class:ol-drop-before={sort.before(i)}
                            class:ol-drop-after={sort.after(i)}
                            data-addon={r.id}
                            role="presentation"
                            onkeydown={(ev) => sort.rowKey(ev, i)}
                        >
                            {#if filter === ''}
                                <SortHandle sortable={sort} index={i} key={r.id} name={r.name} />
                            {:else}
                                <span class="ol-grip ol-grip-none" aria-hidden="true"></span>
                            {/if}
                            <button type="button" role="menuitem" class="ol-menuitem" onclick={() => chooseAddon(e)}>
                                {@render addonLabel(r)}
                            </button>
                            {@render rowTail(r, true)}
                            <button type="button" class="ol-newtab ol-pin" class:ol-pinned={p.pinned} aria-pressed={p.pinned} title={pinLabel(r, p.pinned)} aria-label={pinLabel(r, p.pinned)} onclick={() => togglePin(r.id, !p.pinned)}>
                                <Icon name="pin" size={14} />
                            </button>
                            {@render rowEnd(r)}
                        </div>
                    {/each}
                </div>
            {/if}
            {#if shownGroup.length > 0 && shownRest.length > 0}<div class="ol-menusep"></div>{/if}
            {#if shownRest.length > 0}
                <div role="group" aria-label={t('Addons without a web interface')}>
                    {#each shownRest as r (r.id)}
                        <!-- 28.4 + task 55: an addon without a frontend, or one that is switched off, is
                             listed all the same - the box shows what it has installed - with the name and ↗
                             inert: muted, aria-disabled, out of the tab order, the reason in the popup. ⚙ stays. -->
                        <div class="ol-menurow ol-menurow-off" data-addon={r.id}>
                            <span class="ol-grip ol-grip-none" aria-hidden="true"></span>
                            <span role="menuitem" class="ol-menuitem" aria-disabled="true">
                                {@render addonLabel(r)}
                            </span>
                            {@render rowTail(r, false)}
                            <span class="ol-newtab ol-pin ol-pin-none" aria-hidden="true"></span>
                            {@render rowEnd(r)}
                        </div>
                    {/each}
                </div>
            {/if}
            {#if shownGroup.length + shownRest.length > 0 && shownLinks.length > 0}<div class="ol-menusep"></div>{/if}
            {#if shownLinks.length > 0}
                <div role="group" aria-label={t('Links set up on this system')}>
                    {#each shownLinks as e (e.id)}
                        {@const name = navLabel(e)}
                        <!-- a nav.d entry that is no addon: the name opens it where its drop-in says,
                             ↗ in a new tab; the empty slots keep ↗ in the addons' column -->
                        <div class="ol-menurow ol-menurow-link" class:active={selectedLink?.id === e.id} data-nav={e.id}>
                            <span class="ol-grip ol-grip-none" aria-hidden="true"></span>
                            {#if e.target === 'blank'}
                                <a role="menuitem" class="ol-menuitem" href={addonHref(e.href, e.legacy_session)} target="_blank" rel="noopener" onclick={() => (open = false)}>
                                    <span class="ol-addonicon"><span class="ol-addonmono" aria-hidden="true">{name.slice(0, 1).toUpperCase()}</span></span>
                                    <span class="ol-addonname">{name}</span>
                                    <span class="ol-sronly">({t('opens in a new tab')})</span>
                                </a>
                            {:else}
                                <button type="button" role="menuitem" class="ol-menuitem" onclick={() => chooseAddon(e)}>
                                    <span class="ol-addonicon"><span class="ol-addonmono" aria-hidden="true">{name.slice(0, 1).toUpperCase()}</span></span>
                                    <span class="ol-addonname">{name}</span>
                                </button>
                            {/if}
                            <!-- a `blank` entry's name already opens the new tab: its ↗ is for the mouse, not a second tab stop -->
                            <a class="ol-newtab" href={addonHref(e.href, e.legacy_session)} target="_blank" rel="noopener" title={t('Open in new tab')} aria-label={e.target === 'blank' ? undefined : t('Open in new tab')} aria-hidden={e.target === 'blank' ? 'true' : undefined} tabindex={e.target === 'blank' ? -1 : undefined} onclick={() => (open = false)}>↗</a>
                            <span class="ol-newtab ol-pin ol-pin-none" aria-hidden="true"></span>
                            {#if keptAddons.size > 0}<span class="ol-newtab ol-menuclose" aria-hidden="true"></span>{/if}
                            <span class="ol-newtab ol-linkend" aria-hidden="true"><Icon name="settings" size={14} /></span>
                        </div>
                    {/each}
                </div>
            {/if}
            {#if shownGroup.length + shownRest.length + shownLinks.length > 0}<div class="ol-menusep"></div>{/if}
            {#each ADDON_PAGES as n (n.path)}
                <a href={n.path} use:link role="menuitem" class="ol-menuitem ol-menuitem-dotted" class:active={selectedAddonPage?.path === n.path} onclick={() => (open = false)}
                    >{t(n.label)}{#if addonDot && n.path === '/addons'}<span class="ol-sysdot {addonDot}" role="img" aria-label={addonDot === 'error' ? t('Error') : t('Warning')} data-addon-dot={addonDot}></span>{/if}</a
                >
            {/each}
            {#if filter !== '' && shownGroup.length + shownRest.length + shownLinks.length === 0}
                <div class="ol-sysempty" role="note">{t('Nothing found')}</div>
            {/if}
        </div>
    </MenuPanel>
</div>
<!-- what a screen reader hears after a row was moved; outside the popup so it is never torn down -->
<div class="ol-sronly" role="status" aria-live="polite">{sort.live}</div>

<style>
    /* the word alone: the grid the breadcrumb button needed is one cell wide with one child, so
       the button is exactly as wide as the word plus its padding */
    .ol-addonsbtn { grid-template-columns: auto; }
    .ol-menuitem-dotted { display: flex; align-items: center; gap: 8px; }
    /* task 139: the popup is the System panel's content - its own chrome (app.css .ol-menupop:
       the position, the surface, the shadow) is switched off, the panel has all of that; the rows
       carry the icon in the System menu's 28 px square, in rows of 36 px (task 210). The panel is as
       wide as the rows need, capped, since a row carries four trailing controls where System's has one. */
    .ol-addonpop { position: static; top: auto; left: auto; min-width: 0; max-height: none; overflow: visible; padding: 0; background: none; border: 0; box-shadow: none; border-radius: 0; }
    :global(nav.ol-addonpanel.ol-syspop) { width: auto; min-width: 300px; max-width: calc(100vw - 16px); }
    /* task 210 (the maintainer: "too much spacing ... not the font, but the area that i see when
       hovering"): 36 px rows, the hover box 4 px above and below the 28 px icon - still well above
       WCAG 2.5.8's 24 px target on a phone */
    /* task 311 (the maintainer: the name's box too roomy, the actions crowded and of broken heights):
       the row keeps its 36 px with 2 px above and below; in it the entry is 32 px high with 2 px
       around the 28 px icon, and every action after it - ↗, the pin, ✕, ⚙ and the empty slots that keep
       the columns - is a 32 px square, 4 px apart and 4 px from the entry */
    .ol-addonpop :global(.ol-menurow) { min-height: 36px; padding: 2px 0; gap: 4px; align-items: center; }
    .ol-addonpop :global(.ol-menurow + .ol-menurow) { border-top: 1px solid var(--hmm-border-muted); }
    .ol-addonpop :global(.ol-menuitem), .ol-addonpop :global(a.ol-menuitem) { min-height: 32px; height: 32px; padding: 2px 6px; border-radius: 8px; font-size: 14px; }
    .ol-addonpop :global(.ol-menurow > .ol-newtab) {
        flex: 0 0 32px; box-sizing: border-box; width: 32px; height: 32px; padding: 0;
        display: flex; align-items: center; justify-content: center; border-radius: 8px;
    }
    .ol-addonpop :global(.ol-addonicon) { width: 28px; height: 28px; border-radius: 7px; display: inline-flex; align-items: center; justify-content: center; }
    .ol-addonpop :global(.ol-addonmono) { width: 28px; height: 28px; line-height: 28px; border-radius: 7px; font-size: 13px; }
    .ol-sysfilter { flex: 0 0 auto; display: flex; padding: 2px 2px 8px; }
    .ol-sysfilter :global(.ol-search-input) { height: 32px; border-radius: 8px; font-size: 14px; }
    .ol-sysempty { padding: 12px 10px; color: var(--hmm-fg-muted); font-size: 14px; }
    /* task 51: the ? of an inert addon row, centred in the row's height between the name and ↗ */
    .ol-menuhelp { display: flex; align-items: center; padding: 0 2px; }
    /* task 39: the ✕ of a kept addon page, and the empty slot that keeps ⚙ in line in the other rows */
    .ol-menuclose { width: 28px; padding: 0; justify-content: center; }
    button.ol-menuclose { border: 0; background: none; font: inherit; color: var(--hmm-fg-muted); cursor: pointer; }
    button.ol-menuclose:hover, button.ol-menuclose:focus-visible { color: var(--hmm-fg); background: var(--hmm-control-bg-hover); }
    /* the grab handle, the drop line and the live region: app.css (task 162) */
    /* the pin: outlined when the addon is not pinned, filled in the accent colour when it is */
    button.ol-pin { border: 0; background: none; font: inherit; padding: 0 6px; cursor: pointer; }
    button.ol-pin:hover, button.ol-pin:focus-visible { color: var(--hmm-fg); background: var(--hmm-control-bg-hover); }
    /* the pinned state is the same square, filled - not a box of its own size (task 311) */
    button.ol-pin.ol-pinned { color: var(--hmm-accent); background: var(--hmm-accent-bg); }
    button.ol-pin.ol-pinned :global(svg) { fill: currentColor; }
    .ol-pin-none { width: 26px; padding: 0; }
    /* a link row has no ⚙: the slot is the icon made invisible, so it is exactly as wide */
    .ol-linkend { visibility: hidden; }
    /* a pinned addon's tab: the favicon, where the frontend has one, at text size before its name */
    :global(.ol-nav a.ol-pintab) { display: inline-flex; align-items: center; gap: 6px; max-width: 200px; }
    /* a folded tab: laid out for its width, out of the flow, unseen and unreachable */
    :global(.ol-nav a.ol-pintab-folded) { position: absolute; visibility: hidden; pointer-events: none; }
    .ol-pintab-icon { flex: 0 0 auto; display: flex; align-items: center; justify-content: center; width: 14px; height: 14px; }
    .ol-pintab-icon img { width: 14px; height: 14px; object-fit: contain; }
    .ol-pintab-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
    @media (prefers-reduced-motion: no-preference) {
        .ol-menurow-front { transition: box-shadow 80ms linear; }
    }
</style>
