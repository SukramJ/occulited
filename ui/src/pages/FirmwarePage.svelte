<script lang="ts">
    import {onMount} from 'svelte';
    import {pageLife} from '../lib/pagelife.svelte';
    import {api, REQUEST_HEADER} from '../lib/api';
    import {t} from '../lib/i18n.svelte';
    import SystemTitle from '../lib/SystemTitle.svelte';
    import {auth} from '../lib/auth.svelte';
    import Loading from '../lib/Loading.svelte';
    import Help from '../lib/Help.svelte';
    import ModalDialog from '../lib/ModalDialog.svelte';
    import {bundleFilePath, viewableBundleFile} from '../lib/bundlefiles';
    import SystemUpdate from '../lib/SystemUpdate.svelte';
    import RadioFirmware from '../lib/RadioFirmware.svelte';
    import CheckDaily from '../lib/CheckDaily.svelte';
    import SearchInput from '../lib/SearchInput.svelte';
    import Icon from '../lib/Icon.svelte';
    import type {Status} from '../lib/api';
    import {newerFirmware, shownVersion} from '../lib/fwversion';

    // version_from_name and date_from_name: what the update file's name carries, sent only when the
    // bundle's info states no version
    interface Bundle { type_code: string; dir?: string; name: string; version: string; version_from_name?: string; date_from_name?: string; files: string[] }
    interface TypeResult { type: string; version: string; version_from_name?: string; date_from_name?: string; action: string; detail?: string }
    // latest: eQ-3's version for the type, from the index; not_listed: the index has no entry for it (B-195)
    interface DeviceStatus { interface: string; address: string; type: string; firmware: string; available_firmware?: string; latest?: string; not_listed?: boolean; update_available: boolean; updatable?: boolean }
    interface State { enabled: boolean; running: boolean; last_run?: string; next_run?: string; last_error?: string; last_result: TypeResult[]; devices: DeviceStatus[]; deployed: Bundle[]; index_size: number; interface_errors?: Record<string, string> }

    let st = $state<State | null>(null);
    // the system update needs to know whether this is a container (no recovery system to stage for)
    // and the platform (the boot entry the install writes)
    let status = $state<Status | null>(null);
    let error = $state('');
    let notice = $state('');
    let file = $state<File | null>(null);
    let busy = $state(false);

    async function load() {
        try {
            const r = await api.get<State>('/api/system/v1/firmware');
            // an empty or partial answer (a stub, an older daemon) must not throw in the page
            st = {...r, devices: r.devices ?? [], last_result: r.last_result ?? [], deployed: (r.deployed ?? []).map((b) => ({...b, files: b.files ?? []}))};
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
    }
    const life = pageLife();
    onMount(() => {
        void load();
        void api.get<Status>('/api/system/v1/status').then((s) => (status = s), () => undefined);
        // task 177: no polls while another page shows, a refresh when it comes back
        const id = setInterval(() => life.active && load(), 5000);
        const stopReturn = life.onReturn(() => void load());
        return () => {
            stopReturn();
            clearInterval(id);
        };
    });
    async function check() {
        notice = '';
        try {
            await api.post('/api/system/v1/firmware/check');
            notice = t('Check started');
        } catch (e) {
            notice = (e as Error).message;
        }
        await load();
    }
    async function toggle(on: boolean) {
        try {
            await api.put('/api/system/v1/firmware/settings', {enabled: on});
        } catch (e) {
            notice = (e as Error).message;
            throw e;
        } finally {
            await load();
        }
    }
    // task 246: the deployed firmware's filter, the interface that reads a bundle, its firmware files
    let bundleFilter = $state('');
    const shownBundles = $derived.by(() => {
        const q = bundleFilter.trim().toLowerCase();
        const all = st?.deployed ?? [];
        if (!q) return all;
        return all.filter((b) => [b.type_code, b.dir ?? '', b.name, b.version, b.version_from_name ?? '', ...b.files].some((v) => v.toLowerCase().includes(q)));
    });
    // BidCos types are named HM-…; everything else in eQ-3's list is HmIP (HmIP-, HmIPW-, ELV-SH-, the access points)
    const interfaceOf = (name: string) => (/^hm-/i.test(name) ? 'BidCos-RF' : 'HmIP-RF');
    // the files that are not the two texts: the firmware (.efw/.eq3/.zip), and anything else a bundle carries
    const firmwareFiles = (b: Bundle) => b.files.filter((f) => f !== 'changelog.txt' && f !== 'info');
    async function upload(e: Event) {
        e.preventDefault();
        if (!file) return;
        busy = true;
        notice = '';
        try {
            const fd = new FormData();
            fd.append('file', file);
            const res = await fetch('/api/system/v1/firmware/upload', {method: 'POST', headers: REQUEST_HEADER, body: fd});
            const data = (await res.json()) as Bundle & {error?: string; message?: string};
            if (data.error) throw new Error(data.message ?? data.error);
            notice = `${t('Deployed')}: ${data.name} ${data.version}`;
            file = null;
        } catch (err) {
            notice = (err as Error).message;
        } finally {
            busy = false;
            await load();
        }
    }
    const admin = $derived(auth.role === 'admin');
    const fmt = (s?: string) => (s ? new Date(s).toLocaleString() : '–');

    // A bundle's text files - its changelog, its info - open in a dialog: the file name as the
    // title, the device type under it, the text as it is in a monospaced block. The answer of a
    // file opened before is dropped when another one was opened meanwhile.
    interface Viewer { name: string; type: string; text: string; error: string; loading: boolean }
    let viewer = $state<Viewer | null>(null);
    let viewerSeq = 0;
    async function openFile(b: Bundle, name: string) {
        const seq = ++viewerSeq;
        viewer = {name, type: b.name || b.type_code, text: '', error: '', loading: true};
        try {
            const text = await api.getText(bundleFilePath(b.dir || b.type_code, name));
            if (seq === viewerSeq && viewer) viewer = {...viewer, text, loading: false};
        } catch (e) {
            if (seq === viewerSeq && viewer) viewer = {...viewer, error: (e as Error).message, loading: false};
        }
    }
    function closeFile() {
        viewerSeq++;
        viewer = null;
    }
</script>

<!-- a version a bundle does not state (eQ-3's 0.0.0 arrives as an empty one) is the one its update
     file's name carries, muted and marked as such; a quiet dash when the name gives none either -->
{#snippet version(v: {version: string; version_from_name?: string; date_from_name?: string})}{#if v.version}{v.version}{:else if v.version_from_name}<span class="ol-muted ol-fromname" title={t('The bundle states no version; this one is read from the name of its update file.')}>{v.date_from_name ? `${v.version_from_name} (${v.date_from_name})` : v.version_from_name}{' '}<em class="ol-fromname-mark">{t('from the file name')}</em></span>{:else}<span class="ol-muted">–</span>{/if}{/snippet}

<SystemTitle />

<!-- the maintainer, 2026-09-19/20: the system update (from the Status page), the radio firmware
     (from the Interfaces page) and the paired devices' firmware, each in a panel of its own -->
<section class="ol-panel" data-panel="system-update">
    <SystemUpdate container={!!status?.container} platform={status?.version?.platform} />
</section>

<RadioFirmware />

<section class="ol-panel" data-panel="device-firmware">
<!-- task 51: how the page works is help, not the system's state - behind the heading's ? -->
<h2 id="device-firmware">{t('Device firmware')}<Help>{t('Firmware for the paired device types is fetched from eQ-3 - on Check now, and once a day with Check daily - and made available to the interfaces. Nothing is installed onto a device by itself: that stays your decision in the frontend.')}</Help></h2>
{#if notice}<div class="ol-notice">{notice}</div>{/if}
{#if !st}
    <Loading {error} />
{:else}
    <div class="ol-toolbar">
        {#if admin}
            <!-- task 244: the check button with Check daily behind it (lib/CheckDaily.svelte) -->
            <CheckDaily label={t('Check now')} busyLabel={t('Running…')} busy={st.running} onCheck={check} daily={st.enabled} onDaily={toggle} host="ccu3-update.homematic.com" name="firmware" />
        {/if}
        <span class="ol-muted">{t('Last run')}: {fmt(st.last_run)} · {t('Next run')}: {fmt(st.next_run)}</span>
    </div>
    {#if st.last_error}<div class="ol-notice error">{st.last_error}</div>{/if}
    {#if st.interface_errors}
        {#each Object.entries(st.interface_errors) as [name, err] (name)}<div class="ol-notice error">{name}: {err}</div>{/each}
    {/if}

    <h2>{t('Paired devices')} <span class="ol-muted">· {st.devices.length}</span></h2>
    <table class="ol-table ol-stack">
        <thead><tr><th>{t('Interface')}</th><th>{t('Address')}</th><th>{t('Type')}</th><th>{t('Firmware')}</th><th>{t('Available')}</th><th>{t('Latest from eQ-3')}</th></tr></thead>
        <tbody>
            <!-- Available is what the interface offers the device (a deployed bundle); Latest is eQ-3's
                 version for the type, so an update shows before its bundle is here (B-195) -->
            {#each st.devices as d (d.interface + d.address)}
                <tr><td data-label={t('Interface')}>{d.interface}</td><td class="hmm-mono" data-label={t('Address')}>{d.address}</td><td data-label={t('Type')}>{d.type}</td><td data-label={t('Firmware')}>{d.firmware}</td><td data-label={t('Available')}>{#if newerFirmware(d.available_firmware, d.firmware)}<span class="ol-fw-newer">{d.available_firmware}</span>{:else}<span class="ol-muted">{shownVersion(d.available_firmware) || '–'}</span>{/if}</td><td data-label={t('Latest from eQ-3')} class="ol-fw-latest">{#if d.updatable === false}<span class="ol-muted" data-not-updatable>{t('not updatable over the air')}</span>{:else if d.latest && newerFirmware(d.latest, d.firmware)}<span class="ol-fw-newer">{d.latest}</span>{:else if d.latest}<span class="ol-muted">{d.latest}</span>{:else if d.not_listed}<span class="ol-muted">{t("not in eQ-3's list")}</span>{:else}<span class="ol-muted">–</span>{/if}</td></tr>
            {/each}
        </tbody>
    </table>

    <h2>{t('Last run')}</h2>
    {#if st.last_result.length === 0}<div class="ol-muted">–</div>{:else}
        <table class="ol-table ol-stack">
            <thead><tr><th>{t('Type')}</th><th>{t('Version')}</th><th>{t('Result')}</th><th></th></tr></thead>
            <tbody>
                {#each st.last_result as r (r.type)}
                    <tr><td data-label={t('Type')}>{r.type}</td><td data-label={t('Version')}>{@render version(r)}</td><td data-label={t('Result')}>{t(r.action)}</td><td class="ol-muted">{r.detail ?? ''}</td></tr>
                {/each}
            </tbody>
        </table>
    {/if}

    <!-- task 246 (the maintainer): what the table is, the path in the help; changelog and info as
         icon buttons left of the firmware file; a filter over type, name, version and file -->
    <h2 id="deployed-firmware">{t('Deployed device firmware')}<Help>{t('hmipserver (HmIP-RF, access points) and rfd (BidCos-RF) read it from /etc/config/firmware; BidCos-Wired devices get firmware only with the system image.')}</Help></h2>
    {#if st.deployed.length === 0}<div class="ol-muted">–</div>{:else}
        <div class="ol-toolbar fw-filter">
            <SearchInput bind:value={bundleFilter} placeholder={t('Filter')} label={t('Filter the deployed firmware')} delay={0} />
            <span class="ol-muted" data-bundle-count>{t('{n} of {total}', {n: String(shownBundles.length), total: String(st.deployed.length)})}</span>
        </div>
        <table class="ol-table ol-stack ol-bundles">
            <colgroup><col class="fw-c-code" /><col /><col class="fw-c-ver" /><col class="fw-c-if" /><col class="fw-c-icons" /><col /></colgroup>
            <thead><tr><th>{t('Type code')}</th><th>{t('Type')}</th><th>{t('Version')}</th><th>{t('Interface')}</th><th><span class="fw-sr">{t('Texts')}</span></th><th>{t('Firmware file')}</th></tr></thead>
            <tbody>
                {#each shownBundles as b (b.dir || b.type_code)}
                    {@const texts = b.files.filter((f) => f === 'changelog.txt' || f === 'info')}
                    <tr>
                        <td class="hmm-mono" data-label={t('Type code')}>{b.type_code}</td>
                        <td data-label={t('Type')}>{b.name}</td>
                        <td data-label={t('Version')}>{@render version(b)}</td>
                        <td data-label={t('Interface')}>{interfaceOf(b.name)}</td>
                        <!-- only the texts the bundle has; each opens the viewer, named by its file -->
                        <td class="fw-icons" data-label={t('Texts')}>{#each texts as f (f)}<button type="button" class="ol-iconlink fw-icon" onclick={() => openFile(b, f)} aria-label={f} title={f === 'info' ? t('The bundle\'s info') : t('The changelog')} data-text={f}><Icon name={f === 'info' ? 'info' : 'log'} size={16} /></button>{/each}</td>
                        <td class="ol-files hmm-mono" data-label={t('Firmware file')}>{#each firmwareFiles(b) as f, i (f)}{#if i > 0}<span class="ol-muted">{', '}</span>{/if}{#if viewableBundleFile(f)}<button type="button" class="ol-filelink" onclick={() => openFile(b, f)}>{f}</button>{:else}<span>{f}</span>{/if}{/each}</td>
                    </tr>
                {/each}
            </tbody>
        </table>
        {#if shownBundles.length === 0}<p class="ol-muted" data-bundles-none>{t('Nothing matches the filter.')}</p>{/if}
    {/if}
    {#if admin}
        <h2>{t('Upload a bundle')}<Help>{t('An eQ-3 device firmware .tgz — for air-gapped boxes or firmware the vendor no longer serves.')}</Help></h2>
        <form class="ol-toolbar" onsubmit={upload}>
            <input type="file" accept=".tgz,.tar.gz,application/gzip" onchange={(e) => (file = (e.currentTarget as HTMLInputElement).files?.[0] ?? null)} />
            <button class="hmm-button" type="submit" disabled={!file || busy}>{t('Deploy')}</button>
        </form>
    {/if}
{/if}
</section>

<ModalDialog open={viewer !== null} title={viewer?.name ?? ''} subtitle={viewer?.type ?? ''} size="large" class="ol-fileviewer" onclose={closeFile}>
    {#if viewer}
        {#if viewer.loading}
            <div class="ol-muted ol-fileviewer-state">{t('Loading…')}</div>
        {:else if viewer.error}
            <div class="ol-notice error ol-fileviewer-state">{viewer.error}</div>
        {:else if viewer.text === ''}
            <div class="ol-muted ol-fileviewer-state">{t('empty file')}</div>
        {:else}
            <pre class="hmm-mono ol-filetext">{viewer.text}</pre>
        {/if}
    {/if}
</ModalDialog>

<style>
    /* task 246: the deployed firmware's filter and its narrow icon column */
    .fw-filter { margin: 0 0 8px; }
    .fw-c-code { width: 7.5em; }
    .fw-c-ver { width: 9em; }
    .fw-c-if { width: 7em; }
    .fw-c-icons { width: 4.5em; }
    .fw-icons { white-space: nowrap; }
    .fw-icon { display: inline-flex; padding: 2px; margin-right: 4px; border: 0; background: none; color: var(--hmm-accent); cursor: pointer; border-radius: var(--hmm-radius); }
    .fw-icon:hover { background: var(--hmm-hover, rgba(127, 127, 127, 0.15)); }
    .fw-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
    /* a version newer than the one the device runs */
    .ol-fw-newer { color: var(--hmm-warn); }
    /* the file list wraps on a phone instead of widening the table */
    .ol-bundles .ol-files { white-space: normal; overflow-wrap: anywhere; }
    .ol-filelink { display: inline; padding: 0; border: 0; background: none; color: var(--hmm-link); font: inherit; text-align: left; cursor: pointer; overflow-wrap: anywhere; }
    .ol-filelink:hover { text-decoration: underline; }
    /* the version taken from the update file's name: muted, with a small marker that stays on its line */
    .ol-fromname-mark { font-size: var(--hmm-font-size-small); white-space: nowrap; }
    .ol-filelink:focus-visible { outline: 1px solid var(--hmm-focus); outline-offset: 1px; }
    .ol-filetext { margin: 0 0 16px; padding: 10px 12px; white-space: pre-wrap; overflow-wrap: anywhere; font-size: var(--hmm-font-size-grid); line-height: 1.45; color: var(--hmm-fg); background: var(--hmm-bg-sunken); border: 1px solid var(--hmm-border-muted); border-radius: var(--hmm-radius); }
    .ol-fileviewer-state { margin-bottom: 16px; }
</style>
