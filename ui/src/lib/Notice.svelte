<script lang="ts">
    /*
     * A notice with the way to the page where the matter is handled (task 53). The Status page's
     * warnings used to *name* the page in words ("the Backup page sets the directory") and link
     * nowhere; now the sentence says what is wrong and the button at its end says where to go.
     * The link is a prop so it cannot be forgotten: a warning about something the user can
     * change names its page here. The link is shown to anyone who can open the page; the pages
     * keep their own admin checks.
     *
     * Task 81: `warning` is the amber edge of a warning that is not an error (the default security
     * key, a storage device to watch), and `actions` are the page's own buttons after the link
     * (the Status page's Silence and Fix ownership).
     *
     * Task 203 (the maintainer, 2026-09-22): a notice is an `.ol-panel` like every other section of
     * the UI, and its severity is the card's 4 px left edge, not a coloured line all the way round a
     * sunken box. Only this component changed; the inline `.ol-notice` used on nearly every page for
     * hints, results and empty states is a hint *inside* a panel and stays what it was.
     */
    import type {Snippet} from 'svelte';
    import {link} from './router.svelte';

    interface Props {
        /** `error` for the red left edge, `warning` for the amber one; plain otherwise */
        kind?: 'error' | 'warning' | 'plain';
        /** the page the matter is handled on, an in-app path (`/system/backup`, `/system/log?since=…`, or one of the paths the system pages had before, which the router rewrites) */
        href?: string;
        /** the button's label - the page's name */
        label?: string;
        /** a `data-notice` handle for the tests, naming the warning */
        id?: string;
        /** buttons after the link */
        actions?: Snippet;
        children: Snippet;
    }
    let {kind = 'plain', href = '', label = '', id = '', actions, children}: Props = $props();
</script>

<div class="ol-panel ol-notice-panel" class:err={kind === 'error'} class:warn={kind === 'warning'} class:ol-notice-link={!!href || !!actions} data-notice={id || undefined} data-severity={kind === 'plain' ? undefined : kind}>
    <div class="ol-notice-text">{@render children()}</div>
    {#if href || actions}
        <div class="ol-notice-actions">
            {#if href}
                <a class="hmm-button" {href} use:link>{label}</a>
            {/if}
            {@render actions?.()}
        </div>
    {/if}
</div>

<style>
    .ol-notice-actions { display: flex; flex: 0 0 auto; flex-wrap: wrap; align-items: center; gap: 6px; }
</style>
