<script lang="ts">
    // An addon's settings page (its Config-Url) in the shell's frame, at /addon-settings/<id>. This
    // page finds it and hands it to lib/frames.svelte.ts, and lib/FrameHost.svelte shows it - but
    // unlike a frontend it is not kept: it does not count towards the kept frontends and goes as
    // soon as another page is shown (leaveFrames in App.svelte).
    import {untrack} from 'svelte';
    import {api, type Addon} from '../lib/api';
    import {t, i18n} from '../lib/i18n.svelte';
    import Loading from '../lib/Loading.svelte';
    import {addonHref, ensureLegacySid} from '../lib/auth.svelte';
    import {withLook} from '../lib/addonurl';
    import {theme} from '../lib/theme.svelte';
    import {replace} from '../lib/router.svelte';
    import {frames, keepFrame, noteAddons, useFrame} from '../lib/frames.svelte';
    import {settingsKey} from '../lib/framekeep';

    let {id}: {id: string} = $props();
    const key = $derived(settingsKey(id));
    const kept = $derived(frames.list.find((f) => f.key === key));
    const isKept = $derived(kept !== undefined);
    let error = $state('');

    // runs again when the kept page is dropped while it shows (its addon changed): a fresh one
    $effect(() => {
        const k = key;
        const addonId = id;
        if (isKept) {
            untrack(() => useFrame(k));
            return;
        }
        let cancelled = false;
        untrack(() => (error = ''));
        void (async () => {
            try {
                const all = (await api.get<{addons: Addon[]}>('/api/system/v1/addons')).addons;
                if (cancelled) return;
                noteAddons(all);
                const addon = all.find((a) => a.id === addonId);
                if (!addon) {
                    error = t('Not installed');
                    return;
                }
                const base = addon.config_url || addon.settings?.config_url || '';
                if (!base) {
                    // no settings page of its own: its row on Installed addons, as ⚙ does
                    replace(`/addons?addon=${encodeURIComponent(addonId)}`);
                    return;
                }
                // The CCU convention's ?sid=@..@ carries the session's legacy alias, asked for here,
                // and only for an addon the box marks legacy_session (task 125: not one that reads
                // the gate's session header, task 88, D-67); besides it the frame gets theme= and
                // lang= for its first paint, and changes by postMessage - the embedding contract in
                // docs/system-api.md. Read once: a kept page does not reload for a theme change.
                if (addon.legacy_session) await ensureLegacySid();
                if (cancelled) return;
                const sid = addonHref(base, addon.legacy_session);
                const src = withLook(sid, theme.value, i18n.language);
                keepFrame({key: k, src, title: addon.name || addonId, addon: addonId, detectBlocked: true});
            } catch (e) {
                if (!cancelled) error = (e as Error).message;
            }
        })();
        return () => {
            cancelled = true;
        };
    });
</script>

<!-- 28.4: no bar above the addon - the page is the addon. "Open in new tab" lives in the
     Addons dropdown; the one case that still needs a link here is a frame that refuses embedding. -->
{#if kept?.blocked}
    <div class="ol-notice" style="margin:14px">{t('This addon refuses to be embedded; open it in a new tab.')} <a href={kept.src} target="_blank" rel="noopener">{t('Open in new tab')} ↗</a></div>
{:else if !kept}
    <div style="padding:14px"><Loading {error} /></div>
{/if}
