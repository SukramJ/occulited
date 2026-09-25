<script lang="ts">
    // openccu-lite task 218: a network radio board (HB-RF-ETH) - one radio module on the LAN that
    // the kernel module makes a local one. Its IPv4 address is the whole setting; Find lists the
    // boards that answer mDNS with what they say about themselves and whose they are. A save kicks
    // the radio hotplug: the board is connected (or let go) and the radio stack re-planned, no
    // reboot; a board that does not answer is tried again in the background. There is no pairing:
    // the last system to connect takes the board, so taking one another system uses is asked first.
    import {onDestroy, onMount} from 'svelte';
    import {api, ApiError} from './api';
    import {ask} from './dialog.svelte';
    import {t} from './i18n.svelte';
    import Icon from './Icon.svelte';
    import SectionHead from './SectionHead.svelte';
    import UseMark from './UseMark.svelte';

    interface Board {
        address: string; serial?: string; firmware?: string; module_type?: string; module_serial?: string;
        bidcos_address?: string; hmip_address?: string; connected_to?: string; state?: 'this' | 'free' | 'other'; name?: string; error?: string;
    }
    interface View {
        address: string; connected: boolean; module_loaded: boolean; retrying: boolean; reconnecting?: boolean; lost_since?: string; tries: number; last_try?: string; last_error?: string;
        board?: Board; board_error?: string;
    }

    let {admin = false}: {admin?: boolean} = $props();
    let view = $state<View | null>(null);
    let field = $state('');
    let busy = $state(false);
    let err = $state('');
    let msg = $state('');
    let found = $state<Board[] | null>(null);
    let finding = $state(false);
    let timer: ReturnType<typeof setInterval> | undefined;

    function loaded(v: View) {
        view = v;
        field = v.address;
    }
    async function load() {
        try {
            const v = await api.get<View>('/api/system/v1/radio/hb-rf-eth');
            view = v;
            if (!busy && field === '') field = v.address;
        } catch (e) {
            if (e instanceof ApiError && e.status === 501) view = null;
            else err = (e as Error).message;
        }
    }
    onMount(() => {
        void load();
        // while a board is configured and not connected, the page follows the background retry
        timer = setInterval(() => {
            if (view?.retrying && !busy) void load();
        }, 5000);
    });
    onDestroy(() => clearInterval(timer));

    async function save(address: string, take = false) {
        busy = true;
        err = '';
        msg = '';
        try {
            loaded(await api.put<View>('/api/system/v1/radio/hb-rf-eth', {address, take}));
            msg = address === '' ? t('The board is removed; the radio is re-planned without it.') : t('Saved. The system connects the board and re-plans the radio; this takes a few seconds.');
        } catch (e) {
            if (e instanceof ApiError && e.status === 409) {
                // the answer names the host: "the board is connected to <address>: …"
                const other = /(\d{1,3}(?:\.\d{1,3}){3})/.exec(e.message)?.[1] ?? '';
                const yes = await ask({
                    title: t('Take the board?'),
                    message: t('The board is connected to {host}. There is no pairing: the system that connects last takes the board, and {host} loses this radio. Take it anyway?', {host: other}),
                    confirm: t('Take the board'),
                    danger: true,
                });
                busy = false;
                if (yes) return save(address, true);
                return;
            }
            err = (e as Error).message;
        } finally {
            busy = false;
        }
    }
    async function remove() {
        const yes = await ask({title: t('Remove the board?'), message: t('The system lets the board go and re-plans the radio without it.'), confirm: t('Remove'), danger: true});
        if (yes) await save('');
    }
    async function find() {
        finding = true;
        err = '';
        try {
            const r = await api.post<{boards: Board[]; error?: string}>('/api/system/v1/radio/hb-rf-eth/find', {});
            found = r.boards;
        } catch (e) {
            err = (e as Error).message;
        } finally {
            finding = false;
        }
    }
    const stateText = (b: Board) => (b.state === 'this' ? t('connected to this system') : b.state === 'free' ? t('free') : b.state === 'other' ? t('in use by {host}', {host: b.connected_to ?? ''}) : '');
</script>

{#if view}
    {#snippet actions()}
        <button type="button" class="hmm-button ol-icon-button" disabled={finding || busy} onclick={find} data-hb-find><Icon name="search" /> {finding ? t('Searching…') : t('Find boards')}</button>
    {/snippet}
    <SectionHead id="hb-rf-eth" title={t('Network radio board (HB-RF-ETH)')} help={t('An HB-RF-ETH carries one radio module on the network; the system uses it like a module of its own. Give the board a fixed address (a DHCP reservation or a static address). There is no pairing: the system that connects last takes the board, so one board serves one system. Its firmware is updated on the board\'s own page.')} actions={admin ? actions : undefined} />
    <div class="hb-panel" data-hb-state={view.address === '' ? 'none' : view.connected ? 'connected' : 'retrying'}>
        {#if view.address === ''}
            <p class="ol-muted" data-hb-none>{t('No board is configured.')}</p>
        {:else if view.connected}
            <p class="hb-ok" data-hb-connected><Icon name="check-circle" /> {t('Connected to the board at {address}.', {address: view.address})}</p>
        {:else}
            <p class="ol-warn" data-hb-retrying>{view.reconnecting ? t('The connection to the board at {address} is lost. The system reconnects it on its own; the radio daemons keep running and have the module again when it is back.', {address: view.address}) : t('The board at {address} is not connected. The system tries again every 30 seconds and re-plans the radio when it answers.', {address: view.address})}{#if view.board_error}{' '}{t('The board: {reason}', {reason: view.board_error})}{/if}</p>
        {/if}
        {#if view.board}
            {@const b = view.board}
            <dl class="ol-kv hb-board" data-hb-board>
                <dt>{t('Board')}</dt><dd class="hmm-mono">{b.serial ?? '—'}</dd>
                <dt>{t('Installed firmware')}</dt><dd class="hmm-mono">{b.firmware ?? '—'}</dd>
                <dt>{t('Radio module')}</dt><dd data-hb-module>{b.module_type || t('none')}{#if b.module_serial}{' '}<span class="hmm-mono">{b.module_serial}</span>{/if}</dd>
                {#if b.bidcos_address}<dt>BidCos-RF</dt><dd class="hmm-mono">{b.bidcos_address}</dd>{/if}
                {#if b.hmip_address}<dt>HmIP-RF</dt><dd class="hmm-mono">{b.hmip_address}</dd>{/if}
                <dt>{t('Connection')}</dt><dd data-hb-board-state={b.state}>{#if b.state === 'this'}<UseMark who="self" />{' '}{:else if b.state === 'other'}<UseMark who="other" detail={b.connected_to ?? ''} />{' '}{/if}{stateText(b)}</dd>
            </dl>
            <p class="ol-muted"><a href={`http://${view.address}/`} target="_blank" rel="noopener noreferrer">{t("The board's own page (settings, firmware update)")}</a></p>
        {/if}
        {#if admin}
            <div class="lv-grid hb-form">
                <label class="lv-field" for="ol-hb-address">
                    <span>{t('IPv4 address')}</span>
                    <input id="ol-hb-address" class="hmm-input hmm-mono" bind:value={field} placeholder="192.168.1.50" inputmode="decimal" disabled={busy} />
                </label>
            </div>
            <div class="ol-actions">
                <button type="button" class="hmm-button primary" disabled={busy || field.trim() === '' || field.trim() === view.address} onclick={() => save(field.trim())} data-hb-save>{t('Save')}</button>
                {#if view.address !== ''}<button type="button" class="hmm-button" disabled={busy} onclick={remove} data-hb-remove>{t('Remove the board')}</button>{/if}
            </div>
        {/if}
        {#if err}<p class="ol-warn hb-err" data-hb-error>{err}</p>{/if}
        {#if msg}<p class="ol-muted" data-hb-msg>{msg}</p>{/if}
        {#if found}
            {#if found.length === 0}
                <p class="ol-muted" data-hb-found-none>{t('No board answered. A board in another network, or behind this system\'s firewall, does not answer the search: type its address.')}</p>
            {:else}
                <ul class="hb-found" data-hb-found>
                    {#each found as b (b.address)}
                        <li data-hb-found-board={b.address}>
                            <div class="hb-found-main">
                                <strong class="hmm-mono">{b.address}</strong>
                                {#if b.name}<span class="ol-muted">{b.name}</span>{/if}
                                {#if b.error}<span class="ol-muted">{b.error}</span>{:else}
                                    <span>{b.module_type || t('no radio module')} · {t('firmware {v}', {v: b.firmware ?? '—'})} · <span data-hb-found-state={b.state}>{#if b.state === 'this'}<UseMark who="self" />{' '}{:else if b.state === 'other'}<UseMark who="other" detail={b.connected_to ?? ''} />{' '}{/if}{stateText(b)}</span></span>
                                {/if}
                            </div>
                            {#if admin && !b.error}<button type="button" class="hmm-button" disabled={busy} onclick={() => (field = b.address)} data-hb-use>{t('Use this board')}</button>{/if}
                        </li>
                    {/each}
                </ul>
            {/if}
        {/if}
    </div>
{/if}

<style>
    .hb-panel { display: flex; flex-direction: column; gap: 8px; margin: 0 0 16px; }
    .hb-panel p { margin: 0; overflow-wrap: anywhere; }
    .hb-ok { color: var(--hmm-fg); display: flex; align-items: center; gap: 6px; }
    .hb-form { max-width: min(320px, 100%); }
    .hb-found { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
    .hb-found li { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; border: 1px solid var(--hmm-border-muted); border-radius: 6px; padding: 8px 10px; }
    .hb-found-main { display: flex; flex-direction: column; gap: 2px; min-width: 0; overflow-wrap: anywhere; }
</style>
