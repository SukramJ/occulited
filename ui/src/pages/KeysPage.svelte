<script lang="ts">
    // Task 183 (the maintainer, 2026-09-19): the radio keys on a page of their own, off the
    // Interfaces page - rfd's BidCos-RF security key (the CCU's "Zentralenschlüssel"), task 149's
    // HmIP network key (local key mode) and task 154's HmIP device keys. The anchors they had there
    // (#security-key, #local-key, #device-keys) lead here (lib/systemmenu.ts, MOVED_SECTIONS).
    import {onMount} from 'svelte';
    import {api, type Radio} from '../lib/api';
    import {auth} from '../lib/auth.svelte';
    import {ask} from '../lib/dialog.svelte';
    import {t} from '../lib/i18n.svelte';
    import Loading from '../lib/Loading.svelte';
    import Disclosure from '../lib/Disclosure.svelte';
    import Help from '../lib/Help.svelte';
    import SystemTitle from '../lib/SystemTitle.svelte';
    import LocalKey from '../lib/LocalKey.svelte';
    import DeviceKeys from '../lib/DeviceKeys.svelte';
    import {pageLife} from '../lib/pagelife.svelte';

    interface Keys { security_key_set: boolean; security_key_known: boolean }
    const life = pageLife();
    let radio = $state<(Radio & Keys) | null>(null);
    let error = $state('');
    let notice = $state('');
    let busy = $state(false);
    let secKey = $state('');
    let secKey2 = $state('');
    // the form is for something one does once, so it is not on the page until asked for
    let secOpen = $state(false);
    const admin = $derived(auth.role === 'admin');

    async function load() {
        try {
            radio = await api.get<Radio & Keys>('/api/system/v1/radio');
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
    }

    async function setSecurityKey() {
        if (secKey !== secKey2) {
            notice = t('The two keys differ.');
            return;
        }
        if (!(await ask(t('Set the system security key now? rfd sends it to every AES-capable BidCos device; every paired HmIP device has to be taught in again. This cannot be undone from here.')))) return;
        busy = true;
        try {
            await api.post('/api/system/v1/radio/security-key', {key: secKey});
            notice = t('Security key set.');
            secKey = secKey2 = '';
            secOpen = false;
            await load();
        } catch (e) {
            notice = (e as Error).message;
        } finally {
            busy = false;
        }
    }

    // task 81: the Status page's security-key warning opens #security-key; once the flags are read
    // the section scrolls into view and, for an administrator, the Set key panel opens with the
    // focus in the key field. A user's page opens nothing.
    let anchored = false;
    $effect(() => {
        if (!radio || anchored || location.hash !== '#security-key') return;
        anchored = true;
        if (admin && radio.security_key_known) secOpen = true;
        setTimeout(() => {
            document.getElementById('security-key')?.scrollIntoView({block: 'start'});
            document.getElementById('ol-sec-key')?.focus({preventScroll: true});
        }, 0);
    });

    onMount(() => {
        void load();
        const stop = life.onReturn(() => void load());
        return () => void stop();
    });
</script>

<SystemTitle />
{#if !radio}
    <Loading {error} />
{:else}
    {#if notice}<div class="ol-notice" data-notice="keys">{notice}</div>{/if}
    <!-- the maintainer, 2026-09-19: each of the three keys in a panel of its own -->
    {#if radio.security_key_known}
        <section class="ol-panel" data-panel="security-key">
        <!-- rfd's, and only rfd's: eq3configcmd sets it on the BidCos coprocessor -->
        <h2 id="security-key">{t('Security key')}<Help>{t('This is a BidCos-RF setting: rfd holds it and re-keys the AES-capable BidCos devices. BidCos-Wired and HmIP are not affected by it.')}</Help> <span class="ol-muted">· BidCos-RF</span></h2>
        <p>{radio.security_key_set ? t('A system security key is set. Backups are signed with it; a restore on another system asks for it.') + ' ' + t('It does not encrypt backups; see Backup → Encryption.') : t('No system security key is set — the default key is in use. The CCU WebUI calls this the "Zentralenschlüssel".')}</p>
        {#if !admin}
            <p class="ol-muted" data-note="security-key-admin">{t('An administrator sets the key.')}</p>
        {:else}
            <Disclosure label={radio.security_key_set ? t('Change key') : t('Set key')} title={t('Security key')} bind:open={secOpen}>
                <div class="ol-form">
                    <!-- task 51: the rules of the key behind the label's ?; `for` because a label
                         without it would name its first labelable child, which is the ? -->
                    <label for="ol-sec-key"><span>{radio.security_key_set ? t('New key') : t('Key')}<Help>{t('At least 5 characters: letters, digits and underscore. Changing the key costs: rfd re-keys every AES-capable BidCos device, and every paired HmIP device has to be taught in again. Write the key down — a backup restored elsewhere asks for it.')}</Help></span> <input id="ol-sec-key" class="hmm-input hmm-mono" type="password" bind:value={secKey} autocomplete="new-password" /></label>
                    <label>{t('Repeat')} <input class="hmm-input hmm-mono" type="password" bind:value={secKey2} autocomplete="new-password" /></label>
                    <div class="ol-actions"><button class="hmm-button primary" disabled={busy || secKey.length < 5} onclick={setSecurityKey}>{radio.security_key_set ? t('Change key') : t('Set key')}</button></div>
                </div>
            </Disclosure>
        {/if}
        </section>
    {/if}
    <!-- task 149 (D-103): the HmIP network key; task 154: the device keys need radio:keys, which of
         the accounts only an administrator has -->
    {#if admin}
        <section class="ol-panel" data-panel="local-key"><LocalKey /></section>
        <section class="ol-panel" data-panel="device-keys"><DeviceKeys /></section>
    {:else}
        <p class="ol-muted" data-note="hmip-keys-admin">{t("The HmIP network key and the HmIP devices' keys are an administrator's.")}</p>
    {/if}
{/if}
