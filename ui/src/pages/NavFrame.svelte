<script lang="ts">
    import {untrack} from 'svelte';
    import {postLook, theme} from '../lib/theme.svelte';
    import {withLook} from '../lib/addonurl';
    import type {NavEntry} from '../lib/api';
    import {i18n} from '../lib/i18n.svelte';
    import {addonHref, auth, ensureLegacySid} from '../lib/auth.svelte';
    import {frames, keepFrame, useFrame} from '../lib/frames.svelte';
    import {frontendKey, keepable} from '../lib/framekeep';

    // keep_alive: a nav.d drop-in that asks the shell to keep its page loaded (internal/system/nav.go)
    let {entry}: {entry: NavEntry & {keep_alive?: boolean}} = $props();
    // Task 125: an entry the box marks legacy_session gets the session's alias as ?sid=@..@ - an addon
    // that lives by the CCU convention, or a nav.d page under /addons/ while the switch is on; the
    // alias is asked for once and the page waits for it, so the frame loads once, with the URL it keeps.
    const needsAlias = $derived(!!entry.legacy_session && !auth.legacySid);
    $effect(() => {
        if (entry.legacy_session) void ensureLegacySid();
    });
    // B-200: theme= and lang= on the URL as on the settings frame (the embedding contract), read
    // once - a theme or language change reaches the page by message, it does not reload it
    const url = $derived(withLook(addonHref(entry.href, entry.legacy_session), untrack(() => theme.value), untrack(() => i18n.language)));
    const label = $derived(entry.label[i18n.language] ?? entry.label.en ?? entry.id);

    // Task 39: an addon's frontend is kept loaded (lib/frames.svelte.ts) and shown by
    // lib/FrameHost.svelte; this page only hands it over. A nav.d page stays this page's own iframe
    // unless its drop-in asks.
    const keep = $derived(keepable(entry));
    const key = $derived(frontendKey(entry.id));
    const isKept = $derived(frames.list.some((f) => f.key === key));
    // runs again when the kept page is dropped while it shows (its addon changed): a fresh one
    $effect(() => {
        if (!keep || needsAlias) return;
        const k = key;
        if (isKept) {
            untrack(() => useFrame(k));
            return;
        }
        // the page follows its addon (task 88: the addon id, which the route id need not be)
        untrack(() => keepFrame({key: k, src: url, title: label, addon: entry.source === 'addon' ? (entry.addon ?? entry.id) : '', detectBlocked: false}));
    });
</script>

<!-- 28.4: no bar above the page; the new-tab link is in the Addons dropdown -->
{#if !keep && !needsAlias}
    <iframe class="ol-frame" data-addon={entry.addon ?? entry.id} src={url} title={label} onload={(e) => postLook((e.currentTarget as HTMLIFrameElement).contentWindow)}></iframe>
{/if}
