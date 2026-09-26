<script lang="ts">
    /*
     * openccu-lite task 231: the ACME trust store as the Certificate page shows it under the CA
     * root field - the same list as System → Trust stores' ACME section (the maintainer: a
     * certificate added in the settings appears there at once; a removal here removes it there).
     * `refresh` changes after the form is saved, so a pasted root shows up in the list.
     */
    import {api} from './api';
    import {ask} from './dialog.svelte';
    import {t} from './i18n.svelte';
    import {link} from './router.svelte';
    import {commonName, shortFingerprint, type TrustCert, type TrustView} from './trust';

    let {refresh = 0}: {refresh?: number} = $props();
    let certs = $state<TrustCert[] | null>(null);
    let busy = $state('');
    let error = $state('');

    async function load() {
        try {
            const v = await api.get<TrustView>('/api/system/v1/trust');
            certs = v.stores.find((s) => s.id === 'acme')?.certificates ?? [];
        } catch (e) {
            error = (e as Error).message;
        }
    }
    $effect(() => {
        void refresh;
        void load();
    });
    async function remove(c: TrustCert) {
        if (!(await ask({title: t('Remove the certificate'), message: t('{subject} ({fingerprint}) is no longer trusted in the {store} store.', {subject: commonName(c.subject), fingerprint: shortFingerprint(c.fingerprint), store: 'ACME'}), confirm: t('Remove')}))) return;
        busy = c.id;
        error = '';
        try {
            await api.del(`/api/system/v1/trust/acme/${encodeURIComponent(c.id)}`);
            await load();
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }
</script>

<div class="atl" data-acme-trust>
    {#if error}<div class="ol-notice error">{error}</div>{/if}
    {#if certs && certs.length > 0}
        <ul class="atl-list">
            {#each certs as c (c.id)}
                <li data-acme-trusted={c.id}>
                    <span class="atl-name" title={c.subject}>{commonName(c.subject)}</span>
                    <span class="hmm-mono ol-muted atl-fp" title={c.fingerprint}>{shortFingerprint(c.fingerprint)}</span>
                    <button type="button" class="hmm-button" onclick={() => remove(c)} disabled={busy !== ''}>{t('Remove')}</button>
                </li>
            {/each}
        </ul>
    {/if}
    <p class="ol-muted atl-note">{t('Trusted for the ACME directory besides the occulited store: {n}.', {n: certs?.length ?? 0})} <a href="/system/trust#acme" use:link>{t('Trust stores')}</a></p>
</div>

<style>
    .atl { margin: 4px 0 8px; }
    .atl-list { list-style: none; padding: 0; margin: 0 0 6px; display: flex; flex-direction: column; gap: 4px; }
    .atl-list li { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; }
    .atl-name { font-weight: 600; }
    .atl-fp { font-size: var(--hmm-font-size-small); }
    .atl-note { margin: 0; font-size: var(--hmm-font-size-small); }
</style>
