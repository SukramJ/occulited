<script lang="ts">
    /*
     * openccu-lite task 217 (the maintainer, 2026-09-24): the HmIP access points paired to this
     * system - HAPs and DRAPs, second access points of HmIP-RF reached over the LAN - as cards
     * beside the BidCoS gateways (on System → LAN devices since task 222), with what the firewall does with the ports they connect to (task
     * 181's hint, D-110's cases, answered by the API so the page only renders it). No list of
     * "incompatible devices": a paired access point gets its firmware like any device (B-195).
     */
    import {onMount} from 'svelte';
    import {link} from './router.svelte';
    import {pageLife} from './pagelife.svelte';
    import {api} from './api';
    import {newerFirmware, shownVersion} from './fwversion';
    import {t} from './i18n.svelte';
    import SectionHead from './SectionHead.svelte';
    import Icon from './Icon.svelte';
    import {foundBySerial, runningIP} from './lanscan.svelte';
    import FirewallVerdicts, {type PortVerdict} from './FirewallVerdicts.svelte';

    interface AccessPoint {
        address: string;
        type: string;
        firmware: string;
        available_firmware?: string;
        firmware_update_state?: string;
        reachable: boolean | null;
        ip_address?: string;
        duty_cycle?: number;
        duty_cycle_limit?: boolean;
        carrier_sense?: number;
        config_pending?: boolean;
        update_pending?: boolean;
        connected?: boolean;
        error?: string;
        sgtin?: string;
        name?: string;
        latest?: string;
        update_available?: boolean;
    }
    interface View {
        interface: string;
        available: boolean;
        error?: string;
        access_points: AccessPoint[];
        firewall: {ports: PortVerdict[]; hint: 'keep' | 'blocked' | 'unused' | 'reopen' | 'unknown'} | null;
    }

    let view = $state<View | null>(null);
    async function load() {
        try {
            view = await api.get<View>('/api/system/v1/radio/hmip/access-points');
        } catch {
            view = null;
        }
    }
    const life = pageLife();
    onMount(() => {
        // reachability and the duty cycle change: every 60 s while the page shows (task 177)
        let timer: ReturnType<typeof setTimeout> | undefined;
        let mounted = true;
        const tick = async () => {
            if (life.active) await load();
            if (mounted) timer = setTimeout(tick, 60000);
        };
        void tick();
        const stopReturn = life.onReturn(() => void load());
        return () => {
            mounted = false;
            clearTimeout(timer);
            stopReturn();
        };
    });

    // an update: eQ-3 lists a newer version (B-195), or hmipserver has one deployed for the device
    const updateWaiting = (ap: AccessPoint) => !!ap.update_available || newerFirmware(shownVersion(ap.available_firmware), ap.firmware);
    function stateOf(ap: AccessPoint): {dot: string; text: string} {
        if (ap.reachable === true) return {dot: 'ok', text: t('reachable')};
        if (ap.reachable === false) return {dot: 'err', text: t('unreachable')};
        return {dot: '', text: t('not known yet')};
    }
    const HINT: Record<string, string> = {
        keep: 'An access point is paired: keep these rules on ACCEPT.',
        blocked: 'A port the access points need is not accepted: they cannot reach the system. Set these rules to ACCEPT on the Firewall page.',
        unused: 'No HmIP access point is paired: these rules can be set to REJECT on the Firewall page.',
        reopen: 'Set these rules to ACCEPT again before pairing an HmIP access point; it cannot reach the system otherwise.',
        unknown: 'HmIP-RF does not answer, so whether an access point is paired is not known. An access point needs these ports accepted.',
    };
</script>

{#if view?.available}
    <SectionHead id="access-points" title={t('HmIP Access Points')} help={t('An HmIP access point (HAP, DRAP) is a second access point of HmIP-RF, reached over the network. It is paired like a device: reset it to its factory settings, start the install mode with its SGTIN and key, then power it. The system and the access point must be in the same network. It reaches the system on udp 43438 and fetches its firmware on tcp 9293 and 9294; a paired access point gets firmware updates like any device.')} />
    {#if view.error}
        <div class="ol-muted" data-ap-error>{t('HmIP-RF does not answer: {error}', {error: view.error})}</div>
    {:else if view.access_points.length === 0}
        <div class="ol-muted" data-ap-none>{t('No HmIP access point is paired.')}</div>
    {:else}
        <div class="ol-cards ol-cards-radio">
            {#each view.access_points as ap (ap.address)}
                {@const st = stateOf(ap)}
                {@const lanDev = foundBySerial(ap.sgtin)}
                <div class="ol-card" data-access-point={ap.address}>
                    <div class="ol-card-head">
                        <span class="ol-card-icon"><Icon name="network" size={14} /></span>
                        <div class="ol-card-titles">
                            <div class="ol-card-title">{ap.name || ap.type}</div>
                            <div class="ol-card-sub">HmIP-RF · {ap.type}</div>
                        </div>
                    </div>
                    <dl class="ol-kv" style="margin-top:10px">
                        <dt>{t('State')}</dt>
                        <dd data-ap-state><span class="ol-dot {st.dot}"></span>{st.text}{#if ap.config_pending}<span class="ol-badge warn">{t('configuration pending')}</span>{/if}</dd>
                        {#if ap.sgtin}<dt>SGTIN</dt><dd class="hmm-mono">{ap.sgtin}</dd>{/if}
                        <dt>{t('Address')}</dt><dd class="hmm-mono">{ap.address}</dd>
                        <!-- task 220: the address from the LAN find when hmipserver has none, how it is
                             configured, and whether it is in this system's network -->
                        <dt>{t('IP address')}</dt>
                        <dd class="hmm-mono" data-ap-ip>{ap.ip_address || (lanDev ? runningIP(lanDev) : '—')}{#if lanDev?.config}<span class="ol-badge">{lanDev.config.dhcp ? 'DHCP' : t('static')}</span>{/if}{#if lanDev && !lanDev.same_subnet}<span class="ol-badge warn">{t('not in this network')}</span>{/if}</dd>
                        <dt>{t('Firmware')}</dt>
                        <dd data-ap-firmware>{ap.firmware || '—'}{#if updateWaiting(ap)}{' '}<a class="ol-badge ol-badge-accent" href="/system/updates#device-firmware" use:link>{t('update available')}{ap.latest ? `: ${ap.latest}` : ''}</a>{/if}</dd>
                        {#if ap.duty_cycle !== undefined}
                            <dt>{t('Duty cycle')}</dt>
                            <dd data-ap-duty>{Math.round(ap.duty_cycle * 10) / 10} %{#if ap.duty_cycle_limit}<span class="ol-badge bad">{t('limit reached')}</span>{/if}</dd>
                        {/if}
                        {#if ap.carrier_sense !== undefined}<dt>{t('Carrier sense')}</dt><dd>{ap.carrier_sense} %</dd>{/if}
                    </dl>
                    {#if ap.error}<div class="ol-muted ap-error">{t('Channel 0 does not answer: {error}', {error: ap.error})}</div>{/if}
                </div>
            {/each}
        </div>
    {/if}
    {#if view.firewall}
        <FirewallVerdicts ports={view.firewall.ports} text={t(HINT[view.firewall.hint] ?? HINT.unknown!)} error={view.firewall.hint === 'blocked'} data-ap-hint={view.firewall.hint} />
    {/if}
{/if}

<style>
    /* the LAN devices page's card grid (.ol-cards-radio, scoped there): a 24-digit SGTIN fits */
    .ol-cards.ol-cards-radio { grid-template-columns: repeat(auto-fill, minmax(min(340px, 100%), 1fr)); }
    .ap-error { margin-top: 8px; }
</style>
