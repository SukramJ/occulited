<script lang="ts">
    /*
     * Task 102 (D-59): a run's lines - a radio module firmware flash, an ACME attempt - are in the
     * journal, one entry per line with the run's id, and no longer in the attempt the page polls.
     * This reads them through GET /log?run=<id>: once for a finished run, every two seconds while it
     * runs, and once more when it has ended. A finished run the journal holds nothing of (a journal in
     * RAM loses it at a reboot) says so, with the way to the journal's storage settings; the link
     * under the lines opens the run on the Log page.
     */
    import {api, type LogLine} from './api';
    import {pageLife} from './pagelife.svelte';
    import {t} from './i18n.svelte';
    import {link} from './router.svelte';

    let {runId, running = false, cls = ''}: {runId?: string; running?: boolean; cls?: string} = $props();
    const life = pageLife();

    let lines = $state<LogLine[] | null>(null);
    let missing = $state(false);
    let error = $state('');
    // only the newest request's answer counts, as on the Log page (B-59)
    let seq = 0;
    async function load(id: string) {
        const mine = ++seq;
        try {
            const r = await api.get<{lines: LogLine[]; run_missing?: boolean; error?: string}>(`/api/system/v1/log?run=${encodeURIComponent(id)}&limit=1000`);
            if (mine !== seq) return;
            lines = r.lines ?? [];
            missing = !!r.run_missing;
            error = r.error ?? '';
        } catch (e) {
            if (mine === seq) error = (e as Error).message;
        }
    }
    // B-155: the page passes `runId={fw.running.run_id}`, a getter over the whole attempt; every poll
    // that replaced it re-ran the effect below, which emptied the box for a frame (the flash's
    // "lightning" every 2 s) and fetched twice. The deriveds hand the effect only a real change.
    const currentId = $derived(runId);
    const currentLive = $derived(running);
    $effect(() => {
        const id = currentId;
        const live = currentLive;
        lines = null;
        missing = false;
        error = '';
        if (!id) return;
        void load(id);
        if (!live) return;
        // task 177: on a kept page that another page hides, the log waits
        const timer = setInterval(() => life.active && void load(id), 2000);
        return () => clearInterval(timer);
    });
    // the journal's wall clock in the browser's zone, as the lines used to carry it
    function clock(l: LogLine): string {
        const d = l.timestamp ? new Date(l.timestamp) : null;
        return d && !Number.isNaN(d.getTime()) ? d.toTimeString().slice(0, 8) : l.time;
    }
    const text = $derived((lines ?? []).map((l) => `${clock(l)} ${l.message}`).join('\n'));
</script>

{#if runId}
    {#if lines && lines.length === 0 && missing && !running}
        <div class="ol-notice ol-notice-link" data-notice="run-gone">
            <span class="ol-notice-text">{t('The log of this run is no longer in the journal: a journal in RAM loses it at a reboot.')}</span>
            <a class="hmm-button" href="/system/log?settings=journal" use:link>{t('Storage settings')}</a>
        </div>
    {:else}
        <pre class={`ol-log run-log ${cls}`} data-run={runId}>{text}</pre>
        <p class="run-more"><a href={`/system/log?run=${encodeURIComponent(runId)}`} use:link>{t('Show log')}</a></p>
    {/if}
    {#if error}<div class="ol-notice error">{error}</div>{/if}
{:else}
    <p class="ol-muted" data-notice="run-no-id">{t('No log was recorded for this run.')}</p>
{/if}

<style>
    .run-log { margin: 0; padding: 8px 10px; border: 1px solid var(--hmm-border-muted); border-radius: var(--hmm-radius); background: var(--hmm-bg-sunken); max-height: 360px; overflow: auto; }
    .run-more { margin: 4px 0 0; font-size: var(--hmm-font-size-small); }
</style>
