<script lang="ts">
    /*
     * openccu-lite task 228, phase 1 (the maintainer, 2026-09-24: "users should be able to
     * erase/format a usb stick filesystem should be chosable, i would suggest offering ext4 and
     * exfat"): System → Storage - every USB stick, with its partitions, what uses it (the journal's
     * copies, a backup target) and, for an administrator, Format… and Safely remove. Formatting is
     * one danger click in the page's own dialog: exFAT (the default, readable on a PC) or ext4, a
     * required label the journal and the backup targets find the stick by. A stick in use is
     * detached first, as a pull would (the journal's last copy, back to RAM); a backup being written
     * to it is waited for. Phase 2: the SMB and NFS shares below the sticks (StorageShares).
     */
    import {onMount} from 'svelte';
    import {pageLife} from '../lib/pagelife.svelte';
    import {api} from '../lib/api';
    import {auth} from '../lib/auth.svelte';
    import {ask} from '../lib/dialog.svelte';
    import {t} from '../lib/i18n.svelte';
    import {link} from '../lib/router.svelte';
    import Loading from '../lib/Loading.svelte';
    import SystemTitle from '../lib/SystemTitle.svelte';
    import SectionHead from '../lib/SectionHead.svelte';
    import Disclosure from '../lib/Disclosure.svelte';
    import Icon from '../lib/Icon.svelte';
    import {formatBytes} from '../lib/netpanels';
    import {suggestLabel, labelError, type FS} from '../lib/usbformat';
    import StorageShares from '../lib/StorageShares.svelte';

    interface Partition { name: string; fstype?: string; label?: string; label_id?: string; mount?: string; read_only?: boolean; size_bytes: number; total_bytes?: number; free_bytes?: number }
    interface Use { kind: 'journal' | 'backup' | 'store'; name?: string; id?: string }
    interface Disk { name: string; vendor?: string; model?: string; serial?: string; size_bytes: number; removable: boolean; partitions: Partition[]; loop?: boolean; uses: Use[] }
    interface View { disks: Disk[]; filesystems: FS[] }

    const life = pageLife();
    const admin = $derived(auth.role === 'admin');
    let view = $state<View | null>(null);
    let error = $state('');
    let notice = $state('');
    let busy = $state('');

    async function load() {
        try {
            view = await api.get<View>('/api/system/v1/storage/usb');
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
    }
    onMount(() => {
        void load();
        // a stick plugged in or pulled shows while the page is open
        const poll = setInterval(() => life.active && busy === '' && !fmtOpen && void load(), 5000);
        const stop = life.onReturn(() => void load());
        return () => {
            clearInterval(poll);
            stop();
        };
    });

    function title(d: Disk): string {
        const labels = d.partitions.map((p) => p.label).filter(Boolean);
        return labels.length ? labels.join(', ') : [d.vendor, d.model].filter(Boolean).join(' ') || d.name;
    }
    function useWord(u: Use): string {
        return u.kind === 'journal' ? t('the journal\'s copies') : u.kind === 'store' ? t('the database\'s copy') : t('the backup target {name}', {name: u.name ?? u.id ?? ''});
    }
    const fsWord = (fs?: string) => (fs === 'exfat' ? 'exFAT' : fs === 'vfat' ? 'FAT32' : fs === 'ntfs' ? 'NTFS' : fs ?? '');

    // the format dialog
    let formatting = $state<Disk | null>(null);
    let fs = $state<FS>('exfat');
    let label = $state('');
    const labelProblem = $derived(labelError(fs, label));
    // task 247: the card's button opens the panel in the card, and closes it again
    let fmtOpen = $state(false);
    function openFormat(d: Disk) {
        if (fmtOpen && formatting?.name === d.name) {
            fmtOpen = false;
            return;
        }
        fmtOpen = true;
        formatting = d;
        fs = view?.filesystems.includes('exfat') ? 'exfat' : 'ext4';
        label = suggestLabel(d.partitions.map((p) => p.label_id ?? p.label ?? ''), fs);
    }
    // the erase asks the shell's danger question first: everything on the stick goes
    async function confirmFormat(d: Disk) {
        if (labelProblem) return;
        const yes = await ask({
            title: t('Erase {name}?', {name: title(d)}),
            message: t('Everything on this stick is erased and it is formatted as {fs} with the label {label}.', {fs: fsWord(fs), label}),
            confirm: t('Erase and format'),
            danger: true,
            focusCancel: true,
        });
        if (yes) await doFormat();
    }
    async function doFormat() {
        const d = formatting;
        if (!d || labelProblem) return;
        busy = 'format';
        error = notice = '';
        try {
            view = await api.post<View>(`/api/system/v1/storage/usb/${encodeURIComponent(d.name)}/format`, {fs, label});
            notice = t('{name} is formatted as {fs} with the label {label}.', {name: d.model || d.name, fs: fsWord(fs), label});
            fmtOpen = false;
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    async function eject(d: Disk) {
        const uses = d.uses.map(useWord).join(', ');
        const message = [
            t('{name} is unmounted, so it can be pulled out without losing data.', {name: title(d)}),
            ...(uses ? [t('It is in use by {uses}: the journal makes a last copy and carries on in RAM, a backup target is skipped with a warning until the stick is back.', {uses})] : []),
        ].join('\n\n');
        if (!(await ask({title: t('Safely remove'), message, confirm: t('Remove')}))) return;
        busy = 'eject:' + d.name;
        error = notice = '';
        try {
            view = await api.post<View>(`/api/system/v1/storage/usb/${encodeURIComponent(d.name)}/eject`, {});
            notice = t('{name} can be pulled out now.', {name: title(d)});
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }
</script>

{#snippet formatForm(f: Disk)}
        <div class="st-fmt" data-format-dialog data-format-panel>
            <div class="ol-notice warn st-erase">
                <strong>{t('Everything on this stick is erased.')}</strong>
                <div>{[[f.vendor, f.model].filter(Boolean).join(' '), formatBytes(f.size_bytes)].filter(Boolean).join(' · ')}</div>
                {#each f.partitions as p (p.name)}
                    <div class="ol-muted">{p.name}: {fsWord(p.fstype) || t('unknown filesystem')}{p.label ? ` „${p.label}“` : ''}</div>
                {/each}
                {#if f.uses.length}
                    <div>{t('It is in use by {uses}: it is detached first, as when it is pulled out.', {uses: f.uses.map(useWord).join(', ')})}</div>
                {/if}
            </div>
            <fieldset class="st-fs" disabled={busy !== ''}>
                <legend>{t('Filesystem')}</legend>
                {#each ['exfat', 'ext4'] as const as f (f)}
                    <label class:ol-muted={!view?.filesystems.includes(f)}>
                        <input type="radio" name="st-fs" value={f} bind:group={fs} disabled={!view?.filesystems.includes(f)} />
                        <strong>{f === 'exfat' ? 'exFAT' : 'ext4'}</strong>
                        <span class="ol-muted">{f === 'exfat' ? t('readable on any PC - a backup can be copied off it') : t('for the system\'s own use: the journal, safe against a power cut')}</span>
                    </label>
                {/each}
            </fieldset>
            <label class="st-label">
                <span>{t('Label')}</span>
                <input class="hmm-input hmm-mono" bind:value={label} maxlength={fs === 'exfat' ? 15 : 16} disabled={busy !== ''} data-format-label />
            </label>
            {#if labelProblem}<div class="ol-warn" data-label-problem>{t(labelProblem, {n: fs === 'exfat' ? 15 : 16})}</div>{/if}
            {#if busy === 'format'}<div class="ol-muted">{t('Formatting - this takes up to a minute on a large stick.')}</div>{/if}
            <div class="ol-form-buttons"><button type="button" class="hmm-button primary danger" onclick={() => void confirmFormat(f)} disabled={busy !== '' || !!labelProblem} data-action="format-confirm">{busy === 'format' ? t('Formatting…') : t('Erase and format')}</button></div>
        </div>
{/snippet}

<SystemTitle />

{#if !view}
    <Loading {error} />
{:else}
    <SectionHead id="usb" title={t('USB sticks')} help={t('The USB sticks plugged into this system, mounted under /media. The journal\'s copies and the backups find a stick by its label, so a stick keeps its role in any port.')} />
    {#if error}<div class="ol-notice error" data-notice="storage-error">{error}</div>{/if}
    {#if notice}<div class="ol-notice" data-notice="storage">{notice}</div>{/if}
    {#if view.disks.length === 0}
        <p class="ol-muted" data-storage-empty>{t('No USB stick is plugged in.')}</p>
    {:else}
        <div class="st-disks">
            {#each view.disks as d (d.name)}
                <section class="ol-card st-disk" data-disk={d.name}>
                    <div class="ol-card-head">
                        <span class="ol-card-icon"><Icon name="disk" size={14} /></span>
                        <div class="ol-card-titles">
                            <h3 class="ol-card-title">{title(d)}</h3>
                            <div class="ol-card-sub">{[[d.vendor, d.model].filter(Boolean).join(' '), formatBytes(d.size_bytes), d.loop ? t('lab loop device') : ''].filter(Boolean).join(' · ')}</div>
                        </div>
                    </div>
                    {#if d.partitions.length === 0}
                        <p class="ol-muted" data-no-fs>{t('No filesystem: format it to use it.')}</p>
                    {:else}
                        <dl class="ol-fields st-parts">
                            {#each d.partitions as p (p.name)}
                                <dt class="ol-field-label hmm-mono">{p.name}</dt>
                                <dd class="ol-field-value" data-part={p.name}>
                                    <div>{[fsWord(p.fstype) || t('unknown filesystem'), p.label ? `„${p.label}“` : '', formatBytes(p.size_bytes)].filter(Boolean).join(' · ')}</div>
                                    {#if p.mount}
                                        <div class="ol-muted">{t('mounted at {path}', {path: p.mount})}{p.read_only ? ` · ${t('read-only')}` : ''}{p.total_bytes ? ` · ${t('{free} free of {total}', {free: formatBytes(p.free_bytes ?? 0), total: formatBytes(p.total_bytes)})}` : ''}</div>
                                    {:else}
                                        <div class="ol-muted">{t('not mounted')}</div>
                                    {/if}
                                </dd>
                            {/each}
                        </dl>
                    {/if}
                    {#if d.uses.length}
                        <div class="st-uses" data-uses>
                            {t('In use by')}:
                            {#each d.uses as u, i (i)}
                                {#if i > 0}{', '}{/if}<a href={u.kind === 'journal' ? '/system/log?settings=journal' : u.kind === 'store' ? '/system/log?settings=history' : '/system/backup#targets'} use:link>{useWord(u)}</a>
                            {/each}
                        </div>
                    {/if}
                    {#if admin}
                        <div class="ol-form-buttons st-actions">
                            <button type="button" class="hmm-button" aria-expanded={fmtOpen && formatting?.name === d.name} onclick={() => openFormat(d)} disabled={busy !== '' || view.filesystems.length === 0} data-action="format">{t('Format')}</button>
                            <button type="button" class="hmm-button" onclick={() => eject(d)} disabled={busy !== ''} data-action="eject">{busy === 'eject:' + d.name ? t('Removing…') : t('Safely remove')}</button>
                        </div>
                    {/if}
                    <!-- task 247 (the maintainer): the format form opens in the stick's card as an in-page panel;
                         the erase itself still asks first -->
                    {#if admin}
                        <Disclosure title={t('Format the USB stick')} bind:open={() => fmtOpen && formatting?.name === d.name, (v) => { if (!v && formatting?.name === d.name && busy === '') fmtOpen = false; }}>
                            {#if formatting?.name === d.name}{@render formatForm(formatting)}{/if}
                        </Disclosure>
                    {/if}
                </section>
            {/each}
        </div>
    {/if}
    <StorageShares active={() => life.active} />
{/if}



<style>
    .st-disks { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(360px, 100%), 1fr)); gap: 12px; margin: 8px 0 14px; }
    .st-disk { display: flex; flex-direction: column; min-width: 0; }
    .st-parts { margin: 4px 0 0; }
    .st-uses { margin-top: 8px; }
    .st-actions { margin-top: auto; padding-top: 10px; display: flex; gap: 8px; flex-wrap: wrap; }
    .st-fmt { display: flex; flex-direction: column; gap: 12px; }
    .st-erase { border-left: 3px solid var(--hmm-warn); margin: 0; }
    .st-fs { border: 0; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 6px; }
    .st-fs legend { font-weight: 600; margin-bottom: 4px; }
    .st-fs label { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
    .st-label { display: flex; flex-direction: column; gap: var(--ol-label-gap, 4px); }
    .st-label input { max-width: 16em; }
</style>
