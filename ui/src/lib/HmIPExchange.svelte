<script lang="ts">
    /*
     * The radio-exchange diagnosis (task 155, D-106): HmIP-RF stopped because eQ-3's key server
     * rejected the move of this system's HmIP network to the module in use. One log line, two
     * causes, told apart by hmipserver's own output (the system probes nothing):
     *   - the key server was never reached: a network or DNS matter; the page offers a retry, which
     *     restarts HmIP-RF - the move is tried at every start;
     *   - the key server answered and refused this module: the network cannot move to it, whatever
     *     the network connection does. The page names the two ways out - the previous module back,
     *     or the guided fresh start: the previous module's identity is moved aside and kept, local
     *     key mode can be switched on in the same step, then HmIP-RF starts with an empty network
     *     and every HmIP device is paired again. Administrators only, confirmed by typing the
     *     system's host name (the factory reset's shape).
     * A marker written before the causes existed has neither and gets the sentence that names both.
     */
    import {onMount} from 'svelte';
    import {api} from './api';
    import {askText} from './dialog.svelte';
    import {t} from './i18n.svelte';
    import {sameHostname} from './factoryreset';

    interface Fatal { code: string; line: string; adapter?: string; cause?: string }
    interface View { fatal?: Fatal; module?: string; previous: string[]; replaces_snapshots?: string[]; local_key: boolean; exchange_id: boolean; switching?: string; error?: string; hostname: string }

    let {fatal, admin = false, ondone}: {fatal: Fatal; admin?: boolean; ondone?: (kind: 'retry' | 'fresh-start') => void} = $props();

    let view = $state<View | null>(null);
    let localKey = $state(true);
    let busy = $state('');
    let error = $state('');

    async function load() {
        if (!admin) return;
        try {
            view = await api.get<View>('/api/system/v1/radio/hmip/exchange');
        } catch (e) {
            error = (e as Error).message;
        }
    }
    onMount(() => void load());

    const adapter = $derived(fatal.adapter ?? view?.module ?? '');
    const previous = $derived((view?.previous ?? []).join(', '));

    async function retry() {
        busy = 'retry';
        error = '';
        try {
            await api.post('/api/system/v1/radio/hmip/exchange/retry', {});
            ondone?.('retry');
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    async function freshStart() {
        if (!view) return;
        error = '';
        const message = [
            previous ? t('The HmIP identity of the previous module {previous} is moved aside and kept, nothing is deleted.', {previous}) : t('The previous HmIP identity is moved aside and kept, nothing is deleted.'),
            t('This module then starts with an empty HmIP network: every HmIP device has to be reset and paired again, and the direct links between devices go with that.'),
            localKey && !view.local_key ? t("Local key mode is switched on in the same step: the new network's key stays on this system, so a later module swap never needs eQ-3's key server.") : '',
            view.replaces_snapshots?.length ? t('The kept identity of {sgtin} from before is replaced by this one.', {sgtin: view.replaces_snapshots.join(', ')}) : '',
            t('Type the host name {host} to confirm.', {host: view.hostname}),
        ].filter(Boolean).join('\n\n');
        const typed = await askText({
            title: t('Start fresh with this module?'),
            message,
            input: {label: t('Host name'), placeholder: view.hostname},
            confirm: t('Start fresh'),
            danger: true,
            focusCancel: true,
        });
        if (typed === null) return;
        if (!sameHostname(typed, view.hostname)) {
            error = t('The name typed is not this system\'s host name; nothing happened.');
            return;
        }
        busy = 'fresh-start';
        try {
            await api.post('/api/system/v1/radio/hmip/exchange/fresh-start', {confirm: true, hostname: typed, local_key: localKey && !view.local_key});
            ondone?.('fresh-start');
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }
</script>

<div class="ol-notice error hx" data-notice="hmip-fatal" data-cause={fatal.cause ?? 'unknown'}>
    {#if fatal.cause === 'unreachable'}
        <p>{t("HmIP-RF is down: the system could not reach eQ-3's key server, which has to move the HmIP network to the module {adapter}. Check the network connection and the DNS settings; the move is tried again at every start of HmIP-RF.", {adapter})}</p>
        {#if admin}
            <div class="ol-actions"><button type="button" class="hmm-button" disabled={!!busy || !!view?.switching} onclick={retry}>{t('Try again')}</button></div>
        {/if}
    {:else if fatal.cause === 'refused'}
        <p>{t("HmIP-RF is down: eQ-3's key server does not know the module {adapter}, so the HmIP network of this system cannot move to it, whatever the network connection does.", {adapter})}
            {#if previous}{t('The network belongs to the previous module {previous}.', {previous})}{/if}</p>
        <p>{t("Two ways out: put the previous module back, or start fresh with this module. Starting fresh moves the previous module's HmIP identity aside - it is kept, nothing is deleted - and gives this module an empty network: every HmIP device has to be reset and paired again.")}</p>
        {#if admin && view}
            {#if view.local_key}
                <p class="ol-muted">{t("Local key mode is on already: the new network uses this system's key and never depends on eQ-3's key server.")}</p>
            {:else}
                <label class="hx-choice"><input type="checkbox" bind:checked={localKey} disabled={view.exchange_id || !!busy} />
                    {t("Switch to local key mode in the same step (recommended): the new network's key stays on this system, so a later module swap never needs eQ-3's key server.")}</label>
                {#if view.exchange_id}<p class="ol-muted">{t('hmip_address.conf carries accesspoint.exchange.id: hmipserver ignores a configured key for this module, so local key mode cannot work here.')}</p>{/if}
            {/if}
            <div class="ol-actions"><button type="button" class="hmm-button danger" disabled={!!busy || !!view.switching || !view.previous.length} onclick={freshStart}>{t('Start fresh with this module…')}</button></div>
            {#if !view.previous.length}<p class="ol-muted">{t("No previous module's identity is on the system, so there is nothing to move aside; a restart of HmIP-RF tries the move again.")}</p>{/if}
        {/if}
    {:else}
        <p>{t("HmIP-RF is down: eQ-3's key server rejected the adapter exchange to {adapter}. This system's HmIP devices belong to the adapter it was set up with, and the key server does not hand them to this one. Put the previous adapter back, or set HmIP up afresh (all HmIP devices have to be paired again).", {adapter})}</p>
    {/if}
    {#if view?.switching}<p class="ol-muted" data-switching={view.switching}><span class="ol-dot starting"></span>{t('HmIP-RF is restarting after the radio exchange.')}</p>{/if}
    {#if error || view?.error}<p class="ol-warn">{error || view?.error}</p>{/if}
</div>

<style>
    .hx p { margin: 0 0 8px; }
    .hx p:last-child { margin-bottom: 0; }
    .hx-choice { display: flex; align-items: flex-start; gap: 8px; margin: 8px 0; }
    .hx-choice input { margin-top: 3px; flex: none; }
</style>
