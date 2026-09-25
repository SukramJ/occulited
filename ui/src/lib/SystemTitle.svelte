<script lang="ts">
    /*
     * Task 57 (D-68): a system page's heading is the page title switcher - *System › Firewall ▾*.
     * The crumb says where the page lives, the button opens the same System menu the tab opens,
     * anchored under the title (the bottom sheet on a phone), with this page checked. A page puts
     * what it had in its h1 besides the name - a ? with the page's explanation - into the children.
     * The heading's accessible name is what a reader sees: "System › Firewall".
     */
    import type {Snippet} from 'svelte';
    import {t} from './i18n.svelte';
    import TokenNotice from './TokenNotice.svelte';
    import {router} from './router.svelte';
    import {opensFilter, systemPageAt} from './systemmenu';
    import {systemMenu, toggleSystemMenu} from './systemmenu.svelte';

    let {children}: {children?: Snippet} = $props();
    const page = $derived(systemPageAt(router.path));
    let button = $state<HTMLButtonElement | null>(null);
</script>

<h1 class="ol-systitle">
    <span class="ol-systitle-crumb">{t('System')} ›</span>
    <button
        type="button"
        class="ol-systitle-btn"
        aria-haspopup="menu"
        aria-expanded={systemMenu.open && systemMenu.anchor === button}
        bind:this={button}
        onclick={(ev) => toggleSystemMenu(button, opensFilter(ev))}
        aria-keyshortcuts="Control+K Meta+K"
    >
        {page ? t(page.label) : ''}<span class="ol-caret" aria-hidden="true">▾</span>
    </button>
    {#if children}{@render children()}{/if}
</h1>
<!-- B-174: a token session is read-only here; the notice says so on every system page -->
<TokenNotice />

<style>
    .ol-systitle { display: flex; align-items: center; flex-wrap: wrap; gap: 0 6px; }
    .ol-systitle-crumb { color: var(--hmm-fg-muted); font-weight: 500; }
    /* the button reads as the heading's own text; the caret and the hover say it opens something */
    .ol-systitle-btn {
        display: inline-flex; align-items: center; gap: 5px; margin: 0 0 0 -4px; padding: 1px 6px 1px 4px;
        border: 0; border-radius: var(--hmm-radius); background: none; color: inherit; font: inherit; cursor: pointer;
    }
    .ol-systitle-btn:hover, .ol-systitle-btn[aria-expanded='true'] { background: var(--hmm-control-bg-hover); }
    .ol-systitle-btn .ol-caret { font-size: 13px; }
</style>
