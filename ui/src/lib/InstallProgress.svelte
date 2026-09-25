<script lang="ts" module>
    /** a catalogue install run as /catalog/progress answers it */
    import type {InstallOutcome} from './install';
    export interface Progress { addon_id: string; phase: string; message?: string; bytes?: number; total?: number; percent?: number; started: string; finished?: string; result?: InstallOutcome & {meaning?: string} }
</script>

<script lang="ts">
    /*
     * The catalogue installer's progress (30.2, shared since task 56): one notice with one bar
     * for the whole run, polled from /catalog/progress every 1.5 s. The server paces the bar
     * (bytes for the download, a learned duration for the install); this only draws percent.
     *
     * It is one component for the Catalogue and the Installed addons page, because an update
     * can be started on either: whichever page is open shows the same run, and `onfinished`
     * fires once when it ends so the page reloads its list. `busy` is bindable - the pages
     * disable their Install and Update buttons while a run is on - and `poll()` is exported so a
     * page that has just started a run shows it without waiting for the next tick.
     */
    import {onMount} from 'svelte';
    import {api} from './api';
    import {t} from './i18n.svelte';
    import {askStartStopped} from './install';

    interface Props {
        /** true while a run is on; the pages read it */
        busy?: boolean;
        /**
         * whether a run that had already ended when the page opened is shown. The Catalogue
         * shows the last run as it always did; the Installed addons page shows only what
         * happens while it is open, so an install of last week is not a notice on every visit.
         */
        showEnded?: boolean;
        /** called once when a run has ended (done or failed) since the page opened */
        onfinished?: (p: Progress) => void;
    }
    let {busy = $bindable(false), showEnded = true, onfinished}: Props = $props();

    let progress = $state<Progress | null>(null);
    const keyOf = (p: Progress | null) => (p ? `${p.addon_id}@${p.started}` : '');
    // the run whose end was already reported, so a finished run that stays in the answer (it
    // does, until the next one starts) fires `onfinished` once and not on every poll
    let reported = '';
    // the run that had ended before the page opened
    let endedAtMount = $state('');

    export async function poll(): Promise<void> {
        try {
            const p = (await api.get<{progress: Progress | null}>('/api/system/v1/catalog/progress')).progress ?? null;
            progress = p;
            busy = !!p && !p.finished;
            const key = p?.finished ? keyOf(p) : '';
            if (key && key !== reported) {
                reported = key;
                onfinished?.(p!);
                // B-98 (D-67): an addon the failed or reboot-asking install left stopped - ask to start it
                if (p!.result?.stopped_addons?.length && (await askStartStopped(p!.result)).length) onfinished?.(p!);
            }
        } catch {
            /* the box is between two answers; the next poll tells */
        }
    }
    onMount(() => {
        // a run that ended before this page opened is history, not news: it is not reported
        void (async () => {
            try {
                const p = (await api.get<{progress: Progress | null}>('/api/system/v1/catalog/progress')).progress ?? null;
                if (p?.finished) reported = endedAtMount = keyOf(p);
                progress = p;
                busy = !!p && !p.finished;
            } catch {
                /* the interval tries again */
            }
        })();
        const id = setInterval(poll, 1500);
        return () => clearInterval(id);
    });
    const pct = $derived(progress?.percent ?? (progress?.total ? Math.round(((progress.bytes ?? 0) / progress.total) * 100) : 0));
    const visible = $derived(!!progress && (showEnded || !progress.finished || keyOf(progress) !== endedAtMount));
</script>

{#if progress && visible}
    <div class="ol-notice ol-install-progress" class:error={progress.phase === 'failed'} data-phase={progress.phase} role="status">
        <strong>{progress.addon_id}</strong>: {t(progress.phase)}{progress.message ? ` — ${progress.message}` : ''}
        {#if busy && progress.phase === 'downloading' && progress.total} <span class="ol-muted">{Math.round((progress.bytes ?? 0) / 1048576)} / {Math.round(progress.total / 1048576)} MB</span>{/if}
        {#if progress.result?.reboot_required} <strong>{t('An addon asked for a reboot to finish its installation.')}</strong>{/if}
        {#if busy || progress.phase === 'done'}
            <div class="ol-progress" class:failed={progress.phase === 'failed'} role="progressbar" aria-valuenow={pct} aria-valuemin="0" aria-valuemax="100"><div style={`width:${pct}%`}></div></div>
            <span class="ol-muted">{pct} %</span>
        {/if}
    </div>
{/if}
