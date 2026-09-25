<script lang="ts">
    /*
     * Task 89: the Wi-Fi interface's panel on the Network page, half its width beside Ethernet's.
     * The switch (off unloads the driver), the connection (network, band and channel, signal,
     * bitrate, addresses), scan and connect, the saved networks with Forget, and - for the
     * administrator - the country, the addressing and the preferred interface. A change made over
     * the Wi-Fi itself is reverted after 90 s unless confirmed here.
     */
    import {onMount, type Snippet} from 'svelte';
    import {pageLife} from './pagelife.svelte';
    import {api} from './api';
    import {ask, askText} from './dialog.svelte';
    import {t} from './i18n.svelte';
    import Icon from './Icon.svelte';
    import Help from './Help.svelte';
    import Loading from './Loading.svelte';
    import {band, bars, COUNTRIES, joinable, type ScanResult, type WiFiSettings, type WiFiView} from './wifi';
    import {parseWifiCode} from './devicekeys';
    import QrScanner from './QrScanner.svelte';
    import type {NetIface} from './netpanels';

    // iface: the live interface while its driver is loaded; details: the Network page's own rendering
    // of an interface's MAC, driver, addresses and traffic, shared with the other panels
    let {admin = false, iface, details, onenabled}: {admin?: boolean; iface?: NetIface; details?: Snippet<[NetIface]>; onenabled?: (on: boolean) => void} = $props();

    let view = $state<WiFiView | null>(null);
    // task 226: the page hides the interface's address panels while Wi-Fi is switched off
    $effect(() => {
        if (view) onenabled?.(view.settings.enabled);
    });
    let error = $state('');
    let busy = $state('');
    let scan = $state<ScanResult[] | null>(null);
    let saved = $state<string[]>([]);
    // the settings form, taken from the view once and after each save
    let form = $state<WiFiSettings | null>(null);
    let dnsText = $state('');
    let now = $state(Date.now());

    async function load(resetForm = false) {
        try {
            const v = await api.get<WiFiView>('/api/system/v1/wifi');
            view = v;
            if (resetForm || !form) {
                form = {...v.settings, dns: [...(v.settings.dns ?? [])]};
                dnsText = (v.settings.dns ?? []).join(', ');
            }
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
    }
    const life = pageLife();
    onMount(() => {
        void load(true);
        const poll = setInterval(() => life.active && void load(), 5000);
        const clock = setInterval(() => (now = Date.now()), 1000);
        const stop = life.onReturn(() => void load());
        return () => {
            clearInterval(poll);
            clearInterval(clock);
            stop();
        };
    });

    const chip = $derived(view?.chips.find((c) => c.iface === view?.settings.iface) ?? view?.chips[0]);
    const confirmLeft = $derived(view?.confirm ? Math.max(0, Math.round((new Date(view.confirm).getTime() - now) / 1000)) : 0);

    function stateWord(s: string): string {
        switch (s) {
            case 'off': return t('off');
            case 'starting': return t('starting');
            case 'not-configured': return t('no network saved');
            case 'connecting': return t('connecting');
            case 'connected': return t('connected');
            case 'disconnected': return t('not connected');
        }
        return s;
    }
    function securityWord(s: string): string {
        switch (s) {
            case 'open': return t('open');
            case 'wpa2': return 'WPA2';
            case 'wpa3': return 'WPA3';
            case 'wpa2-wpa3': return 'WPA2/WPA3';
            case 'enterprise': return t('Enterprise (not supported)');
            case 'wep': return t('WEP (not supported)');
        }
        return s;
    }

    async function run(what: string, fn: () => Promise<void>) {
        busy = what;
        try {
            await fn();
            error = '';
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    async function toggle() {
        if (!view || !form) return;
        const on = !view.settings.enabled;
        if (!on && view.state === 'connected' && !(await ask({message: t('Switch Wi-Fi off? The connection drops and the radio and its driver are switched off. The saved networks are kept.'), confirm: t('Switch off')}))) return;
        await run('switch', async () => {
            view = await api.put<WiFiView>('/api/system/v1/wifi', {...view!.settings, enabled: on});
            form = {...view.settings, dns: [...(view.settings.dns ?? [])]};
        });
    }

    async function doScan() {
        await run('scan', async () => {
            const r = await api.post<{networks: ScanResult[]; saved: string[]}>('/api/system/v1/wifi/scan', {});
            scan = r.networks;
            saved = r.saved;
        });
    }

    async function connect(ssid: string, security: string, hidden = false) {
        let password = '';
        if (security !== 'open') {
            const p = await askText({
                title: t('Connect to {ssid}', {ssid}),
                message: security === 'wpa3' ? t('The password of the network. WPA3 needs the password itself, so the system keeps it in its settings file (readable by root only).') : t('The password of the network. The system keeps only the key derived from it.'),
                input: {label: t('Password'), type: 'password', minLength: 8},
                confirm: t('Connect'),
            });
            if (p === null) return;
            password = p;
        }
        await run('connect', async () => {
            view = await api.post<WiFiView>('/api/system/v1/wifi/networks', {ssid, security, password, hidden});
            scan = null;
        });
    }

    // a network's QR code (WIFI:T:WPA;S:…;P:…;;), from a router's sticker or a phone's share
    // screen, read by task 154's scanner: the name, the password and hidden in one go
    let qrOpen = $state(false);
    let qrNote = $state('');
    async function wifiScanned(text: string): Promise<boolean> {
        const code = parseWifiCode(text);
        if (!code) {
            qrNote = t('That QR code is not a Wi-Fi code (WIFI:…).');
            return false;
        }
        qrNote = '';
        const inScan = scan?.find((x) => x.ssid === code.ssid)?.security;
        const security = code.type === 'NOPASS' ? 'open' : code.type === 'SAE' ? 'wpa3' : code.type === 'WPA' ? (inScan && joinable(inScan) && inScan !== 'open' ? inScan : 'wpa2') : '';
        if (!security) {
            qrNote = t('{ssid} uses {type}, which the system cannot join.', {ssid: code.ssid, type: code.type});
            return true;
        }
        qrOpen = false;
        if (!(await ask({title: t('Connect to {ssid}', {ssid: code.ssid}), message: t('The QR code carries the name and the password of the network.'), confirm: t('Connect')}))) return true;
        await run('connect', async () => {
            view = await api.post<WiFiView>('/api/system/v1/wifi/networks', {ssid: code.ssid, security, password: code.password, hidden: code.hidden});
            scan = null;
        });
        return true;
    }

    async function connectHidden() {
        const ssid = await askText({title: t('Hidden network'), message: t('The name (SSID) of the network, exactly as it is set in the router.'), input: {label: 'SSID'}, confirm: t('Next')});
        if (!ssid) return;
        await connect(ssid, 'wpa2', true);
    }

    async function forget(ssid: string) {
        if (!(await ask({message: t('Forget {ssid}? The system no longer connects to it.', {ssid}), confirm: t('Forget'), danger: true}))) return;
        await run('forget', async () => {
            view = await api.del<WiFiView>(`/api/system/v1/wifi/networks/${encodeURIComponent(ssid)}`);
        });
    }

    async function saveSettings() {
        if (!form || !view) return;
        const s: WiFiSettings = {...form, dns: dnsText.split(/[\s,]+/).filter(Boolean)};
        if (s.mode === 'dhcp') {
            delete s.address;
            delete s.netmask;
            delete s.gateway;
            s.dns = [];
        }
        await run('save', async () => {
            view = await api.put<WiFiView>('/api/system/v1/wifi', s);
            form = {...view.settings, dns: [...(view.settings.dns ?? [])]};
            dnsText = (view.settings.dns ?? []).join(', ');
        });
    }

    async function confirmChange() {
        await run('confirm', async () => {
            await api.post('/api/system/v1/wifi/confirm', {});
            await load();
        });
    }
</script>

<div class="ol-card ol-wifi" data-iface={view?.settings.iface ?? 'wlan0'} data-loading={view || error ? undefined : ''}>
    <div class="ol-card-head">
        <span class="ol-card-icon"><Icon name="wifi" size={14} /></span>
        <div class="ol-card-titles">
            <div class="ol-card-title">{view?.settings.iface ?? 'wlan0'}</div>
            <div class="ol-card-sub">{[t('Wi-Fi'), chip?.kind === 'usb' ? t('USB stick') : '', iface?.default_route ? t('default route') : '', view?.settings.enabled && view.settings.preferred === 'wlan' ? t('preferred') : ''].filter(Boolean).join(' · ')}</div>
        </div>
        {#if view}
            <span class="ol-badge ol-pill" class:good={view.state === 'connected'} class:warn={view.state === 'disconnected'} data-state={view.state}>{stateWord(view.state)}</span>
        {/if}
    </div>
    {#if error}<div class="ol-notice error">{error}</div>{/if}
    {#if !view}
        {#if !error}<Loading />{/if}
    {:else}
        {#if view.confirm && confirmLeft > 0}
            <div class="ol-notice warn wifi-confirm">
                {t('This change was made over the Wi-Fi. It is reverted in {s} s unless you confirm it.', {s: String(confirmLeft)})}
                <button type="button" class="hmm-button" disabled={busy !== ''} onclick={confirmChange}>{t('Confirm')}</button>
            </div>
        {/if}
        {#if view.setup_error}<div class="ol-notice warn">{t('The setup file on the SD card was not used: {why}', {why: view.setup_error})}</div>{/if}
        {#if admin}
            <label class="wifi-switch">
                <input type="checkbox" role="switch" checked={view.settings.enabled} disabled={busy !== ''} onchange={toggle} />
                {t('Wi-Fi on')}
            </label>
        {/if}
        {#if view.state === 'connected' && view.status}
            {@const b = band(view.status.freq ?? view.signal?.freq ?? 0)}
            <dl class="ol-kv wifi-conn">
                <dt>{t('Network')}</dt><dd>{view.status.ssid}</dd>
                {#if b}<dt>{t('Band')}</dt><dd>{b.band} · {t('channel {n}', {n: String(b.channel)})}</dd>{/if}
                {#if view.signal}
                    <dt>{t('Signal')}</dt>
                    <dd><span class="wifi-bars" data-bars={bars(view.signal.rssi)} aria-label={t('{n} of 4 bars', {n: String(bars(view.signal.rssi))})}>{#each [1, 2, 3, 4] as n (n)}<span class:on={n <= bars(view.signal.rssi)}></span>{/each}</span> {view.signal.rssi} dBm</dd>
                    <dt>{t('Speed')}</dt><dd>{t('{speed} Mbit/s', {speed: String(view.signal.link_speed)})}</dd>
                {/if}
                <!-- the addresses are the page's IPv4 and IPv6 panels below this one (task 221), when the page passes its details -->
                {#if !details}<dt>IPv4</dt><dd class="hmm-mono">{#each view.addresses as a (a)}<div>{a}</div>{:else}<span class="ol-muted">–</span>{/each}</dd>{/if}
            </dl>
        {:else if view.state === 'off'}
            <p class="ol-muted">{t('The radio and its driver are off.')}</p>
        {/if}
        {#if iface && details}{@render details(iface)}{/if}

        {#if view.settings.enabled}
            <h3>{t('Networks')}</h3>
            {#if view.networks.length}
                <ul class="wifi-list" data-list="saved">
                    {#each view.networks as n (n.ssid)}
                        <li>
                            <span class="wifi-ssid">{n.ssid}</span>
                            <span class="ol-muted">{securityWord(n.security)}{n.hidden ? ` · ${t('hidden')}` : ''}{view.status?.ssid === n.ssid && view.state === 'connected' ? ` · ${t('connected')}` : ''}</span>
                            {#if admin}<button type="button" class="hmm-button" disabled={busy !== ''} onclick={() => forget(n.ssid)}>{t('Forget')}</button>{/if}
                        </li>
                    {/each}
                </ul>
            {:else}
                <p class="ol-muted">{t('No network saved yet.')}</p>
            {/if}
            {#if admin}
                <div class="ol-toolbar">
                    <button type="button" class="hmm-button" disabled={busy !== '' || view.state === 'starting'} onclick={doScan}>{busy === 'scan' ? t('Scanning…') : t('Scan')}</button>
                    <button type="button" class="hmm-button" disabled={busy !== ''} onclick={connectHidden}>{t('Hidden network…')}</button>
                    <button type="button" class="hmm-button" disabled={busy !== ''} onclick={() => ((qrOpen = !qrOpen), (qrNote = ''))} aria-expanded={qrOpen} data-action="wifi-qr">{t('QR code…')}</button>
                </div>
                {#if qrOpen}
                    <div class="wifi-qr" data-panel="wifi-qr">
                        <p class="ol-muted">{t("A router's sticker or a phone's \"share Wi-Fi\" screen shows the network as a QR code.")}</p>
                        <QrScanner onresult={wifiScanned} />
                        {#if qrNote}<p class="ol-warn" data-note="wifi-qr">{qrNote}</p>{/if}
                    </div>
                {/if}
                {#if scan}
                    <ul class="wifi-list" data-list="scan">
                        {#each scan.filter((s) => !s.hidden) as s (s.ssid)}
                            {@const sb = band(s.freq)}
                            <li>
                                <span class="wifi-bars" data-bars={bars(s.signal)}>{#each [1, 2, 3, 4] as n (n)}<span class:on={n <= bars(s.signal)}></span>{/each}</span>
                                <span class="wifi-ssid">{s.ssid}</span>
                                <span class="ol-muted">{securityWord(s.security)}{sb ? ` · ${sb.band}` : ''}{saved.includes(s.ssid) ? ` · ${t('saved')}` : ''}</span>
                                {#if joinable(s.security)}<button type="button" class="hmm-button" disabled={busy !== ''} onclick={() => connect(s.ssid, s.security)}>{t('Connect')}</button>{/if}
                            </li>
                        {:else}
                            <li class="ol-muted">{t('No network in range.')}</li>
                        {/each}
                    </ul>
                {/if}
            {/if}
        {/if}

        {#if admin && form}
            <h3>{t('Settings')}<Help>{t('The country decides the channels the radio may use. The preferred interface carries the default route while it has a link; the other one takes over when it loses it. A smart home central is more reliable on Ethernet - Wi-Fi works, but a cable is the better choice where there is one.')}</Help></h3>
            <div class="wifi-form">
                <label>{t('Country')}
                    <select class="hmm-select" bind:value={form.country}>
                        {#each COUNTRIES.includes(form.country) ? COUNTRIES : [form.country, ...COUNTRIES] as c (c)}<option value={c}>{c}</option>{/each}
                    </select>
                </label>
                <label>{t('Preferred interface')}
                    <select class="hmm-select" bind:value={form.preferred}><option value="eth">{t('Ethernet')}</option><option value="wlan">{t('Wi-Fi')}</option></select>
                </label>
                <label>{t('Mode')}
                    <select class="hmm-select" bind:value={form.mode}><option value="dhcp">DHCP</option><option value="static">{t('static')}</option></select>
                </label>
                {#if form.mode === 'static'}
                    <label>{t('Address')} <input class="hmm-input hmm-mono" bind:value={form.address} placeholder="192.168.1.21" /></label>
                    <label>{t('Netmask')} <input class="hmm-input hmm-mono" bind:value={form.netmask} placeholder="255.255.255.0" /></label>
                    <label>{t('Gateway')} <input class="hmm-input hmm-mono" bind:value={form.gateway} placeholder="192.168.1.1" /></label>
                    <label>DNS <input class="hmm-input hmm-mono" bind:value={dnsText} placeholder="192.168.1.1" /></label>
                {/if}
                <div class="ol-actions"><button type="button" class="hmm-button" disabled={busy !== ''} onclick={saveSettings}>{t('Apply')}</button></div>
            </div>
        {/if}
    {/if}
</div>

<style>
    .wifi-qr { margin: 8px 0; padding: 10px 12px; border: 1px solid var(--line, rgba(127, 127, 127, 0.3)); border-radius: 6px; }
    .wifi-qr p { margin: 0 0 8px; }
    .ol-wifi h3 { margin: 14px 0 6px; font-size: var(--hmm-font-size); }
    .wifi-switch { display: inline-flex; align-items: center; gap: 8px; margin-top: 10px; }
    .wifi-confirm { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 8px; }
    .wifi-list { list-style: none; margin: 0 0 8px; padding: 0; }
    .wifi-list li { display: flex; align-items: center; gap: 8px; padding: 5px 0; border-top: 1px solid var(--hmm-border-muted); min-width: 0; }
    .wifi-list li .hmm-button { margin-left: auto; flex: 0 0 auto; }
    .wifi-ssid { font-weight: 600; overflow-wrap: anywhere; min-width: 0; }
    .wifi-bars { display: inline-flex; align-items: flex-end; gap: 2px; height: 12px; vertical-align: -1px; }
    .wifi-bars span { width: 3px; background: var(--hmm-border); border-radius: 1px; }
    .wifi-bars span:nth-child(1) { height: 25%; }
    .wifi-bars span:nth-child(2) { height: 50%; }
    .wifi-bars span:nth-child(3) { height: 75%; }
    .wifi-bars span:nth-child(4) { height: 100%; }
    .wifi-bars span.on { background: var(--hmm-accent); }
    .wifi-form { display: flex; flex-direction: column; gap: 8px; }
    .wifi-form label { display: grid; grid-template-columns: 140px minmax(0, 1fr); align-items: center; gap: 8px; }
    @media (max-width: 480px) { .wifi-form label { grid-template-columns: 1fr; gap: 3px; } }
</style>
