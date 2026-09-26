<script lang="ts">
    /*
     * openccu-lite task 220 (the maintainer, Q&A 2026-09-24): NetFinder's two features that people
     * use, on System → LAN devices (the Interfaces page's until task 222) - find eQ-3's LAN devices, and set the network of a LAN gateway or
     * an HmIP access point. Other systems are listed with a link and never written to. The access
     * point's sticker password is asked every time and never stored; a configured gateway's key is
     * the system's own (rfd.conf / hs485d.conf) and never shown. No key change, no reboot, no
     * firmware, no factory reset.
     */
    import {onMount} from 'svelte';
    import {api, ApiError} from './api';
    import {lan, loadLAN, searchLAN, runningIP, type LANDevice} from './lanscan.svelte';
    import {runTime, runTimeFull} from './timers';
    import {i18n, t} from './i18n.svelte';
    import SectionHead from './SectionHead.svelte';
    import Icon from './Icon.svelte';
    import Loading from './Loading.svelte';
    import Disclosure from './Disclosure.svelte';
    import SecretInput from './SecretInput.svelte';
    import UseMark from './UseMark.svelte';

    // onpick: an unconfigured gateway is taken into the BidCoS gateways' add form
    let {admin = false, onpick}: {admin?: boolean; onpick?: (cls: 'rf' | 'wired', type: string, serial: string, ip: string) => void} = $props();

    // task 237 (the maintainer): opening the page sends nothing to the network - it shows the last
    // search the system kept; only the Search button runs the discovery
    onMount(() => {
        void loadLAN();
    });

    const devices = $derived(lan.scan?.devices ?? []);
    const TYPE_LABEL: Record<string, string> = {
        'eQ3-HM-LGW-App': 'HM-LGW-O-TW-W-EU',
        'eQ3-HMW-LGW-App': 'HMW-LGW-O-DR-GS-EU',
        'eQ3-HMIP-HAP-App': 'HmIP-HAP',
        'eQ3-HmIPW-DRAP-App': 'HmIPW-DRAP',
    };
    const label = (d: LANDevice) => TYPE_LABEL[d.type] ?? d.type.replace(/^eQ3-/, '').replace(/-App$/, '');
    function mode(d: LANDevice): string {
        if (!d.config) return '—';
        if (d.config.dhcp) return d.config.auto_ip ? t('DHCP, then Auto IP') : 'DHCP';
        return t('static');
    }

    // the settings dialog
    let editing = $state<LANDevice | null>(null);
    let form = $state({dhcp: true, ip: '', netmask: '', gateway: '', dns1: '', dns2: '', password: '', other_subnet: false});
    let otherSubnet = $state(false);
    let busy = $state(false);
    let formError = $state('');
    let result = $state('');
    // task 247: the button opens the panel in its card, and closes it again
    let panelOpen = $state(false);
    // task 268: each card's Network settings button, hidden while its panel is open
    let editButtons = $state<Record<string, HTMLButtonElement>>({});
    function edit(d: LANDevice) {
        if (panelOpen && editing?.serial === d.serial) {
            panelOpen = false;
            return;
        }
        const c = d.config;
        const r = d.runtime;
        // the static fields start from what it runs with - the DHCP lease is the likely static address
        form = {
            dhcp: c?.dhcp ?? true,
            ip: c && !c.dhcp ? c.ip : (r?.ip ?? d.ip),
            netmask: c && !c.dhcp ? c.netmask : (r?.netmask ?? '255.255.255.0'),
            gateway: c && !c.dhcp ? c.gateway : (r?.gateway ?? ''),
            dns1: c && !c.dhcp ? c.dns1 : (r?.dns1 ?? ''),
            dns2: c && !c.dhcp ? c.dns2 : (r?.dns2 ?? ''),
            password: '',
            other_subnet: false,
        };
        otherSubnet = false;
        formError = '';
        editing = d;
        panelOpen = true;
    }
    async function save() {
        if (!editing) return;
        busy = true;
        formError = '';
        try {
            const r = await api.post<{written: boolean; restarts: boolean; gateway_file?: boolean; restarted?: string; restart_error?: string; refind_error?: string; device?: LANDevice; differs?: string[]}>(
                `/api/system/v1/radio/lan-devices/${encodeURIComponent(editing.serial)}/network`,
                form.dhcp ? {dhcp: true, password: form.password} : form,
            );
            const parts = [t('{device}: the network settings were taken.', {device: editing.name || label(editing)})];
            if (r.device) parts.push(t('It answers with {ip} now.', {ip: runningIP(r.device)}));
            if (r.gateway_file) parts.push(t('Its address in the configuration was updated and {service} restarted.', {service: r.restarted ?? ''}));
            if (r.restart_error) parts.push(r.restart_error);
            // the lab's HAP-B1 and HMW-LGW took a static address and still reported DHCP on
            if (r.differs?.includes('dhcp')) parts.push(t('The device kept DHCP on and stored the addresses as its fallback. The HmIP access point tested in the lab does not switch to a static address, not even after a restart: give it a fixed address in the DHCP server instead.'))
            if (r.refind_error) parts.push(t('It has not answered with the new settings yet; search again in a minute.'));
            result = parts.join(' ');
            panelOpen = false;
            await loadLAN();
        } catch (e) {
            const err = e as ApiError;
            if (err.code === 'other-subnet') otherSubnet = true;
            formError =
                err.code === 'wrong-password'
                    ? t('The device did not take the password.')
                    : err.code === 'address-in-use'
                      ? t('Something on the network already answers for this address.')
                      : err.code === 'other-subnet'
                        ? t('The address is in none of the networks of the system: the device would not be reachable from here. Tick the checkbox below to set it anyway.')
                        : err.message;
        } finally {
            busy = false;
        }
    }
    const passwordLabel = (d: LANDevice) => (d.kind === 'access-point' ? t('Password (PW on the sticker)') : t('Access key of the gateway'));
</script>

{#snippet settingsForm(d: LANDevice)}
        <div class="ol-form lan-form" data-lan-form>
            <label><input type="radio" name="lan-mode" checked={form.dhcp} onchange={() => (form.dhcp = true)} /> {t('DHCP (and Auto IP without a DHCP server)')}</label>
            <label><input type="radio" name="lan-mode" checked={!form.dhcp} onchange={() => (form.dhcp = false)} /> {t('Static address')}</label>
            {#if d.kind === 'access-point'}<p class="ol-muted" data-lan-ap-dhcp>{t('An HmIP access point keeps DHCP on: a static address is only stored as its fallback. A fixed address comes from a reservation in the DHCP server.')}</p>{/if}
            {#if !form.dhcp}
                <label>{t('IP address')} <input class="hmm-input hmm-mono" bind:value={form.ip} data-lan-field="ip" /></label>
                <label>{t('Netmask')} <input class="hmm-input hmm-mono" bind:value={form.netmask} data-lan-field="netmask" /></label>
                <label>{t('Gateway')} <input class="hmm-input hmm-mono" bind:value={form.gateway} data-lan-field="gateway" /></label>
                <label>DNS 1 <input class="hmm-input hmm-mono" bind:value={form.dns1} /></label>
                <label>DNS 2 <input class="hmm-input hmm-mono" bind:value={form.dns2} /></label>
                {#if otherSubnet}<label><input type="checkbox" bind:checked={form.other_subnet} data-lan-other /> {t('I know the device will not be reachable from this system')}</label>{/if}
            {/if}
            {#if d.password === 'configured'}
                <p class="ol-muted">{t("The gateway's key is taken from the configuration of the system.")}</p>
            {:else}
                <label>{passwordLabel(d)} <SecretInput bind:value={form.password} label={passwordLabel(d)} /></label>
                <p class="ol-muted">{t('Used for this change only; it is not stored.')}</p>
            {/if}
            <p class="ol-muted">{d.kind === 'access-point' ? t('The access point restarts its network and is away for about a minute; HmIP-RF reconnects when it is back.') : t('The gateway restarts its network and is away for a moment. A gateway in the configuration of the system gets its new address there as well, and its daemon restarts.')}</p>
            {#if formError}<div class="ol-warn" data-lan-error>{formError}</div>{/if}
            <div class="ol-form-buttons"><button type="button" class="hmm-button primary" disabled={busy || (d.password === 'sticker' && !form.password)} onclick={() => void save()} data-lan-save>{busy ? t('Waiting for the device…') : t('Save')}</button></div>
        </div>
{/snippet}

{#snippet searchButton()}
    <button type="button" class="hmm-button ol-icon-button" disabled={lan.busy} onclick={() => void searchLAN()} data-lan-search><Icon name="search" /> {lan.busy && lan.scan ? t('Searching…') : t('Search')}</button>
{/snippet}
<SectionHead id="lan-devices" title={t('LAN devices')} help={t("eQ-3's LAN devices in this network, as the NetFinder finds them: the LAN gateways, the HmIP access points and other systems. The network of a gateway or an access point can be set here; nothing else is changed on a device from here. A device set to an address outside this network is still found and can be set back.")} actions={searchButton} />
{#if result}<div class="ol-muted lan-result" data-lan-result>{result}</div>{/if}
{#if lan.scan?.scanned}<div class="ol-muted lan-when" data-lan-scanned title={runTimeFull(lan.scan.scanned, i18n.language)}>{t('Last search: {time}', {time: runTime(lan.scan.scanned, i18n.language)})}</div>{/if}
{#if !lan.scan}
    {#if lan.error}<div class="ol-muted">{lan.error}</div>{:else}<Loading />{/if}
{:else if !lan.scan.scanned}
    <div class="ol-muted" data-lan-unsearched>{lan.error || t('Not searched yet. Search sends the eQ-3 discovery to the local network; nothing is sent before you press it.')}</div>
{:else if devices.length === 0}
    <div class="ol-muted" data-lan-none>{lan.scan.error ?? t("No answers. Is the firewall rule for the eQ-3 discovery replies (from udp 43439) still there?")}</div>
{:else}
    {#if devices.some((d) => d.configured || d.paired)}
        <!-- task 238: what the mark says; another system's use cannot be told for these devices without
             taking them from it (a LAN gateway serves one client), so there is no grey mark here -->
        <p class="ol-muted lan-legend" data-lan-legend><UseMark who="self" /> {t('used by this system. Whether another system uses a gateway or an access point cannot be told from here without taking it from that system, so it is not marked.')}</p>
    {/if}
    <div class="ol-cards ol-cards-radio">
        {#each devices as d (d.type + d.serial)}
            <div class="ol-card" data-lan-device={d.serial} data-kind={d.kind}>
                <div class="ol-card-head">
                    <span class="ol-card-icon"><Icon name={d.kind === 'ccu' ? 'radio' : 'network'} size={14} /></span>
                    <div class="ol-card-titles">
                        <div class="ol-card-title lan-title">{d.name || label(d)}{#if d.configured}<UseMark who="self" detail={d.configured === 'rf' ? 'BidCos-RF (rfd)' : 'BidCos-Wired (hs485d)'} />{:else if d.paired}<UseMark who="self" detail={t('paired to HmIP-RF')} />{/if}</div>
                        <div class="ol-card-sub">{label(d)} · <span class="hmm-mono">{d.serial}</span></div>
                    </div>
                </div>
                <dl class="ol-kv" style="margin-top:10px">
                    <dt>{t('IP address')}</dt>
                    <dd class="hmm-mono" data-lan-ip>{runningIP(d)}{#if !d.same_subnet}<span class="ol-badge warn">{t('not in this network')}</span>{/if}</dd>
                    {#if d.kind !== 'ccu'}
                        <dt>{t('Network')}</dt><dd data-lan-mode>{mode(d)}</dd>
                        {#if d.runtime}
                            <dt>{t('Netmask')}</dt><dd class="hmm-mono">{d.runtime.netmask}</dd>
                            <dt>{t('Gateway')}</dt><dd class="hmm-mono">{d.runtime.gateway}</dd>
                        {/if}
                    {/if}
                    <dt>{t('Firmware')}</dt><dd>{d.version}</dd>
                    {#if d.configured}<dt>{t('In use')}</dt><dd>{d.configured === 'rf' ? 'BidCos-RF (rfd)' : 'BidCos-Wired (hs485d)'}</dd>{/if}
                    {#if d.paired}<dt>{t('In use')}</dt><dd>{t('paired to HmIP-RF')}</dd>{/if}
                </dl>
                <div class="ol-actions lan-actions">
                    {#if d.link}<a class="hmm-button" href={d.link} target="_blank" rel="noopener noreferrer">{t('Open its web UI')}</a>{/if}
                    {#if admin && d.writable}<button type="button" class="hmm-button" aria-expanded={editing?.serial === d.serial && panelOpen} onclick={() => edit(d)} bind:this={editButtons[d.serial]} data-lan-edit>{t('Network settings')}</button>{/if}
                    {#if admin && d.kind === 'gateway' && !d.configured && onpick}
                        <button type="button" class="hmm-button" data-lan-pick onclick={() => onpick(d.type === 'eQ3-HMW-LGW-App' ? 'wired' : 'rf', d.type === 'eQ3-HMW-LGW-App' ? 'HMWLGW' : 'HMLGW2', d.serial, runningIP(d))}>{t('Add as gateway')}</button>
                    {/if}
                </div>
                <!-- task 247 (the maintainer): the settings open in the card, as an in-page panel that grows
                     out of the button, not a dialog -->
                {#if admin && d.writable}
                    <Disclosure title={t('Network settings')} trigger={editButtons[d.serial]} bind:open={() => editing?.serial === d.serial && panelOpen, (v) => { if (!v && editing?.serial === d.serial) panelOpen = false; }}>
                        {#if editing?.serial === d.serial}{@render settingsForm(d)}{/if}
                    </Disclosure>
                {/if}
            </div>
        {/each}
    </div>
{/if}



<style>
    .lan-title { display: flex; align-items: center; gap: 6px; }
    .lan-legend { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin: 0 0 8px; }
    .ol-cards.ol-cards-radio { grid-template-columns: repeat(auto-fill, minmax(min(340px, 100%), 1fr)); }
    .ol-cards-radio > .ol-card { display: flex; flex-direction: column; }
    .lan-actions { margin-top: auto; padding-top: 12px; flex-wrap: wrap; gap: 6px; }
    /* a button's words stay on one line; two that do not fit side by side stack (the German ones) */
    .lan-actions > :global(*) { white-space: nowrap; }
    .lan-actions > a.hmm-button { text-decoration: none; color: var(--hmm-fg); }
    .lan-result { margin-bottom: 10px; }
</style>
