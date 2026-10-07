<script lang="ts">
    /*
     * The service messages on the Status page (task 75; D-80: read-only, acknowledging is the
     * WebUI's). What occulited's store holds - the maintenance datapoints active on a device,
     * UNREACH, LOWBAT, CONFIG_PENDING, UPDATE_PENDING, the ERROR codes - with the device's name
     * from the metadata store and since when. A stream pushes the list to an open page the moment
     * it changes; without it the page asks every 30 s. The subscriber that feeds the store is
     * occulited's own (D-115), so there is no "not running" state to show.
     */
    import {onMount} from 'svelte';
    import {api, type ServiceMessagesView, type ServiceMessage} from './api';
    import {pageLife} from './pagelife.svelte';
    import {subscribeShell} from './shellstream/client';
    import {t} from './i18n.svelte';
    import Help from './Help.svelte';
    import Icon from './Icon.svelte';

    let view = $state<ServiceMessagesView | null>(null);
    // 501: no store on this system (development, or an image without the RPC process) - the
    // section says nothing rather than something wrong
    let unsupported = $state(false);
    const life = pageLife();

    async function load() {
        try {
            view = await api.get<ServiceMessagesView>('/api/system/v1/service-messages');
        } catch (e) {
            if ((e as {status?: number}).status === 501) unsupported = true;
        }
    }
    // the topic service-messages of the shell's stream (occulited B-53: one stream for every window
    // of the browser), while the page is shown; the poll below covers a gap
    $effect(() => {
        if (!life.active) return;
        return subscribeShell('service-messages', (data) => {
            try {
                view = JSON.parse(data);
            } catch {
                /* a torn line: the next one or the poll repairs it */
            }
        });
    });
    onMount(() => {
        void load();
        const poll = setInterval(() => life.active && load(), 30000);
        const stopReturn = life.onReturn(() => void load());
        return () => {
            clearInterval(poll);
            stopReturn();
        };
    });

    // the CCU WebUI's words for the common ones; anything else is the datapoint's name
    const WORDS: Record<string, string> = {
        UNREACH: 'Communication disturbed',
        STICKY_UNREACH: 'Communication was disturbed',
        LOWBAT: 'Low battery',
        LOW_BAT: 'Low battery',
        CONFIG_PENDING: 'Configuration pending',
        UPDATE_PENDING: 'Update pending',
        SABOTAGE: 'Sabotage',
        DUTY_CYCLE: 'Duty cycle exceeded',
        ERROR_OVERHEAT: 'Overheated',
        ERROR_UNDERVOLTAGE: 'Undervoltage',
        ERROR_POWER_FAILURE: 'Power failure',
        FAULT_REPORTING: 'Fault',
    };
    function messageText(m: ServiceMessage): string {
        const w = WORDS[m.key];
        const base = w ? t(w) : m.key;
        // an enum or a number: the value is part of the message (a fault code)
        return typeof m.value === 'boolean' ? base : `${base} (${String(m.value)})`;
    }
    function deviceName(m: ServiceMessage): string {
        return m.name || (m.type ? `${m.type} ${m.address}` : m.address);
    }
    function sinceText(m: ServiceMessage): string {
        const d = new Date(m.since);
        const when = d.toLocaleString();
        return m.seen === 'start' ? t('at least since {when}', {when}) : t('since {when}', {when});
    }
</script>

{#if !unsupported}
    <h2 id="service-messages">{t('Service messages')}{#if view}{' '}<span class="ol-muted sm-count" data-count={view.count}>· {view.count}</span>{/if}<Help>{t('The maintenance messages the devices report - communication disturbed, low battery, configuration pending, update pending, a fault - collected by the system itself from the interface processes, pushed to this page as they change. Acknowledging a message is done in the frontend addon.')}</Help></h2>
    {#if view}
        {#if view.count === 0}
            <div class="ol-muted sm-none" data-service-messages="none">{t('No service messages.')}</div>
        {:else}
            <div class="ol-card sm-card" data-service-messages={view.count}>
                <ul class="sm-list">
                    {#each view.messages as m (`${m.interface}|${m.address}|${m.channel}:${m.key}`)}
                        <li class="sm-item" data-message={`${m.address}:${m.channel}:${m.key}`}>
                            <span class="sm-icon"><Icon name="alert" size={14} /></span>
                            <div class="sm-main">
                                <div class="sm-device">{deviceName(m)}{#if m.enums?.length}<span class="ol-muted"> · {m.enums.map((e) => e.split('/').slice(-1)[0]).join(', ')}</span>{/if}</div>
                                <div class="sm-text">{messageText(m)}</div>
                                <div class="ol-muted sm-meta"><span class="hmm-mono">{m.interface} {m.address}:{m.channel}</span> · {sinceText(m)}</div>
                            </div>
                        </li>
                    {/each}
                </ul>
            </div>
        {/if}
    {/if}
{/if}

<style>
    .sm-count { font-weight: normal; }
    .sm-none { margin: 0 0 14px; }
    .sm-card { margin-bottom: 14px; }
    .sm-list { list-style: none; margin: 0; padding: 0; }
    .sm-item { display: flex; gap: 10px; align-items: flex-start; padding: 8px 0; border-top: 1px solid var(--hmm-border-muted); }
    .sm-item:first-child { border-top: 0; padding-top: 0; }
    .sm-icon { flex: 0 0 auto; color: var(--hmm-warn); line-height: 0; margin-top: 3px; }
    .sm-main { min-width: 0; }
    .sm-device { font-weight: 600; }
    .sm-meta { font-size: var(--hmm-font-size-small); }
</style>
