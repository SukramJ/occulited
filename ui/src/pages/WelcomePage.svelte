<script lang="ts">
    import {onMount} from 'svelte';
    import {api} from '../lib/api';
    import {t} from '../lib/i18n.svelte';
    import {navigate} from '../lib/router.svelte';

    interface FwState { enabled: boolean }
    interface FirstBoot { objects: number; devices: number; channels: number; rooms: number; functions: number; unnamed: number; error?: string }
    let firstBoot = $state<FirstBoot | null>(null);
    interface ImportResult { devices: number; channels: number; rooms: number; functions: number; unnamed: number }

    let fwEnabled = $state(true);
    let ccuHost = $state('');
    let preview = $state<{result: ImportResult; objects: number} | null>(null);
    let msg = $state('');
    let busy = $state('');
    // task 149 (D-103): the local key step, only while HmIP-RF runs with no paired device - there
    // is no re-pairing cost then. An explicit choice, nothing pre-selected; Done waits for it.
    interface LocalKeyView { available: boolean; enabled: boolean; devices?: number }
    let lk = $state<LocalKeyView | null>(null);
    let lkChoice = $state<'' | 'local' | 'eq3'>('');
    const lkStep = $derived(!!lk && lk.available && !lk.enabled && lk.devices === 0);
    const lkBlocked = $derived(lkStep && lkChoice === '');

    onMount(async () => {
        try { lk = await api.get<LocalKeyView>('/api/system/v1/radio/hmip/local-key?devices=1'); } catch { /* no HmIP, or not an administrator */ }
        try { fwEnabled = (await api.get<FwState>('/api/system/v1/firmware')).enabled; } catch { /* no fetcher */ }
        try { firstBoot = (await api.get<{first_boot_import?: FirstBoot}>('/api/system/v1/status')).first_boot_import ?? null; } catch { /* none */ }
    });

    async function setFw(on: boolean) {
        busy = 'fw';
        try { fwEnabled = (await api.put<FwState>('/api/system/v1/firmware/settings', {enabled: on})).enabled; } catch (e) { msg = (e as Error).message; } finally { busy = ''; }
    }

    async function ccu(dryRun: boolean) {
        busy = dryRun ? 'preview' : 'import';
        msg = '';
        try {
            const r = await api.post<{result: ImportResult; objects: number; changed?: boolean}>('/api/meta/v1/import/ccu', {host: ccuHost.trim(), mode: 'merge', dry_run: dryRun});
            preview = r;
            if (!dryRun) msg = t('Imported: {o} named objects, {r} rooms, {f} functions.', {o: r.objects, r: r.result.rooms, f: r.result.functions});
        } catch (e) {
            msg = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    async function done(to = '/') {
        if (lkStep && lkChoice === 'local') {
            busy = 'lk';
            try {
                await api.put('/api/system/v1/radio/hmip/local-key', {mode: 'generate'});
            } catch (e) {
                msg = (e as Error).message;
                busy = '';
                return;
            }
            busy = '';
        }
        try { localStorage.setItem('ol.welcomed', '1'); } catch { /* ignore */ }
        navigate(to);
    }
</script>

<h1>{t('Welcome')}</h1>
<p>{lkStep ? t('The administrator exists. Four things worth deciding now; each can be changed later on its page.') : t('The administrator exists. Three things worth deciding now; each can be changed later on its page.')}</p>

<h2>1 · {t('Device firmware')}</h2>
<p>{t('This system can download firmware for the device types that are paired from eQ-3\'s update server once a day — the same server a CCU talks to, and the only thing it calls out for. Installing stays your decision.')}</p>
<div class="ol-actions">
    <label><input type="radio" name="fw" checked={fwEnabled} onchange={() => setFw(true)} disabled={busy !== ''} /> {t('Download automatically')}</label>
    <label><input type="radio" name="fw" checked={!fwEnabled} onchange={() => setFw(false)} disabled={busy !== ''} /> {t('Off — I upload firmware by hand')}</label>
</div>

<h2>2 · {t('Names from an old CCU')}</h2>
{#if firstBoot && !firstBoot.error}
    <div class="ol-notice">{t('This system was updated from a CCU: its names, rooms and functions were read from the ReGa database on this first boot — {o} named devices and channels, {r} rooms, {f} functions ({u} still carried the default name and were left out). Programs and system variables did not come across; nothing here could run them.', {o: firstBoot.objects, r: firstBoot.rooms, f: firstBoot.functions, u: firstBoot.unnamed})}</div>
{:else if firstBoot?.error}
    <div class="ol-notice">{t('A ReGa database was found but could not be read: {e}', {e: firstBoot.error})}</div>
{/if}
<p>{t('If a CCU, RaspberryMatic or OpenCCU with your device names, rooms and functions is still running, enter its address: the names come across now. Its firewall must allow this system (REGA: full, or this address listed). Can be done later on the Names page.')}</p>
<div class="ol-toolbar">
    <input class="hmm-input" placeholder={t('CCU address')} bind:value={ccuHost} />
    <button class="hmm-button" onclick={() => ccu(true)} disabled={!ccuHost.trim() || busy !== ''}>{t('Preview')}</button>
    <button class="hmm-button" onclick={() => ccu(false)} disabled={!preview || busy !== ''}>{t('Import')}</button>
</div>
{#if preview}<div class="ol-muted">{t('{d} devices, {c} channels ({o} named), {r} rooms, {f} functions', {d: preview.result.devices, c: preview.result.channels, o: preview.objects, r: preview.result.rooms, f: preview.result.functions})}</div>{/if}
{#if msg}<div class="ol-notice">{msg}</div>{/if}

{#if lkStep}
    <h2>3 · {t('The HmIP network key')}</h2>
    <p>{t("HmIP devices share one network key. Normally it is locked in the radio module, and eQ-3's key server is needed to move it to another radio module and to pair a device without its key. It can be kept on this system instead: it is then offline-capable, and a later radio swap never needs the internet. No HmIP device is paired yet, so nothing has to be taught in again for this.")}</p>
    <div class="ol-actions lk-choice" role="radiogroup" aria-label={t('The HmIP network key')}>
        <label><input type="radio" name="lk" value="local" bind:group={lkChoice} disabled={busy !== ''} /> {t('Generate a local key now')}</label>
        <label><input type="radio" name="lk" value="eq3" bind:group={lkChoice} disabled={busy !== ''} /> {t("Keep eQ-3's key server")}</label>
    </div>
    {#if lkChoice === 'local'}
        <div class="ol-notice" data-notice="lk-backup">{t('The key then lies in a file on this system and in every backup - whoever has it can join the network, so protecting the backups becomes crucial.')} <a href="/system/backup">{t('Backup')}</a></div>
    {/if}
    <p class="ol-muted">{t('Either way this can be changed later on the Interfaces page, under Local key mode. Generating the key restarts HmIP-RF.')}</p>
{/if}

<h2>{lkStep ? 4 : 3} · {t('A frontend')}</h2>
<p>{t('This system ships no device management of its own. Homematic Manager (pairing, parameters, direct links, names) and the other addons are one click away in the catalogue.')}</p>

<div class="ol-actions" style="margin-top:16px">
    <button class="hmm-button" disabled={lkBlocked || busy !== ''} onclick={() => done('/catalog')}>{t('Open the catalogue')}</button>
    <button class="hmm-button" disabled={lkBlocked || busy !== ''} onclick={() => done()}>{t('Done')}</button>
    {#if lkBlocked}<span class="ol-muted">{t('Choose where the HmIP network key is kept first.')}</span>{/if}
</div>

<style>
    label { margin-right: 16px; }
</style>
