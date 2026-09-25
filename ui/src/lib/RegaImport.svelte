<script lang="ts">
    /*
     * The names, rooms and functions out of a ReGa database (task 29): the one an update from
     * OpenCCU left on this system, or a .regadom / .sbk file. Until task 193 this sat on the
     * Metadata page; that page went with the App (the App is the one editor of names and
     * assignments now), and the import, a once-at-setup act, lives on the Backup page.
     */
    import {api} from './api';
    import {t} from './i18n.svelte';
    import Help from './Help.svelte';

    interface ImportResult { devices: number; channels: number; rooms: number; functions: number; skipped: string[]; renamed: Record<string, string>; unnamed: number; favorites?: number; favorites_for?: string[] }
    let ccuMode = $state<'merge' | 'replace'>('merge');
    let preview = $state<{result: ImportResult; objects: number} | null>(null);
    let importBusy = $state('');
    let importMsg = $state('');

    let regaFile = $state<File | null>(null);
    async function regadomImport(fromBox: boolean, dryRun: boolean) {
        importBusy = dryRun ? 'preview' : 'import';
        importMsg = '';
        try {
            let r: {result: ImportResult; objects: number; revision?: number; changed?: boolean};
            if (fromBox) {
                r = await api.post('/api/meta/v1/import/regadom', {source: 'box', mode: ccuMode, dry_run: dryRun});
            } else {
                if (!regaFile) return;
                const fd = new FormData();
                fd.append('file', regaFile, regaFile.name);
                const res = await fetch(`/api/meta/v1/import/regadom?mode=${ccuMode}&dry_run=${dryRun}`, {method: 'POST', body: fd});
                const data = await res.json();
                if (!res.ok) throw new Error(data.message ?? res.statusText);
                r = data;
            }
            preview = r;
            if (!dryRun) {
                importMsg = r.changed ? t('Imported — revision {r}', {r: String(r.revision)}) : t('Nothing new: the store already had all of it.');
            }
        } catch (e) {
            importMsg = (e as Error).message;
        } finally {
            importBusy = '';
        }
    }
</script>

<h2 id="rega-import">{t('Import names from a ReGa database')}<Help>{t('The device and channel names, rooms and functions out of a ReGa database: the one an update from OpenCCU left on this system, or a .regadom / .sbk backup file. Programs and system variables do not come across — there is nothing here that could run them.')}</Help></h2>
<div class="ol-toolbar">
    <select class="hmm-select ri-mode" bind:value={ccuMode} aria-describedby="ri-mode-note"><option value="merge">{t('merge')}</option><option value="replace">{t('replace')}</option></select>
    <span class="ol-muted" id="ri-mode-note">{ccuMode === 'merge' ? t('keep what is here, add the CCU\'s') : t('the CCU\'s names become the store')}</span>
</div>
<div class="ol-toolbar" style="margin-top:6px">
    <button class="hmm-button" onclick={() => regadomImport(true, true)} disabled={importBusy !== ''}>{t('Preview the system\'s own (left by an update from OpenCCU)')}</button>
    <button class="hmm-button" onclick={() => regadomImport(true, false)} disabled={importBusy !== ''}>{t('Import it')}</button>
    <input class="hmm-input ri-file" type="file" accept=".regadom,.sbk" onchange={(e) => (regaFile = (e.currentTarget as HTMLInputElement).files?.[0] ?? null)} />
    <button class="hmm-button" onclick={() => regadomImport(false, false)} disabled={!regaFile || importBusy !== ''}>{t('Import from this file (.regadom or .sbk)')}</button>
</div>
{#if importMsg}<div class="ol-notice" style="margin-top:8px">{importMsg}</div>{/if}
{#if preview}
    <dl class="ol-kv">
        <dt>{t('Devices / channels')}</dt><dd>{preview.result.devices} / {preview.result.channels} — {t('{n} named objects to import', {n: preview.objects})}{preview.result.unnamed ? `, ${t('{n} carry only their address and are left out', {n: preview.result.unnamed})}` : ''}</dd>
        <dt>{t('Rooms / functions')}</dt><dd>{preview.result.rooms} / {preview.result.functions}</dd>
        {#if preview.result.favorites}<dt>{t('Favorites')}</dt><dd data-import-favorites>{t('{n} pages, for {names}', {n: String(preview.result.favorites), names: (preview.result.favorites_for ?? []).join(', ')})} <span class="ol-muted">{t('An account of that name takes its page over; any other waits under the CCU\'s name until such an account exists.')}</span></dd>{/if}
        {#if Object.keys(preview.result.renamed).length}<dt>{t('Renamed ids')}</dt><dd>{Object.entries(preview.result.renamed).map(([n, id]) => `${n} → ${id}`).join(', ')}</dd>{/if}
        {#if preview.result.skipped.length}<dt>{t('Skipped')}</dt><dd>{preview.result.skipped.join(', ')}</dd>{/if}
    </dl>
{/if}

<style>
    /* a phone: the mode's long option text and the file input stay inside the column */
    /* a phone: the file input stays inside the column; the mode's explanation is a span beside the
       select, which wraps (.ol-toolbar > span) - as an option label it was 392 px in German, wider
       than a 360 px phone, and WebKit counts a native select's label as the page's overflow whatever
       the select's own width (openccu-lite B-172) */
    .ri-mode, .ri-file { max-width: 100%; min-width: 0; }
</style>
