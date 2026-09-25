<script lang="ts">
    /*
     * The global switch of the legacy session in addon URLs (task 125): on the Security page first,
     * on System → Users with the login settings in task 132 (D-87), and on the Addons page since the
     * maintainer's follow-up (2026-09-16), next to the per-addon switches in the list's ⋯ menus. The
     * page shows it to an administrator only. `view` is the page's own copy of the switches: the
     * section loads it and writes it back when it saves, and shows the page's per-addon changes;
     * `onsaved` lets the page read its addon list again, whose `legacy_session` marks the box decides.
     */
    import {onMount} from 'svelte';
    import {api} from './api';
    import {t} from './i18n.svelte';
    import Loading from './Loading.svelte';
    import Help from './Help.svelte';
    import {scrollToAnchor} from './anchor';

    // ---- the legacy session in addon URLs (task 125, D-77) ---------------------------------
    // The CCU convention hands an addon page the session as ?sid=@..@, and the addons' CGIs live by
    // it. What the shell puts there is the session's alias - never the session, which the API alone
    // takes - and only for addons that do not read the session header; this switch turns it off for
    // all of them at once, the Addons page per addon. Off, their pages refuse to open.
    interface LegacyView { enabled: boolean; off: string[] }
    let {view: legacy = $bindable(null), onsaved}: {view?: LegacyView | null; onsaved?: () => void} = $props();
    let legacyOn = $state(true);
    let legacyError = $state('');
    let legacyNotice = $state('');
    let legacyBusy = $state(false);
    let legacyUnavailable = $state(false);
    async function loadLegacy() {
        try {
            legacy = await api.get<LegacyView>('/api/system/v1/legacy-session');
            legacyOn = legacy.enabled;
        } catch (e) {
            const msg = (e as Error).message;
            if (msg.includes('501') || msg.includes('configuration file')) legacyUnavailable = true;
            else legacyError = msg;
        }
    }
    async function saveLegacy() {
        legacyBusy = true;
        legacyNotice = '';
        try {
            legacy = await api.put<LegacyView>('/api/system/v1/legacy-session', {enabled: legacyOn});
            legacyOn = legacy.enabled;
            legacyNotice = t('Saved.');
            onsaved?.();
        } catch (e) {
            legacyNotice = (e as Error).message;
        } finally {
            legacyBusy = false;
        }
    }

    onMount(async () => {
        await loadLegacy();
        scrollToAnchor('addon-sessions');
    });
</script>

<section class="ol-section" data-section="addon-sessions">
    <h2 id="addon-sessions">{t('Addon sessions')}</h2>
    {#if legacyUnavailable}
        <p class="ol-muted">{t('This daemon runs without a configuration file; the switch cannot be changed here.')}</p>
    {:else if !legacy}
        <Loading error={legacyError} />
    {:else}
        {#if legacyNotice}<div class="ol-notice">{legacyNotice}</div>{/if}
        <div class="ol-checks">
            <label><input type="checkbox" bind:checked={legacyOn} /> <strong>{t('Pass the session in addon URLs (legacy CCU convention)')}</strong><Help><p>{t('Addons written for the CCU expect the session as ?sid=@…@ in the URL of their pages, and their scripts check it that way. What goes there is not your session but an alias of it: ten characters that only addon pages accept, never the API, and that end with your session.')}</p><p>{t('Only addons that do not read the session header get it; the list above marks them, and its ⋯ menu switches it off per addon. Switched off here, none gets it, and their pages refuse to open until the addon reads the header.')}</p></Help></label>
            {#if !legacyOn}
                <div class="ol-warn sub">{t('The settings pages of addons that do not read the session header will refuse to open.')}</div>
            {:else if legacy.off.length > 0}
                <div class="ol-muted sub">{t('Switched off for: {list}', {list: legacy.off.join(', ')})}</div>
            {/if}
        </div>
        <div class="ol-actions" style="margin-top:10px">
            <button class="hmm-button primary" onclick={saveLegacy} disabled={legacyBusy || legacyOn === legacy.enabled}>{t('Save')}</button>
        </div>
    {/if}
</section>

<style>
    .ol-checks { display: flex; flex-direction: column; gap: 8px; margin: 8px 0 4px; max-width: 760px; }
    .ol-checks label { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
    .ol-checks .sub { margin-left: 26px; font-size: var(--hmm-font-size-small); }
</style>
