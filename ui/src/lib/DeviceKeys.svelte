<script lang="ts">
    // Task 154 (D-103, D-104): the HmIP devices' own keys, from their stickers, in hmipserver's
    // sgtin.map. With a device's key here a plain install mode pairs it without eQ-3's key server,
    // and its re-inclusion after a firmware update runs locally too. Three ways in - a scanned
    // or photographed sticker, a pasted code, the SGTIN and key typed from the print - and a key
    // sheet to print. HmIP-RF reads the map when it starts, so the changes wait for that (Apply).
    // The list never carries a key; the sheet asks for the password (or a fresh login at the
    // identity provider) every time.
    import {onMount} from 'svelte';
    import {api} from './api';
    import {confirmTicket, returned} from './confirm';
    import {ask} from './dialog.svelte';
    import {t} from './i18n.svelte';
    import {scrollToAnchor} from './anchor';
    import {DEVICE_KEYS, addressOf, formatSGTIN, keyShape, normalizeSGTIN, parseDeviceCode, type AddResult, type DeviceKeysView, type ExportedKey} from './devicekeys';
    import Disclosure from './Disclosure.svelte';
    import Help from './Help.svelte';
    import KeySheet from './KeySheet.svelte';
    import QrScanner from './QrScanner.svelte';

    const EXPORT = DEVICE_KEYS + '/export';

    let view = $state<DeviceKeysView | null>(null);
    let err = $state('');
    // why the provider's confirmation brought no ticket (from the fragment); a reload keeps it
    let refused = $state('');
    let notice = $state('');
    let noticeError = $state(false);
    let code = $state('');
    let sgtin = $state('');
    let key = $state('');
    let typedOpen = $state(false);
    let sheet = $state<ExportedKey[] | null>(null);
    let poll: ReturnType<typeof setTimeout> | undefined;

    const shape = $derived(keyShape(key));
    const typedOk = $derived(!!normalizeSGTIN(sgtin) && (shape === 'printed' || shape === 'hex'));

    let scrolled = false;
    async function load() {
        try {
            view = await api.get<DeviceKeysView>(DEVICE_KEYS);
            err = '';
        } catch (e) {
            err = (e as Error).message;
        }
        // task 183: the Status page's warning and the old Interfaces anchor land here once it is drawn
        if (!scrolled && view) {
            scrolled = true;
            scrollToAnchor('device-keys');
        }
        clearTimeout(poll);
        if (view?.applying) poll = setTimeout(load, 2000);
    }

    function label(sg: string): string {
        const row = view?.rows.find((r) => r.sgtin === sg || r.address === addressOf(sg));
        return row?.name || (row?.type ? `${row.type} ${formatSGTIN(sg)}` : formatSGTIN(sg));
    }

    /** Stores a key; a serial that is not a paired device is asked about first (D-103). */
    async function store(body: {code?: string; sgtin?: string; key?: string}, sg: string): Promise<boolean> {
        const paired = new Set(view?.rows.filter((r) => r.paired).map((r) => r.address));
        if (view?.devices_known && !paired.has(addressOf(sg)) && !view.rows.some((r) => r.sgtin === sg)) {
            const ok = await ask({
                title: t('Not paired yet'),
                message: t('{sgtin} is not a paired HmIP device of this system. Store its key anyway? It is used when the device is paired.', {sgtin: formatSGTIN(sg)}),
                confirm: t('Store the key'),
            });
            if (!ok) return false;
        }
        try {
            const r = await api.post<AddResult>(DEVICE_KEYS, body);
            noticeError = false;
            notice = r.same
                ? t('The key of {device} was stored already.', {device: label(r.sgtin)})
                : r.replaced
                  ? t('The key of {device} was replaced.', {device: label(r.sgtin)})
                  : r.paired
                    ? t('Key stored for {device}.', {device: label(r.sgtin)})
                    : t('Key stored for {device}; it is used when the device is paired.', {device: formatSGTIN(r.sgtin)});
            await load();
            return true;
        } catch (e) {
            noticeError = true;
            notice = (e as Error).message;
            return false;
        }
    }

    async function scanned(text: string): Promise<boolean> {
        const c = parseDeviceCode(text);
        if (!c) {
            noticeError = true;
            notice = t('That QR code is not an HmIP device code (EQ01SG…DLK…).');
            return false;
        }
        return store({code: text}, c.sgtin);
    }

    async function storePasted(e: Event) {
        e.preventDefault();
        const c = parseDeviceCode(code);
        if (!c) {
            noticeError = true;
            notice = t('That is not an HmIP device code (EQ01SG…DLK…). For the printed key, use "Type the SGTIN and the key".');
            return;
        }
        if (await store({code}, c.sgtin)) code = '';
    }

    async function storeTyped(e: Event) {
        e.preventDefault();
        const sg = normalizeSGTIN(sgtin);
        if (!sg || !typedOk) return;
        if (await store({sgtin, key}, sg)) {
            sgtin = key = '';
        }
    }

    async function apply() {
        await ask({
            title: t('Apply the keys now?'),
            message: t('HmIP-RF restarts and reads the stored keys; the HmIP radio is unavailable for about a minute.'),
            confirm: t('Restart HmIP-RF'),
            run: async () => {
                view = await api.post<DeviceKeysView>(DEVICE_KEYS + '/apply');
                await load();
            },
        });
    }

    async function remove(sg: string) {
        await ask({
            title: t('Remove the key of {device}?', {device: label(sg)}),
            message: t('Without it, pairing this device needs eQ-3\'s key server again, and a lost sticker cannot be replaced from here.'),
            confirm: t('Remove'),
            danger: true,
            focusCancel: true,
            run: async () => {
                view = await api.del<DeviceKeysView>(`${DEVICE_KEYS}/${encodeURIComponent(sg)}`);
            },
        });
    }

    async function openSheet(ticket: string) {
        try {
            sheet = (await api.getWith<{keys: ExportedKey[]}>(EXPORT, {'X-Occulite-Confirm': ticket})).keys;
        } catch (e) {
            err = (e as Error).message;
        }
    }

    /** The sheet: confirmed every time, by the login password or at the identity provider (D-104, task 20). */
    async function printSheet() {
        err = refused = '';
        let ticket: string | null;
        try {
            ticket = await confirmTicket(EXPORT, {
                title: t('Print the key sheet'),
                message: t('The sheet holds every HmIP device key in clear.'),
                provider: t('The sheet holds every HmIP device key in clear, so it asks who you are every time: you sign in at the identity provider once more and come back here.'),
                impossible: t('This account has no password and no identity provider is configured, so it cannot confirm the key sheet.'),
            });
        } catch (e) {
            err = (e as Error).message;
            return;
        }
        if (ticket) await openSheet(ticket);
    }

    onMount(() => {
        // back from the identity provider: the ticket (or why there is none) in the fragment
        const back = returned();
        if (back && 'ticket' in back) void openSheet(back.ticket);
        else if (back) refused = t('The confirmation at the identity provider was refused: {reason}', {reason: back.refused});
        void load();
        return () => clearTimeout(poll);
    });
</script>

<h2 id="device-keys">{t('HmIP device keys')}<Help>{t("Every HmIP device carries its own key on its sticker, as a QR code and as printed text. With a device's key here, a plain install mode pairs it without eQ-3's key server, and its re-inclusion after a firmware update runs on this system too. Scan the stickers now and pair the devices whenever you like.")}</Help> <span class="ol-muted">· HmIP-RF</span></h2>
{#if err || refused}<div class="ol-warn" data-notice="device-keys-error">{err || refused}</div>{/if}
{#if view}
    {#if view.unread}<div class="ol-notice error" data-notice="device-keys-unread">{t('KeyServer.Mode is KEYSERVER in hmip_user.conf: HmIP-RF does not read the stored keys. Remove that line to use them.')}</div>{/if}
    {#if view.error}<div class="ol-notice error">{view.error}</div>{/if}
    {#if view.applying}
        <div class="ol-notice" data-notice="device-keys-applying"><span class="ol-dot starting"></span>{t('HmIP-RF is restarting to read the keys.')}</div>
    {:else if view.pending}
        <div class="ol-notice" data-notice="device-keys-pending">
            {view.pending === 1 ? t('One key change waits for the next HmIP-RF start. Apply it before pairing the scanned device.') : t('{n} key changes wait for the next HmIP-RF start. Apply them before pairing the scanned devices.', {n: view.pending})}
            <button type="button" class="hmm-button" onclick={apply}>{t('Apply now (restarts HmIP-RF)')}</button>
        </div>
    {/if}
    <p data-count>
        {#if view.devices_known}
            {t('{n} of {m} paired HmIP devices have their key here.', {n: view.with_key, m: view.paired})}
            {#if view.stored > view.with_key}{t('{k} more for devices not paired yet.', {k: view.stored - view.with_key})}{/if}
        {:else}
            {t('HmIP-RF did not answer, so the stored keys are listed without their devices ({k} stored).', {k: view.stored})}
        {/if}
    </p>

    <div class="dk-add">
        <QrScanner continuous onresult={scanned} photoLabel={t('Photo of a sticker')} />
        <form class="dk-paste" onsubmit={storePasted}>
            <input class="hmm-input hmm-mono" bind:value={code} placeholder="EQ01SG…DLK…" autocomplete="off" spellcheck="false" aria-label={t('Device code')} />
            <button type="submit" class="hmm-button" disabled={!code.trim()}>{t('Store')}</button>
        </form>
        <Disclosure label={t('Type the SGTIN and the key')} title={t('From the sticker')} bind:open={typedOpen}>
            <form class="ol-form dk-typed" onsubmit={storeTyped}>
                <label>{t('SGTIN')} <input class="hmm-input hmm-mono" bind:value={sgtin} placeholder="3014-F711-A000-…" autocomplete="off" spellcheck="false" aria-label={t('SGTIN')} /></label>
                <label>{t('Key')} <input class="hmm-input hmm-mono" bind:value={key} placeholder="XXXXX-XXXXX-XXXXX-XXXXX-XXXXXX" autocomplete="off" spellcheck="false" aria-label={t('Key')} /></label>
                {#if shape === 'odiv'}<p class="ol-muted" data-note="odiv">{t('The printed key has no D, I, O or V: a 0 or 1 was probably meant.')}</p>{/if}
                <div class="ol-actions"><button type="submit" class="hmm-button primary" disabled={!typedOk}>{t('Store')}</button></div>
            </form>
        </Disclosure>
    </div>
    {#if notice}<p class={noticeError ? 'ol-warn' : 'ol-muted'} data-notice="device-key-added">{notice}</p>{/if}

    {#if view.rows.length}
        <div class="ol-scroll">
            <table class="ol-table ol-stack dk-table">
                <thead><tr><th>{t('Device')}</th><th>{t('SGTIN or address')}</th><th>{t('Key')}</th><th></th></tr></thead>
                <tbody>
                    {#each view.rows as r (r.sgtin ?? r.address)}
                        <tr data-row={r.sgtin ?? r.address} data-key={r.has_key ? 'stored' : 'missing'}>
                            <td data-label={t('Device')}>{r.name || r.type || t('Not paired yet')}{#if r.name && r.type}<span class="ol-muted">{' · '}{r.type}</span>{/if}</td>
                            <td data-label={t('SGTIN or address')} class="hmm-mono">{r.sgtin ? formatSGTIN(r.sgtin) : r.address}</td>
                            <td data-label={t('Key')}>
                                {#if r.has_key}
                                    <span class="dk-stored">{t('stored')}</span>{#if r.pending}<span class="ol-muted">{' · '}{t('waits for the HmIP-RF start')}</span>{/if}{#if !r.paired}<span class="ol-muted">{' · '}{t('device not paired yet')}</span>{/if}
                                {:else}
                                    <span class="ol-muted">{t('missing')}</span>
                                {/if}
                            </td>
                            <td>{#if r.has_key && r.sgtin}<button type="button" class="hmm-button" onclick={() => remove(r.sgtin ?? '')}>{t('Remove')}</button>{/if}</td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>
    {/if}
    {#if view.bad_lines}<p class="ol-muted">{t('{n} lines of sgtin.map are not an SGTIN with a key; HmIP-RF ignores them.', {n: view.bad_lines})}</p>{/if}

    <div class="ol-actions dk-sheet">
        <button type="button" class="hmm-button" disabled={!view.stored} onclick={printSheet} data-action="key-sheet">{t('Print the key sheet…')}</button>
    </div>
    <p class="ol-muted dk-sheet-note">{t('A printable page with every stored key as its QR code, for when a sticker is lost. It is a copy of every device key in clear, so it asks for your password every time.')}</p>
{/if}
{#if sheet}<KeySheet keys={sheet} onclose={() => (sheet = null)} />{/if}

<style>
    .dk-add { display: flex; flex-direction: column; gap: 10px; margin: 10px 0; }
    .dk-paste { display: flex; gap: 8px; flex-wrap: wrap; max-width: 640px; }
    .dk-paste input { flex: 1 1 280px; min-width: 0; }
    .dk-typed label { display: flex; flex-direction: column; gap: 4px; }
    .dk-stored { color: var(--ok, inherit); font-weight: 600; }
    .dk-sheet { margin-top: 12px; }
    .dk-sheet-note { margin-top: 4px; font-size: 0.92em; }
</style>
