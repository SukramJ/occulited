<script lang="ts">
    import {api, ApiError} from '../lib/api';
    import {ask} from '../lib/dialog.svelte';
    import {t} from '../lib/i18n.svelte';
    import SystemTitle from '../lib/SystemTitle.svelte';
    import {auth} from '../lib/auth.svelte';
    import {download, ticketFor, withTicket} from '../lib/download';
    import {confirmTicket} from '../lib/confirm';
    import {recoveryKeyError} from '../lib/keyerror';
    import BackupEncryption, {type EncryptionView} from '../lib/BackupEncryption.svelte';
    import Help from '../lib/Help.svelte';
    import RegaImport from '../lib/RegaImport.svelte';
    import FactoryReset from '../lib/FactoryReset.svelte';
    import BackupTargets from '../lib/BackupTargets.svelte';
    import BootBar from '../lib/BootBar.svelte';
    import {EASE_MS, type BootEntry} from '../lib/bootbar';
    import {beginBoot, removeEntry, watchBoot} from '../lib/bootwatch';
    import {boxUptime} from '../lib/power';

    interface Check { ok: boolean; output: string; backup_version?: string; running_version?: string; needs_key: boolean; has_rega: boolean }
    // task 91: what the upload's age header said, and whether the recovery key is still needed
    interface Encryption { encrypted: boolean; format?: string; armored?: boolean; box_fingerprint?: string; recovery_fingerprint?: string; known?: 'current' | 'previous' | 'unknown'; key_created?: string; opened_with?: 'box' | 'recovery'; needs_recovery_key: boolean; passphrase?: boolean; created_here: boolean }
    let encryption = $state<EncryptionView | null>(null);
    const encrypted = $derived(!!encryption?.enabled);
    let encInfo = $state<Encryption | null>(null);
    let recoveryKey = $state('');
    // the plain download although encryption is on (D-64): the password every time, through a
    // confirmed ticket (task 154) that the navigation carries in ?confirm=
    const BACKUP_PLAIN_PATH = '/api/system/v1/backup/unencrypted';
    async function downloadPlain() {
        if (downloading) return;
        error = '';
        const go = await ask({
            title: t('Download unencrypted'),
            message: t('This file contains every key of this system in plain: the radio keys, password hashes, tokens and certificates. Keep it only as long as you need it - for a restore on OpenCCU or a CCU3, which cannot read encrypted backups. The download is noted in the journal.'),
            confirm: t('Continue'),
            danger: true,
        });
        if (!go) return;
        downloading = true;
        try {
            const confirm = await confirmTicket(BACKUP_PLAIN_PATH, {
                title: t('Download unencrypted'),
                message: t('An unencrypted backup asks for your password every time.'),
                provider: t('An unencrypted backup asks who you are every time: you sign in at the identity provider once more and come back here.'),
                impossible: t('This account has no password and no identity provider is configured, so it cannot confirm the download.'),
            });
            if (confirm === null) return;
            const ticket = await ticketFor(backupUrl);
            location.assign(withTicket(`${backupUrl}?encrypted=false&confirm=${encodeURIComponent(confirm)}`, ticket));
        } catch (e) {
            error = (e as Error).message;
        } finally {
            downloading = false;
        }
    }
    async function decrypt() {
        if (!uploaded || !recoveryKey.trim()) return;
        busy = 'decrypt';
        error = '';
        try {
            const r = await api.post<{file: string; check: Check; encryption: Encryption}>('/api/system/v1/restore/decrypt', {file: uploaded, recovery_key: recoveryKey});
            uploaded = r.file;
            check = r.check;
            encInfo = r.encryption;
            recoveryKey = '';
        } catch (e) {
            error = recoveryKeyError(e, t);
            // B-194: a damaged upload is gone with the refusal - back to the file picker
            if (e instanceof ApiError && e.code === 'corrupt') {
                uploaded = '';
                encInfo = null;
                check = null;
            }
        } finally {
            busy = '';
        }
    }
    const when = (iso?: string) => (iso ? new Date(iso).toLocaleDateString() : '');

    let busy = $state('');
    let error = $state('');
    let file = $state<File | null>(null);
    let uploaded = $state('');
    let check = $state<Check | null>(null);
    let key = $state('');
    let force = $state(false);
    let applied = $state('');
    let namesMsg = $state('');
    async function importNames() {
        if (!uploaded) return;
        busy = 'names';
        namesMsg = '';
        try {
            const r = await api.post<{objects: number; result: {rooms: number; functions: number; unnamed: number}; changed?: boolean}>('/api/meta/v1/import/regadom', {source: 'restore:' + uploaded, mode: 'merge'});
            namesMsg = t('Imported: {o} named objects, {r} rooms, {f} functions.', {o: r.objects, r: r.result.rooms, f: r.result.functions});
        } catch (e) {
            namesMsg = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    // task 86: Restore this on a target's backup - the same check as an upload, the file fetched
    // by the system from the target
    let restoreFrom = $state('');
    async function restoreFromTarget(target: string, name: string) {
        busy = 'check';
        error = '';
        check = null;
        encInfo = null;
        recoveryKey = '';
        applied = '';
        try {
            const data = await api.post<{file: string; check: Check | null; encryption?: Encryption}>('/api/system/v1/restore/check', {target, name});
            uploaded = data.file;
            check = data.check;
            encInfo = data.encryption ?? null;
            restoreFrom = name;
            document.getElementById('restore')?.scrollIntoView({block: 'start'});
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    // task 125: the download link carries no session; the click fetches a one-time ticket and
    // follows the link with it (lib/download.ts)
    const backupUrl = '/api/system/v1/backup';
    let downloading = $state(false);
    async function downloadBackup(e: Event) {
        e.preventDefault();
        if (downloading) return;
        downloading = true;
        error = '';
        try {
            await download(backupUrl);
        } catch (err) {
            error = (err as Error).message;
        } finally {
            downloading = false;
        }
    }

    async function runCheck() {
        if (!file) return;
        busy = 'check';
        error = '';
        check = null;
        encInfo = null;
        recoveryKey = '';
        try {
            const fd = new FormData();
            fd.append('file', file, file.name);
            const res = await fetch('/api/system/v1/restore/check', {method: 'POST', body: fd});
            const data = await res.json();
            if (!res.ok) throw new Error(data.message ?? res.statusText);
            uploaded = data.file;
            check = data.check;
            encInfo = data.encryption ?? null;
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    async function apply() {
        if (!check || !uploaded) return;
        if (!(await ask(t('Restore this backup and reboot? /usr/local is replaced: pairings, keys, addons and their settings.')))) return;
        busy = 'apply';
        error = '';
        // the countdown before the request (task 94): the restore reboots the box when it is done
        const before = await boxUptime();
        const entry = await beginBoot('restore', () => api.get('/api/system/v1/boot-expect?kind=restore'));
        const wait = () => {
            restoring = entry;
            watchBoot({entry, before, onUpdate: (e) => (restoring = e), onBack: () => setTimeout(() => location.reload(), EASE_MS)});
        };
        try {
            // B-193: the answer comes before the reboot; rebooting false = staged for the next boot
            const r = await api.post<{output: string; rebooting: boolean; message?: string}>('/api/system/v1/restore/apply', {file: uploaded, key, force});
            if (r.rebooting) {
                applied = r.output;
                wait();
            } else {
                removeEntry();
                error = t('The restore is prepared but the reboot did not start: {message} Reboot the system to apply it.', {message: r.message ?? ''});
            }
        } catch (e) {
            if (e instanceof ApiError) {
                removeEntry();
                error = e.message;
            } else {
                // no answer at all: the restore's reboot took the box away with the request
                wait();
            }
        } finally {
            busy = '';
        }
    }
    let restoring = $state<BootEntry | null>(null);
</script>

<SystemTitle />
{#if auth.role !== 'admin'}
    <div class="ol-notice">{t('Backups contain the radio keys; an administrator session is required.')}</div>
{:else}
    {#if error}<div class="ol-notice err">{error}</div>{/if}
    <!-- task 91: the encryption first - what every other section hands out depends on it -->
    <BackupEncryption onchange={(v) => (encryption = v)} />

    <!-- task 51: what each section does is behind its heading's ?; the warnings stay on the page -->
    <h2>{t('Create a backup')}<Help>{t('A CCU-compatible .sbk: /usr/local — pairings, keys, interface configuration, addons, names and rooms, occulited\'s own state. It restores on openccu-lite, OpenCCU and a CCU3 alike.')} {t("An encrypted backup (.sbk.age) restores on openccu-lite with this system's key or your recovery key; for OpenCCU or a CCU3, decrypt it first or download it unencrypted.")}</Help></h2>
    {#if encrypted}
        <div class="ol-actions ol-dl">
            <a class="hmm-button primary" href={backupUrl} download onclick={downloadBackup} aria-disabled={downloading} data-action="download-backup">{t('Download encrypted backup')}</a>
            <button type="button" class="hmm-button" onclick={downloadPlain} disabled={downloading} data-action="download-plain">{t('Download unencrypted (for OpenCCU or a CCU3)…')}</button>
        </div>
    {:else}
        <a class="hmm-button" href={backupUrl} download onclick={downloadBackup} aria-disabled={downloading} data-action="download-backup">{t('Download backup')}</a>
        {#if encryption}
            <div class="ol-notice" data-notice="backup-plain">{t('This backup is not encrypted: whoever can read the file has every key of this system.')} <a href="#encryption" onclick={(e) => { e.preventDefault(); document.getElementById('encryption')?.scrollIntoView({block: 'start'}); }}>{t('Set up encryption')}</a></div>
        {/if}
    {/if}

    <!-- task 86: the targets - the USB directory, NFS and SMB shares, SSH servers -->
    <BackupTargets onrestore={(target, name) => void restoreFromTarget(target, name)} />

    <h2 id="restore">{t('Restore a backup')}<Help>{t('A backup from OpenCCU or a CCU3 is accepted: the pairings, keys, interface configuration and addons in it are exactly what this system wants. Its ReGa database — names, rooms, functions, programs, system variables — cannot be used here; names, rooms and functions come across through the import below instead.')}</Help></h2>
    <div class="ol-toolbar">
        <input class="hmm-input" type="file" accept=".sbk,.age" onchange={(e) => { file = (e.currentTarget as HTMLInputElement).files?.[0] ?? null; check = null; encInfo = null; applied = ''; restoreFrom = ''; }} />
        <button class="hmm-button" onclick={runCheck} disabled={!file || busy !== ''}>{t('Check')}</button>
    </div>
    {#if restoreFrom && uploaded}<p class="ol-muted" data-restore="from-target">{t('From a backup target: {name}', {name: restoreFrom})}</p>{/if}
    {#if encInfo?.encrypted && encInfo.needs_recovery_key}
        <div class="ol-panel ol-decrypt" data-restore="needs-recovery-key">
            {#if encInfo.passphrase}
                <div class="ol-notice err" data-notice="restore-passphrase">{t('This is an age file protected by a passphrase, which this system cannot open. Decrypt it on a PC with age and upload the .sbk.')}</div>
            {:else}
                <p data-notice="restore-recovery-key">
                    {#if encInfo.known === 'current'}
                        {t('This backup is encrypted to your current recovery key ({fp}). Enter it to decrypt the backup on this system.', {fp: encInfo.recovery_fingerprint ?? ''})}
                    {:else if encInfo.known === 'previous'}
                        {t('This backup needs the earlier recovery key {fp} from {date}.', {fp: encInfo.recovery_fingerprint ?? '', date: when(encInfo.key_created)})}
                    {:else}
                        {t('This backup is encrypted, and not to a key this system knows{fp}. Enter the recovery key from the emergency kit of the system that made it.', {fp: encInfo.recovery_fingerprint ? ` (${encInfo.recovery_fingerprint})` : ''})}
                    {/if}
                </p>
                <label>{t('Recovery key')} <input class="hmm-input hmm-mono ol-keyinput" bind:value={recoveryKey} autocomplete="off" spellcheck="false" placeholder="XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX" data-input="restore-recovery-key" /></label>
                <div class="ol-actions" style="margin-top:8px"><button class="hmm-button primary" onclick={decrypt} disabled={busy !== '' || !recoveryKey.trim()} data-action="restore-decrypt">{t('Decrypt')}</button></div>
            {/if}
        </div>
    {/if}
    {#if check}
        <pre class="ol-log">{check.output}</pre>
        <dl class="ol-kv">
            <dt>{t('Result')}</dt><dd>{check.ok ? t('valid') : t('rejected')}</dd>
            {#if encInfo?.encrypted}
                <dt>{t('Encryption')}</dt><dd data-restore="opened-with">{encInfo.opened_with === 'box' ? t("encrypted · opened with this system's key") : t('encrypted · decrypted with the recovery key')}{#if encInfo.created_here} · {t('made by this system')}{:else} · <span class="ol-warn">{t('this system did not create this file')}</span>{/if}</dd>
            {/if}
            {#if check.backup_version}<dt>{t('Versions')}</dt><dd>{t('backup {b}, running {r}', {b: check.backup_version, r: check.running_version ?? ''})}</dd>{/if}
            <dt>ReGa</dt><dd>{check.has_rega ? t('contains a ReGa database — the restore ignores it; its names, rooms and functions can be imported here:') : t('none')} {#if check.has_rega}<button class="hmm-button" onclick={importNames} disabled={busy !== ''}>{t('Import the names from this backup')}</button>{/if}{#if namesMsg}<div class="ol-muted">{namesMsg}</div>{/if}</dd>
        </dl>
        {#if check.needs_key}
            {#if encInfo?.encrypted}
                <p class="ol-muted">{t('The security key protects the radio link to BidCos devices, and a restore on another system asks for it; the recovery key encrypts the backup itself, so nobody can read it without that key.')}</p>
            {/if}
            <label>{t('Security key')} <input class="hmm-input" type="password" bind:value={key} autocomplete="off" /></label>
            <label><input type="checkbox" bind:checked={force} /> {t('Force (ignore a key mismatch)')}</label>
        {/if}
        {#if check.ok}
            <div class="ol-actions" style="margin-top:8px"><button class="hmm-button" onclick={apply} disabled={busy !== '' || (check.needs_key && !key && !force)}>{t('Restore and reboot')}</button></div>
        {/if}
    {/if}
    {#if applied || restoring}
        <div class="ol-notice ol-restorenotice" role="status"><strong>{t('Restoring — the system reboots now.')}</strong>{#if restoring}<BootBar entry={restoring} />{/if}{#if applied}<pre class="ol-log">{applied}</pre>{/if}</div>
    {/if}
{/if}
{#if auth.role === 'admin'}
    <RegaImport />
    <FactoryReset />
{/if}

<style>
    .ol-notice.err { border-left: 3px solid #d33; }
    label { display: block; margin-top: 6px; }
    /* B-115: a file name is one run without a break where the engine does not break after a hyphen
       (Firefox: the backups table 462 px on a 412 px phone); it breaks anywhere, so the column can shrink */
    .ol-dl { display: flex; flex-wrap: wrap; gap: 8px; }
    .ol-decrypt { margin-top: 10px; }
    .ol-decrypt p { max-width: 720px; }
    .ol-decrypt label { display: block; }
    .ol-keyinput { width: min(100%, 26em); }
</style>
