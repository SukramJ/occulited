<script lang="ts">
    // Task 149 (D-103): local key mode - the HmIP network key kept on this system instead of at eQ-3's
    // key server, so a later radio module swap needs no internet. The section sits beside rfd's
    // Security key, so both radio keys are in one place. Switching restarts HmIP-RF at once (the
    // key is written into the module when hmipserver starts); the identity from before is kept
    // aside until the user discards it, and a wrong entered key is caught by watching the devices.
    // The keys themselves never come back from the box.
    import {onMount} from 'svelte';
    import {api} from './api';
    import {ask} from './dialog.svelte';
    import {t} from './i18n.svelte';
    import {scrollToAnchor} from './anchor';
    import Disclosure from './Disclosure.svelte';
    import Help from './Help.svelte';

    // total: the devices watched (those that answered before the switch); heard: answering again
    interface Check { started: string; finished?: string; state: 'waiting' | 'running' | 'ok' | 'failed' | 'skipped' | 'error' | 'interrupted' | 'superseded'; before: number; total: number; heard: number; unreachable: string[]; quiet: string[]; error?: string }
    interface Snapshot { sgtin: string; at: string; files: string[]; kind?: string }
    interface Status {
        available: boolean; sgtin?: string; enabled: boolean; source?: 'entered' | 'generated' | 'manual'; keyserver_mode: string;
        exchange_id: boolean; snapshots: Snapshot[]; revert_blocked?: string; override_active: boolean; override_since?: string;
        check?: Check; switching?: string; error?: string;
    }

    let st = $state<Status | null>(null);
    let err = $state('');
    let open = $state(false);
    let how = $state<'' | 'known' | 'generate'>('');
    let nwk = $state('');
    let bbk = $state('');
    let poll: ReturnType<typeof setTimeout> | undefined;

    const busy = $derived(!!st?.switching);
    const watching = $derived(st?.check?.state === 'waiting' || st?.check?.state === 'running');
    const hex32 = (s: string) => /^[0-9a-fA-F]{32}$/.test(s.replace(/[\s:-]/g, ''));
    const keyOk = $derived(hex32(nwk) && (bbk.trim() === '' || hex32(bbk)));

    let scrolled = false;
    async function load() {
        try {
            st = await api.get<Status>('/api/system/v1/radio/hmip/local-key');
            err = '';
        } catch (e) {
            err = (e as Error).message;
        }
        // task 183: the Status page's warning and the old Interfaces anchor land here once it is drawn
        if (!scrolled && st) {
            scrolled = true;
            scrollToAnchor('local-key');
        }
        clearTimeout(poll);
        if (st?.switching || st?.check?.state === 'waiting' || st?.check?.state === 'running') poll = setTimeout(load, 2000);
    }

    function sourceText(s: Status): string {
        return s.source === 'generated' ? t('generated on this system') : s.source === 'entered' ? t('entered') : t('set by hand in hmip_user.conf');
    }

    async function switchOn() {
        if (!how) return;
        const body = how === 'known' ? {mode: 'known', network_key: nwk, backbone_key: bbk} : {mode: 'generate'};
        const message = [
            how === 'known'
                ? t('The key is written into the radio module. If it is the network\'s real key, every HmIP device keeps working and nothing has to be paired again. If it is not, the devices go silent - the system watches them for ten minutes after the switch and says so, with the way back beside it.')
                : t('A new random key is written into the radio module. Every HmIP device has to be taught in again once, one by one, with its button (physical access to each). Until then it does not answer.'),
            t('HmIP-RF restarts at once; the radio is unavailable for about a minute.'),
            t('The module\'s identity from before is kept aside, so "Back to eQ-3\'s key server" can undo this later.'),
        ].join('\n\n');
        await ask({
            title: t('Switch to local key mode?'),
            message,
            confirm: t('Switch'),
            danger: how === 'generate',
            focusCancel: how === 'generate',
            run: async () => {
                st = await api.put<Status>('/api/system/v1/radio/hmip/local-key', body);
                nwk = bbk = '';
                how = '';
                open = false;
                await load();
            },
        });
    }

    async function switchOff() {
        await ask({
            title: t("Back to eQ-3's key server?"),
            message: [
                t('The radio module\'s identity from before the switch is put back, and the key lines leave hmip_user.conf. HmIP-RF restarts at once.'),
                t('Devices that were taught in under the local key since then have to be taught in again.'),
            ].join('\n\n'),
            confirm: t('Go back'),
            danger: true,
            run: async () => {
                st = await api.del<Status>('/api/system/v1/radio/hmip/local-key');
                await load();
            },
        });
    }

    async function override(on: boolean) {
        try {
            st = await api.post<Status>('/api/system/v1/radio/hmip/local-key/override', {on});
            await load();
        } catch (e) {
            err = (e as Error).message;
        }
    }

    async function discard(s: Snapshot) {
        await ask({
            title: t('Discard the snapshot of {sgtin}?', {sgtin: s.sgtin}),
            message: t('It holds this module\'s identity from before the switch to local key mode, key material included. Without it the way back to eQ-3\'s key server is gone.'),
            confirm: t('Discard'),
            danger: true,
            focusCancel: true,
            run: async () => {
                st = await api.del<Status>(`/api/system/v1/radio/hmip/local-key/snapshots/${encodeURIComponent(s.sgtin)}`);
            },
        });
    }

    onMount(() => {
        void load();
        return () => clearTimeout(poll);
    });
</script>

<h2 id="local-key">{t('Local key mode')}<Help>{t("HmIP devices share one network key. Normally it is locked in the radio module, and eQ-3's key server is needed to move it to another module and to pair a device without its key. In local key mode the key is kept on this system: a later radio module swap works without eQ-3's key server and without the internet, and devices are paired with the key from their QR code.")}</Help> <span class="ol-muted">· HmIP-RF</span></h2>
{#if err}<div class="ol-warn">{err}</div>{/if}
{#if st}
    {#if st.error}<div class="ol-notice error" data-notice="local-key-error">{st.error}</div>{/if}
    {#if st.check?.state === 'failed'}
        <div class="ol-notice error" data-notice="local-key-check">{t('{n} of {total} HmIP devices have not answered since the switch to local key mode: the key was probably not the network\'s.', {n: st.check.total - st.check.heard, total: st.check.total})}
            {#if !st.revert_blocked}<button type="button" class="hmm-button" onclick={switchOff} disabled={busy}>{t("Back to eQ-3's key server")}</button>{/if}
        </div>
    {/if}
    {#if !st.available && !st.enabled}
        <p class="ol-muted">{t('No HmIP radio module is in use.')}</p>
    {:else}
        <p data-state={st.enabled ? 'on' : 'off'}>
            {#if st.enabled}
                <strong>{t('On')}:</strong> {t('the HmIP network key is kept on this system ({source}).', {source: sourceText(st)})}
                {st.keyserver_mode === 'LOCAL' ? t("Radio module swaps and pairing work without eQ-3's key server; a device is paired with the key from its QR code.") : t("eQ-3's key server may be asked for the next pairing, for a device whose key is not known here.")}
            {:else}
                <strong>{t('Off')}:</strong> {t("swapping the radio module needs eQ-3's key server once. Pairing a device without its key (from its QR code or under HmIP device keys) needs it too.")}
            {/if}
        </p>
        {#if busy}<div class="ol-notice"><span class="ol-dot starting"></span>{st.switching === 'off' ? t('Going back to eQ-3\'s key server; HmIP-RF is restarting.') : st.switching === 'override' ? t('HmIP-RF is restarting with the new key-server setting.') : st.switching === 'retry' || st.switching === 'fresh-start' ? t('HmIP-RF is restarting after the radio exchange.') : t('Switching to local key mode; HmIP-RF is restarting.')}</div>{/if}
        {#if watching && st.check}
            <p class="ol-muted" data-check="running">{st.check.state === 'waiting' ? t('Waiting for HmIP-RF to answer, then the devices are watched for ten minutes.') : t('Watching the devices after the switch: {n} of {total} not answering so far.', {n: st.check.total - st.check.heard, total: st.check.total})}</p>
        {:else if st.check?.state === 'ok'}
            <p class="ol-muted" data-check="ok">{t('All {total} HmIP devices answered after the switch.', {total: st.check.total})}</p>
        {:else if st.check?.state === 'skipped' && st.check.before > 0}
            <p class="ol-muted" data-check="skipped">{t('No HmIP device had answered HmIP-RF before the switch, so there was nothing to compare.')}</p>
        {:else if st.check?.state === 'error'}
            <p class="ol-muted" data-check="error">{t('HmIP-RF did not answer after the switch, so the devices could not be checked.')}</p>
        {/if}
        {#if st.exchange_id}<div class="ol-notice error" data-notice="exchange-id">{t('hmip_address.conf carries accesspoint.exchange.id: hmipserver ignores a configured key for this module, so local key mode cannot work here.')}</div>{/if}

        {#if !st.enabled}
            <Disclosure label={t('Switch to local key mode')} title={t('Local key mode')} bind:open>
                <div class="ol-form lk-form">
                    <fieldset>
                        <legend>{t('Where the key comes from')}</legend>
                        <label><input type="radio" name="lk-how" value="known" bind:group={how} /> {t('Enter the network\'s key')}</label>
                        <label><input type="radio" name="lk-how" value="generate" bind:group={how} /> {t('Generate a new key')}</label>
                    </fieldset>
                    {#if how === 'known'}
                        <p class="ol-muted">{t('With the network\'s real key every HmIP device keeps working, and nothing is paired again.')}</p>
                        <label>{t('Network key')} <input class="hmm-input hmm-mono" bind:value={nwk} placeholder="32 hex" autocomplete="off" spellcheck="false" aria-label={t('Network key')} /></label>
                        <label>{t('Backbone key (optional)')} <input class="hmm-input hmm-mono" bind:value={bbk} placeholder="32 hex" autocomplete="off" spellcheck="false" aria-label={t('Backbone key (optional)')} /></label>
                    {:else if how === 'generate'}
                        <div class="ol-notice" data-notice="generate-cost">{t('Every HmIP device already paired has to be taught in again once, one by one, with its button.')}</div>
                    {/if}
                    <ul class="lk-costs">
                        <li>{t('The key then lies in a file on this system and in every backup. Whoever has it can join the network, so protect the backups.')}</li>
                        <li>{t('Pairing still works offline: from a scanned device key or with the key from the device\'s sticker. A device whose sticker is lost cannot be paired.')}</li>
                        <li>{t('eQ-3 treats a configured key as a test feature; it is checked again with every firmware update of this system.')}</li>
                    </ul>
                    <div class="ol-actions"><button type="button" class="hmm-button primary" disabled={!how || busy || st.exchange_id || !st.available || (how === 'known' && !keyOk)} onclick={switchOn}>{t('Switch to local key mode')}</button></div>
                </div>
            </Disclosure>
        {:else}
            <div class="ol-actions lk-actions">
                <button type="button" class="hmm-button" disabled={busy || !!st.revert_blocked} onclick={switchOff} title={st.revert_blocked ?? ''}>{t("Back to eQ-3's key server")}</button>
            </div>
            {#if st.revert_blocked}<p class="ol-muted" data-note="revert-blocked">{t('Not possible now: {reason}', {reason: st.revert_blocked})}</p>{/if}
            <label class="lk-override">
                <input type="checkbox" checked={st.override_active} disabled={busy} onchange={(e) => override((e.currentTarget as HTMLInputElement).checked)} />
                {t('Allow the key server for the next pairing')}
            </label>
            <p class="ol-muted lk-override-note">{st.override_active ? t('On until the next install mode has ended, at the latest 30 minutes. HmIP-RF restarted for it.') : t('For a device whose key is not at hand: its pairing then asks eQ-3 once. Switching restarts HmIP-RF.')}</p>
        {/if}
        {#if st.snapshots.length}
            <h3>{t('Kept identities')}</h3>
            <ul class="lk-snapshots">
                {#each st.snapshots as s (s.sgtin)}
                    <li data-sgtin={s.sgtin} data-kind={s.kind ?? 'switch'}><span class="hmm-mono">{s.sgtin}</span> · {new Date(s.at).toLocaleString()}{s.sgtin === st.sgtin ? ` · ${t('this module')}` : ''}{s.kind === 'fresh-start' ? ` · ${t('the previous module, moved aside by the fresh start')}` : ''}
                        <button type="button" class="hmm-button" disabled={busy} onclick={() => discard(s)}>{t('Discard')}</button></li>
                {/each}
            </ul>
            <p class="ol-muted">{t('Copies of a module\'s identity from before a switch to local key mode, or a previous module\'s identity the fresh start moved aside, with key material; kept until discarded.')}</p>
        {/if}
    {/if}
{/if}

<style>
    .lk-form fieldset { border: 0; padding: 0; margin: 0 0 8px; display: flex; gap: 16px; flex-wrap: wrap; }
    .lk-form legend { padding: 0; margin-bottom: 4px; }
    .lk-costs { margin: 8px 0; padding-left: 18px; font-size: 0.92em; }
    .lk-override { display: flex; align-items: center; gap: 6px; margin-top: 12px; }
    .lk-override-note { margin: 2px 0 0 22px; font-size: 0.9em; }
    .lk-snapshots { padding-left: 18px; }
    .lk-snapshots li { margin: 4px 0; }
    .lk-snapshots button { margin-left: 8px; }
    [data-notice="local-key-check"] button { margin-left: 8px; }
</style>
