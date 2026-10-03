<script lang="ts">
    /*
     * openccu-lite task 222 (the maintainer, 2026-09-24): "move lan-devices to their own page,
     * including bidcos-gateways and hmip-access-points". System → LAN devices, beside Interfaces: the
     * devices this system reaches its radio through over the network, in this order - the BidCoS
     * gateways (task 199's cards and their add form, from the Interfaces page), the HB-RF-ETH
     * (task 218, here since task 239), the HmIP access points (task 217) and eQ-3's LAN devices as the NetFinder finds them (task 220). Each
     * section's own action stands in a toolbar row below its heading (lib/SectionHead.svelte).
     * The old anchors of the Interfaces page lead here (lib/systemmenu.ts).
     */
    import {onMount} from 'svelte';
    import {pageLife} from '../lib/pagelife.svelte';
    import {api, type Radio} from '../lib/api';
    import {auth} from '../lib/auth.svelte';
    import {ask, askText} from '../lib/dialog.svelte';
    import {t} from '../lib/i18n.svelte';
    import Loading from '../lib/Loading.svelte';
    import Disclosure from '../lib/Disclosure.svelte';
    import SystemTitle from '../lib/SystemTitle.svelte';
    import SectionHead from '../lib/SectionHead.svelte';
    import AccessPoints from '../lib/AccessPoints.svelte';
    import LANDevices from '../lib/LANDevices.svelte';
    import HBRFETH from '../lib/HBRFETH.svelte';
    import Icon from '../lib/Icon.svelte';
    import SecretInput from '../lib/SecretInput.svelte';
    import {scrollToAnchor} from '../lib/anchor';
    import WarnEdge from '../lib/WarnEdge.svelte';

    interface Gateway { index: number; type: string; name?: string; serial?: string; address?: string; has_key: boolean }
    interface Extra { lan_gateways: Gateway[]; wired_gateways: Gateway[]; pending_key_changes: string[] }
    interface GatewaySpec { type: string; name: string; serial: string; key: string; ip: string }
    type Cls = 'rf' | 'wired';

    const life = pageLife();
    const admin = $derived(auth.role === 'admin');
    let radio = $state<(Radio & Extra) | null>(null);
    let error = $state('');
    let busy = $state('');
    let notice = $state('');

    // task 8: LAN gateways are edited as a whole list per class, like the WebUI's setConfiguration
    const TYPES: Record<Cls, [string, ...string[]]> = {rf: ['Lan Interface', 'HMLGW2'], wired: ['HMWLGW']};
    const TYPE_LABEL: Record<string, string> = {'Lan Interface': 'HM-CFG-LAN (Lan Interface)', HMLGW2: 'HM-LGW-O-TW-W-EU (HMLGW2)', HMWLGW: 'HMW-LGW-O-DR-GS-EU (HMWLGW)'};
    let gwClass = $state<Cls>('rf');
    let gwForm = $state<GatewaySpec>({type: 'HMLGW2', name: '', serial: '', key: '', ip: ''});
    let restartAfter = $state(true);
    // the add form is for a thing one does once, when a gateway arrives: not on the page until it
    // is asked for (maintainer, 2026-09-07), its trigger the button under the section's heading
    let addOpen = $state(false);
    let addButton = $state<HTMLButtonElement | null>(null);
    // task 220: an unconfigured gateway the LAN find lists is taken into the add form
    function pickGateway(cls: Cls, type: string, serial: string, ip: string) {
        gwClass = cls;
        gwForm = {type, name: '', serial, key: '', ip};
        addOpen = true;
        document.getElementById('gateways')?.scrollIntoView({block: 'start', behavior: 'smooth'});
    }
    function switchClass() {
        gwForm = {type: TYPES[gwClass][0], name: '', serial: '', key: '', ip: ''};
    }
    // both classes stand in one grid; every action carries the class of the card it acts on
    const listOf = (c: Cls) => (c === 'rf' ? (radio?.lan_gateways ?? []) : (radio?.wired_gateways ?? []));
    const allGateways = $derived([...listOf('rf').map((g) => ({g, cls: 'rf' as Cls})), ...listOf('wired').map((g) => ({g, cls: 'wired' as Cls}))]);
    async function reloadRadio() {
        radio = await api.get<Radio & Extra>('/api/system/v1/radio');
    }
    /** true when the list was written; a panel only closes on that. */
    async function putGateways(cls: Cls, list: GatewaySpec[]): Promise<boolean> {
        busy = 'gw';
        try {
            const r = await api.put<{restarted: boolean; restart_error?: string; service: string}>('/api/system/v1/radio/lan-gateways', {class: cls, gateways: list, restart: restartAfter});
            notice = r.restarted ? t('{service} restarted.', {service: r.service}) : r.restart_error ? r.restart_error : t('Saved. {service} reads the file at its next start.', {service: r.service});
            await reloadRadio();
            return true;
        } catch (e) {
            notice = (e as Error).message;
            return false;
        } finally {
            busy = '';
        }
    }
    function specOf(g: Gateway): GatewaySpec {
        return {type: g.type, name: g.name ?? '', serial: g.serial ?? '', key: '', ip: g.address ?? ''};
    }
    async function addGateway() {
        if (!(await putGateways(gwClass, [...listOf(gwClass).map(specOf), {...gwForm}]))) return;
        gwForm = {type: TYPES[gwClass][0], name: '', serial: '', key: '', ip: ''};
        addOpen = false;
    }
    async function removeGateway(g: Gateway, cls: Cls) {
        if (!(await ask(t('Remove gateway {serial}? Devices reached only through it become unreachable.', {serial: g.serial ?? String(g.index)})))) return;
        await putGateways(cls, listOf(cls).filter((x) => x.index !== g.index).map(specOf));
    }
    // the maintainer, 2026-09-22: "only possible actions for a gateway: remove and rename"
    async function renameGateway(g: Gateway, cls: Cls) {
        const name = await askText({
            title: t('Rename gateway'),
            message: t('The name is what this system calls the gateway; it is written into its section of the configuration file.'),
            input: {label: t('Name'), initial: g.name ?? '', placeholder: t('e.g. Garage')},
            confirm: t('Rename'),
        });
        if (name === null || name === (g.name ?? '')) return;
        await putGateways(cls, listOf(cls).map((x) => (x.index === g.index ? {...specOf(x), name} : specOf(x))));
    }

    onMount(() => {
        void (async () => {
            try {
                await reloadRadio();
            } catch (e) {
                error = (e as Error).message;
            }
            scrollToAnchor('gateways');
        })();
        // task 177: back from another page - the gateways as they are now
        return life.onReturn(() => void reloadRadio().catch(() => undefined));
    });
</script>

<SystemTitle />
{#if !radio}
    <Loading {error} />
{:else}
    <!-- task 199, the maintainer: "a heading 'BidCoS Gateways', the add gateway button ... and every
         configured gateway as a panel"; task 222: the button under the heading -->
    {#snippet addAction()}
        <!-- task 268: the button becomes the panel - hidden while it is open, the panel's Cancel closes it -->
        <button type="button" class="hmm-button" aria-expanded={addOpen} disabled={busy !== ''} onclick={() => (addOpen = true)} bind:this={addButton} data-gw-add>{t('Add gateway')}</button>
    {/snippet}
    <SectionHead id="gateways" title={t('BidCoS Gateways')} help={t('A LAN gateway carries the radio of another room or another building. BidCos-RF gateways belong to rfd, BidCos-Wired ones to hs485d; both are configured here and reached over the network.')} actions={admin ? addAction : undefined} />
    {#if notice}<div class="ol-notice">{notice}</div>{/if}
    <div class="ol-cards ol-cards-radio ol-panelrow">
        <Disclosure title={t('Add a gateway')} bind:open={addOpen} trigger={addButton}>
            {#snippet help()}{t('Written to rfd.conf / hs485d.conf exactly as the CCU WebUI did. Without an address the gateway is found by its serial on the LAN. The access key is the one printed on the gateway.')}{/snippet}
            <div class="ol-form">
                <!-- the class belongs to the thing being added: it decides which daemon owns the
                     gateway and therefore which types are offered -->
                <label>{t('Class')}
                    <select class="hmm-select" bind:value={gwClass} onchange={switchClass}>
                        <option value="rf">BidCos-RF (rfd)</option>
                        <option value="wired">BidCos-Wired (hs485d)</option>
                    </select>
                </label>
                <label>{t('Type')}
                    <select class="hmm-select" bind:value={gwForm.type}>{#each TYPES[gwClass] as ty (ty)}<option value={ty}>{TYPE_LABEL[ty] ?? ty}</option>{/each}</select>
                </label>
                <label>{t('Name')} <input class="hmm-input" bind:value={gwForm.name} placeholder={t('e.g. Garage')} /></label>
                <label>{t('Serial')} <input class="hmm-input hmm-mono" bind:value={gwForm.serial} placeholder="NEQ0123456" /></label>
                <label>{t('Access key')} <SecretInput bind:value={gwForm.key} label={t('the access key')} /></label>
                <label>{t('Address (optional)')} <input class="hmm-input hmm-mono" bind:value={gwForm.ip} placeholder="192.168.1.50" /></label>
                <div class="ol-form-buttons"><label class="gw-restart"><input type="checkbox" bind:checked={restartAfter} /> {t('Restart the daemon after saving')}</label></div>
                <div class="ol-form-buttons"><button class="hmm-button primary" disabled={busy !== '' || !gwForm.serial} onclick={addGateway}>{t('Add')}</button></div>
            </div>
        </Disclosure>
    </div>
    {#if allGateways.length === 0}
        <div class="ol-muted" data-gw-none>{t('No LAN gateway configured.')}</div>
    {:else}
        <div class="ol-cards ol-cards-radio">
            {#each allGateways as {g, cls} (cls + g.index)}
                <div class="ol-card" data-gateway={g.serial ?? String(g.index)}>
                    <div class="ol-card-head">
                        <span class="ol-card-icon"><Icon name="network" size={14} /></span>
                        <div class="ol-card-titles">
                            <div class="ol-card-title">{g.name || g.serial || `#${g.index}`}</div>
                            <div class="ol-card-sub">{cls === 'rf' ? 'BidCos-RF' : 'BidCos-Wired'} · {g.type}</div>
                        </div>
                    </div>
                    <dl class="ol-kv" style="margin-top:10px">
                        {#if g.serial}<dt>{t('Serial')}</dt><dd class="hmm-mono">{g.serial}</dd>{/if}
                        <dt>{t('Address')}</dt><dd class="hmm-mono">{g.address || t('found by its serial on the LAN')}</dd>
                        <dt>{t('Key')}</dt>
                        <dd>{g.has_key ? '•••' : '—'}{(radio.pending_key_changes ?? []).includes(g.serial ?? '') ? ` (${t('change queued')})` : ''}</dd>
                        <!-- the index is the section number in rfd.conf / hs485d.conf, the order the
                             daemon reads them in -->
                        <dt>#</dt><dd class="hmm-mono">{g.index}</dd>
                    </dl>
                    {#if admin}
                        <div class="ol-actions gw-actions">
                            <button class="hmm-button" disabled={busy !== ''} onclick={() => renameGateway(g, cls)}>{t('Rename')}</button>
                            <button class="hmm-button" disabled={busy !== ''} onclick={() => removeGateway(g, cls)}>{t('Remove')}</button>
                        </div>
                    {/if}
                </div>
            {/each}
        </div>
    {/if}
    <!-- task 218's network radio board, here since task 239 (the maintainer: "wouldnt it make sense
         to move the hb-rf-eth config to the lan-devices page?"): a LAN device like the gateways,
         after them - both carry a radio over the network - and before the access points -->
    <WarnEdge ids={['hb-rf-eth']}><HBRFETH {admin} /></WarnEdge>
    <!-- task 217: the HmIP access points (HAP, DRAP) paired to HmIP-RF; shown while HmIP-RF is in
         the interface list -->
    <AccessPoints />
    <!-- task 220: eQ-3's LAN devices as NetFinder finds them; an unconfigured gateway is taken into
         the add form above -->
    <LANDevices {admin} onpick={pickGateway} />
{/if}

<style>
    /* a 24-digit SGTIN in the mono face fits the wider track (27.1) */
    .ol-cards.ol-cards-radio { grid-template-columns: repeat(auto-fill, minmax(min(340px, 100%), 1fr)); }
    .ol-cards-radio > .ol-card { display: flex; flex-direction: column; }
    /* task 199: the panel that opens from a heading's button is two of the three card columns wide
       and lines up with the cards below it; on a narrow window it takes the one column */
    .ol-panelrow { margin-bottom: 0; }
    .ol-panelrow > :global(.ol-disclosure-slot) { grid-column: 1 / span 2; min-width: 0; }
    .ol-panelrow :global(.ol-disclosure) { max-width: none; }
    .gw-actions { margin-top: auto; padding-top: 12px; }
    .gw-restart { display: inline-flex; align-items: center; gap: 6px; }
</style>
