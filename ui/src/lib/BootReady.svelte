<script lang="ts">
    /*
     * After a reboot the web interface started (task 94): the page has reloaded onto the box, and
     * the countdown entry (lib/bootbar.ts) is still there. A thinner second bar says the radio
     * interfaces are starting, over the expected time, until the services list shows rfd and
     * hmipserver running (or not there, not configured, failed). Then the page reports what it saw
     * to POST /api/system/v1/boot-timing - the box's calibration - and removes the entry.
     *
     * B-104: the bar stood without a label at the top of the page, with only "Radio interfaces are
     * starting" under it, and read like a stray element. It keeps its place and has a heading - the
     * box has restarted - and one sentence naming the interfaces still starting (from the same
     * services poll) and about how many seconds are left.
     *
     * Two more cases end here: an entry of a recovery boot or a halt is removed (the box runs
     * normally again), and a page reloaded while the box was still going down waits for it again,
     * but only for as long as a shutdown could still be under way.
     */
    import {onMount} from 'svelte';
    import {api} from './api';
    import BootBar from './BootBar.svelte';
    import {BAR_KINDS, EASE_MS, bootText, isBack, markSeen, readyView, startingInterfaces, timingBody, uptimeOf, type BootEntry, type ServiceState} from './bootbar';
    import {probeHealth, readEntry, removeEntry, watchBoot, writeEntry} from './bootwatch';
    import {i18n, t} from './i18n.svelte';

    const POLL_MS = 3000;
    /** how long after the request a reload may still be waiting for the box to go down */
    const RESUME_GRACE_S = 60;

    let ready = $state<BootEntry | null>(null);
    let resume = $state<BootEntry | null>(null);
    /** the interfaces still starting, by the last services answer; null before the first */
    let waiting = $state<string[] | null>(null);

    async function finish(entry: BootEntry, readyAt?: number) {
        removeEntry();
        ready = null;
        try {
            await api.post('/api/system/v1/boot-timing', timingBody(entry, readyAt));
        } catch {
            /* a box without the calibration, or a session that may not write */
        }
    }

    onMount(() => {
        let stopped = false;
        let timer: ReturnType<typeof setTimeout> | undefined;
        let stopWatch: (() => void) | undefined;
        (async () => {
            let entry = readEntry();
            if (!entry) return;
            if (!BAR_KINDS.has(entry.kind)) {
                removeEntry();
                return;
            }
            if (entry.seen.ui === undefined) {
                const {body} = await probeHealth();
                const up = uptimeOf(body);
                if (stopped || up === null) return;
                const now = Date.now();
                if (!isBack(entry, up, -1, now)) {
                    if (now - entry.started > (entry.expect.down + RESUME_GRACE_S) * 1000) {
                        // the reboot never came
                        removeEntry();
                        return;
                    }
                    resume = entry;
                    stopWatch = watchBoot({entry, before: -1, onUpdate: (e) => (resume = e), onBack: () => setTimeout(() => location.reload(), EASE_MS)});
                    return;
                }
                entry = markSeen(entry, 'ui', now);
                writeEntry(entry);
            }
            const e = entry;
            ready = e;
            const check = async () => {
                let done = false;
                try {
                    const r = await api.get<{services?: ServiceState[]}>('/api/system/v1/services');
                    waiting = startingInterfaces(r.services ?? []);
                    done = waiting.length === 0;
                } catch {
                    /* asked again */
                }
                if (stopped) return;
                if (done || readyView(e, Date.now(), i18n.language).expired) {
                    await finish(e, done ? Date.now() : undefined);
                    return;
                }
                timer = setTimeout(check, POLL_MS);
            };
            await check();
        })();
        return () => {
            stopped = true;
            clearTimeout(timer);
            stopWatch?.();
        };
    });
</script>

{#if resume}
    <div class="ol-bootresume" role="status" aria-live="polite">
        <div class="ol-card ol-bootresume-box">
            <h2>{t('Rebooting…')}</h2>
            <BootBar entry={resume} />
        </div>
    </div>
{:else if ready}
    <section class="ol-bootready" role="status" aria-labelledby="ol-bootready-title">
        <h2 id="ol-bootready-title" class="ol-bootready-title">{bootText('readyTitle', i18n.language)}</h2>
        <BootBar entry={ready} mode="ready" {waiting} />
    </section>
{/if}

<style>
    .ol-bootready { max-width: 480px; margin: 0 auto 12px; }
    /* the heading reads as one with the bar under it: the box restarted, and what still starts */
    .ol-bootready-title { margin: 4px 0 0; text-align: center; font-size: 15px; font-weight: 600; text-transform: none; letter-spacing: 0; color: var(--hmm-fg); overflow-wrap: anywhere; }
    .ol-bootresume { position: fixed; inset: 0; z-index: 200; display: flex; align-items: center; justify-content: center; padding: 16px; background: var(--hmm-bg); color: var(--hmm-fg); }
    .ol-bootresume-box { max-width: 520px; width: 100%; text-align: center; padding: 28px 24px; }
    .ol-bootresume-box h2 { margin: 0 0 8px; font-size: 18px; text-transform: none; letter-spacing: 0; color: var(--hmm-fg); }
</style>
