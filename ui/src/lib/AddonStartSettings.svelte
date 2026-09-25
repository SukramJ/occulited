<script lang="ts">
    /*
     * Task 119 (D-75): the global switch of the early addon start, on the Addons page under the list,
     * next to the per-addon switches in the list's ⋯ menus. An addon whose catalogue entry declares
     * runtime.start "early" copes with radio interfaces that are not ready yet, so the box starts it
     * before them; this switch turns that off for all of them. A change takes effect at the next
     * boot, and nothing is restarted. `view` is the page's copy of the switches, as in
     * AddonSessionSettings; `onsaved` lets the page read its addon list again.
     */
    import {onMount} from 'svelte';
    import {api} from './api';
    import {t} from './i18n.svelte';
    import Loading from './Loading.svelte';
    import Help from './Help.svelte';
    import {scrollToAnchor} from './anchor';

    interface EarlyView { enabled: boolean; off: string[] }
    let {view: early = $bindable(null), onsaved}: {view?: EarlyView | null; onsaved?: () => void} = $props();
    let earlyOn = $state(true);
    let error = $state('');
    let notice = $state('');
    let busy = $state(false);
    let unavailable = $state(false);
    async function loadEarly() {
        try {
            early = await api.get<EarlyView>('/api/system/v1/early-start');
            earlyOn = early.enabled;
        } catch (e) {
            const msg = (e as Error).message;
            if (msg.includes('501') || msg.includes('configuration file')) unavailable = true;
            else error = msg;
        }
    }
    async function save() {
        busy = true;
        notice = '';
        try {
            early = await api.put<EarlyView>('/api/system/v1/early-start', {enabled: earlyOn});
            earlyOn = early.enabled;
            notice = t('Saved. Takes effect at the next boot.');
            onsaved?.();
        } catch (e) {
            notice = (e as Error).message;
        } finally {
            busy = false;
        }
    }

    onMount(async () => {
        await loadEarly();
        scrollToAnchor('addon-start');
    });
</script>

<section class="ol-section" data-section="addon-start">
    <h2 id="addon-start">{t('Addon start')}</h2>
    {#if unavailable}
        <p class="ol-muted">{t('This daemon runs without a configuration file; the switch cannot be changed here.')}</p>
    {:else if !early}
        <Loading error={error} />
    {:else}
        {#if notice}<div class="ol-notice ad-early-notice">{notice}</div>{/if}
        <div class="ol-checks">
            <label><input type="checkbox" class="ad-early-global" bind:checked={earlyOn} /> <strong>{t('Start addons early when they support it')}</strong><Help><p>{t('Such an addon has declared that it copes with radio interfaces that are not ready yet: it retries within seconds and logs no errors while it waits. The system starts it before the radio interfaces, so it is ready sooner after a reboot. Addons without that declaration always wait for the interfaces.')}</p><p>{t('The ⋯ menu of such an addon switches it off for that addon alone.')}</p></Help></label>
            <div class="ol-muted sub">{t('Such addons start before the radio interfaces are ready and connect when they are.')}</div>
            {#if !earlyOn}
                <div class="ol-muted sub">{t('Every addon waits for the radio interfaces.')}</div>
            {:else if early.off.length > 0}
                <div class="ol-muted sub">{t('Switched off for: {list}', {list: early.off.join(', ')})}</div>
            {/if}
            <div class="ol-muted sub ad-early-nextboot">{t('A change takes effect at the next boot.')}</div>
        </div>
        <div class="ol-actions" style="margin-top:10px">
            <button class="hmm-button primary ad-early-save" onclick={save} disabled={busy || earlyOn === early.enabled}>{t('Save')}</button>
        </div>
    {/if}
</section>

<style>
    .ol-checks { display: flex; flex-direction: column; gap: 8px; margin: 8px 0 4px; max-width: 760px; }
    .ol-checks label { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
    .ol-checks .sub { margin-left: 26px; font-size: var(--hmm-font-size-small); }
</style>
