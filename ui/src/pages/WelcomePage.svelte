<script lang="ts">
    import {onMount} from 'svelte';
    import {api} from '../lib/api';
    import {t} from '../lib/i18n.svelte';
    import {link, navigate} from '../lib/router.svelte';

    interface FwState { enabled: boolean }
    interface FeedState { feed?: {enabled: boolean} | null }
    interface CatalogState { daily?: boolean }
    interface FirstBoot { objects: number; devices: number; channels: number; rooms: number; functions: number; unnamed: number; error?: string }
    let firstBoot = $state<FirstBoot | null>(null);

    // B-241 (D-90): the daily checks are off on a fresh system; the page asks once, one checkbox
    // per destination. GitHub is two settings behind one box: the release check and the
    // catalogue's daily check (which carries the installed addons' update checks).
    let fwEnabled = $state(false);
    let relEnabled = $state(false);
    let catDaily = $state(false);
    const ghOn = $derived(relEnabled && catDaily);
    // occulited task 4: the devices of an old CCU come across from its backup (openccu-lite task
    // 251, System → Backup), and only onto a system with no device paired yet - the import takes
    // over the old system's radio identity. The factory reset's view counts the paired devices per
    // interface; when it cannot be read the offer stays, and the Backup page checks again.
    interface PairedView { interfaces?: Record<string, {devices: number; known: boolean}> }
    let paired = $state(0);
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
        try { relEnabled = (await api.get<FeedState>('/api/system/v1/system-update')).feed?.enabled ?? false; } catch { /* no feed */ }
        try { catDaily = (await api.get<CatalogState>('/api/system/v1/catalog')).daily ?? false; } catch { /* no catalogue */ }
        try { firstBoot = (await api.get<{first_boot_import?: FirstBoot}>('/api/system/v1/status')).first_boot_import ?? null; } catch { /* none */ }
        try { paired = Object.values((await api.get<PairedView>('/api/system/v1/factory-reset')).interfaces ?? {}).reduce((n, i) => n + (i.known ? i.devices : 0), 0); } catch { /* not known: the offer stays */ }
    });

    async function setFw(on: boolean) {
        busy = 'fw';
        try { fwEnabled = (await api.put<FwState>('/api/system/v1/firmware/settings', {enabled: on})).enabled; } catch (e) { msg = (e as Error).message; } finally { busy = ''; }
    }

    async function setGitHub(on: boolean) {
        busy = 'gh';
        try {
            relEnabled = (await api.put<{enabled: boolean}>('/api/system/v1/system-update/settings', {enabled: on})).enabled;
            catDaily = (await api.put<{daily: boolean}>('/api/system/v1/catalog/settings', {daily: on})).daily;
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

<h2>1 · {t('Automatic checks')}</h2>
<p>{t('This system connects to the internet only when you ask it to. Two checks can run daily instead; each is named here with where it connects, each is off, and each can be changed later on its page.')}</p>
<div class="ol-actions outbound" data-welcome-outbound>
    <label><input type="checkbox" checked={ghOn} onchange={(e) => setGitHub((e.currentTarget as HTMLInputElement).checked)} disabled={busy !== ''} data-outbound="github" /> {t('GitHub (api.github.com, raw.githubusercontent.com): check daily for a new system release and refresh the addon catalogue; the installed addons\' own update checks run with it.')}</label>
    <label><input type="checkbox" checked={fwEnabled} onchange={(e) => setFw((e.currentTarget as HTMLInputElement).checked)} disabled={busy !== ''} data-outbound="eq3" /> {t('eQ-3 (ccu3-update.homematic.com): check daily for new firmware of the paired device types and download it — the same server a CCU asks. Installing stays your decision.')}</label>
</div>

<h2>2 · {t('Devices from a CCU or OpenCCU')}</h2>
{#if firstBoot && !firstBoot.error}
    <div class="ol-notice">{t('This system was updated from a CCU: its names, rooms and functions were read from the ReGa database on this first boot — {o} named devices and channels, {r} rooms, {f} functions ({u} still carried the default name and were left out). Programs and system variables did not come across; nothing here could run them.', {o: firstBoot.objects, r: firstBoot.rooms, f: firstBoot.functions, u: firstBoot.unnamed})}</div>
{:else if firstBoot?.error}
    <div class="ol-notice">{t('A ReGa database was found but could not be read: {e}', {e: firstBoot.error})}</div>
{/if}
<p>{t('This does not restore a backup: it takes over only the paired devices, with their keys and names, from the backup of a CCU or OpenCCU into this system. The system reboots afterwards.')}</p>
<div class="ol-actions" data-welcome-devices>
    {#if paired > 0}
        <span class="ol-muted" data-welcome-devices-paired>{t('This system has {n} devices paired already: the import only works on a system without paired devices.', {n: paired})}</span>
    {:else}
        <a class="hmm-button" href="/system/backup#restore" use:link data-action="welcome-import-devices">{t('Import paired devices, keys and names from a backup')}</a>
    {/if}
</div>
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
    .outbound { flex-direction: column; align-items: flex-start; gap: 8px; }
    .outbound label { margin-right: 0; display: flex; gap: 8px; align-items: baseline; }
    .outbound input { flex: none; }
</style>
