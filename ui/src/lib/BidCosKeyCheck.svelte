<script module lang="ts">
    /**
     * openccu-lite task 296: a backup made with a non-default BidCos security key. The restore
     * and the device import ask for its passphrase (the "System-Sicherheitsschlüssel" of the
     * system that made the backup) as a check - does the one the user has match? - and never
     * block: "Skip" is always there, and going on without a match asks a danger question with the
     * warning below. The passphrase is sent for the comparison only (POST /restore/verify-key);
     * the server keeps it nowhere.
     */
    import {t} from './i18n.svelte';

    export type Side = 'none' | 'match' | 'mismatch' | 'skipped' | 'unknown';
    export interface Verdict { backup: Side; system: Side; key_index: number }

    /** Whether the backup's passphrase is confirmed (or there is none to know). */
    export const keyConfirmed = (v: Verdict | null, needed: boolean) => !needed || v?.backup === 'match' || v?.backup === 'none';

    /**
     * The warning when the backup's passphrase is not confirmed: what not knowing it means. `what`
     * is the step (the restore or the import), `v` the check's verdict (null: not checked).
     */
    export function keyWarning(what: 'restore' | 'import', v: Verdict | null): string[] {
        return [
            v?.backup === 'mismatch'
                ? t('The passphrase you entered does not match the backup\'s BidCos security key.')
                : t('The passphrase of the backup\'s BidCos security key is not confirmed.'),
            what === 'restore'
                ? t('The restore itself re-keys no device: the key comes back as it is, the BidCos devices paired with it keep it, and this system works with them without the passphrase.')
                : t('The import itself re-keys no device: the key comes along as it is, the BidCos devices paired with it keep it, and this system works with them without the passphrase.'),
            t('The passphrase is needed later to change the system security key; to re-key the devices, when they move to another system or the key has to be set again; to pair these devices with another central; and to restore onto a system that has a different key.'),
            t('Without it, the only way back is a factory reset of every such device and pairing it again.'),
            t('Find the passphrase before you go on: it was set in the security settings of the CCU that made this backup (the system security key), and it may be in your own records.'),
        ];
    }
</script>

<script lang="ts">
    import {api} from './api';

    let {file, keyIndex = 0, what, systemKey = false, verdict = $bindable(null), passphrase = $bindable('')}: {
        file: string;
        keyIndex?: number;
        what: 'restore' | 'import';
        /** this system has a key of its own (the restore replaces it) */
        systemKey?: boolean;
        verdict?: Verdict | null;
        passphrase?: string;
    } = $props();
    let busy = $state(false);
    let error = $state('');
    // a verdict is about the passphrase it was made for: typing again clears it
    let checkedFor = '';
    $effect(() => {
        if (verdict && passphrase !== checkedFor) verdict = null;
    });

    async function check(skip: boolean) {
        busy = true;
        error = '';
        if (skip) passphrase = '';
        try {
            const v = await api.post<Verdict>('/api/system/v1/restore/verify-key', {file, key: skip ? '' : passphrase});
            checkedFor = skip ? '' : passphrase;
            verdict = v;
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = false;
        }
    }
</script>

<div class="ol-keycheck" data-keycheck={what}>
    <p><strong>{t('The backup\'s BidCos security key')}</strong></p>
    <p>{what === 'restore'
        ? t('This backup was made with its own BidCos security key (key index {n}): the system security key of the system that made it. The restore brings it back as it is, and the BidCos devices paired with it keep working. Enter its passphrase to check that you have it - you will need it later. It is only compared, never stored.', {n: keyIndex})
        : t('This backup was made with its own BidCos security key (key index {n}): the system security key of the system that made it. The import brings it along as it is, and the BidCos devices paired with it keep working. Enter its passphrase to check that you have it - you will need it later. It is only compared, never stored.', {n: keyIndex})}</p>
    {#if systemKey && what === 'restore'}
        <p class="ol-muted" data-keycheck-system>{t('This system has a security key of its own; the restore replaces it with the backup\'s.')}</p>
    {/if}
    <label>{t('Passphrase of the security key')} <input class="hmm-input" type="password" bind:value={passphrase} autocomplete="off" spellcheck="false" data-input="keycheck-pass" /></label>
    <div class="ol-actions">
        <button type="button" class="hmm-button" onclick={() => check(false)} disabled={busy || !passphrase.trim()} data-action="keycheck-check">{t('Check the passphrase')}</button>
        <button type="button" class="hmm-button" onclick={() => check(true)} disabled={busy} data-action="keycheck-skip">{t('Skip - I do not know it')}</button>
    </div>
    {#if verdict}
        {#if verdict.backup === 'match'}
            <p class="ok" data-keycheck-verdict="match">{t('The passphrase matches the backup\'s security key. Keep it safe.')}</p>
        {:else if verdict.backup === 'mismatch'}
            <p class="ol-warn" data-keycheck-verdict="mismatch">{t('The passphrase does not match the backup\'s security key. You can still go on; the warning then says what that means.')}</p>
        {:else if verdict.backup === 'skipped'}
            <p class="ol-warn" data-keycheck-verdict="skipped">{t('Skipped: the passphrase is not checked. You can still go on; the warning then says what that means.')}</p>
        {:else}
            <p class="ol-muted" data-keycheck-verdict="none">{t('This backup uses the factory key: there is no passphrase to know.')}</p>
        {/if}
        {#if what === 'restore' && verdict.backup === 'match' && verdict.system === 'mismatch'}
            <p class="ol-muted" data-keycheck-system-verdict="mismatch">{t('It is not this system\'s own key, which the restore replaces.')}</p>
        {/if}
    {/if}
    {#if error}<p class="ol-warn" data-keycheck-error>{error}</p>{/if}
</div>

<style>
    .ol-keycheck { margin-top: 10px; padding: 10px 12px; border: 1px solid var(--hmm-border); border-left: 3px solid var(--hmm-warn); border-radius: var(--hmm-radius); background: var(--hmm-card-bg); }
    .ol-keycheck p { margin: 4px 0; max-width: 820px; }
    .ol-keycheck label { display: block; margin-top: 6px; }
    .ol-keycheck .ol-actions { margin-top: 8px; display: flex; gap: 8px; flex-wrap: wrap; }
    .ok { color: var(--hmm-ok); font-weight: 600; }
</style>
