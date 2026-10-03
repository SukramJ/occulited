<script lang="ts">
    /*
     * openccu-lite task 231 (the maintainer: "central truststore management page ... the
     * system-wide truststore and the occulited-truststore both in the ui. both should be editable
     * ... i want a new system-truststores page and good ux, consistent in look&feel to what we have
     * on the other system pages"): System → Trust stores. Four stores, one section each
     * (lib/TrustStoreSection.svelte): System (the bundle every program uses), occulited (its own
     * downloads' minimal set), OAuth / OIDC (task 230's anchors) and ACME (the CA root of a private
     * ACME directory). Above them, what is pending: a server occulited's store could not verify,
     * with the one-click copy of the missing authority from the System store (strict, by decision:
     * nothing proceeds until an administrator adds it).
     *
     * Task 267 (the maintainer: "i dont want them just underneath, i would prefer a subnav for those
     * 4 truststores on top of page"): the stores are tabs (lib/Tabs.svelte), one shown at a time,
     * each tab with its count; the chosen store is the URL's anchor (#system, #occulited, #oidc,
     * #acme), so the Status page's warning, the OIDC and ACME settings' links and a bookmark open the
     * right one. The four tables share one column layout (TrustStoreSection).
     */
    import {onMount} from 'svelte';
    import {pageLife} from '../lib/pagelife.svelte';
    import {api} from '../lib/api';
    import {auth} from '../lib/auth.svelte';
    import {t} from '../lib/i18n.svelte';
    import Help from '../lib/Help.svelte';
    import Loading from '../lib/Loading.svelte';
    import SystemTitle from '../lib/SystemTitle.svelte';
    import TrustStoreSection from '../lib/TrustStoreSection.svelte';
    import Tabs from '../lib/Tabs.svelte';
    import {STORE_ORDER, commonName, storeFromHash, storeTitle, type PinFailure, type StoreID, type TrustFailure, type TrustView} from '../lib/trust';
    import {warnFor} from '../lib/systemmenu.svelte';

    const life = pageLife();
    const admin = $derived(auth.role === 'admin');
    let view = $state<TrustView | null>(null);
    let error = $state('');
    let busy = $state('');
    let notice = $state('');
    let selected = $state<StoreID>(storeFromHash(typeof location === 'undefined' ? '' : location.hash));
    const tabs = $derived((view?.stores ?? []).map((s) => ({id: s.id, label: storeTitle(s.id, t), count: s.certificates.length})));
    const shownStore = $derived(view?.stores.find((s) => s.id === selected));
    // openccu-lite task 232: a pin failure's Re-pin opens the store's pin panel with the key the
    // server presents now
    let repin = $state<PinFailure | null>(null);
    function startRepin(f: PinFailure) {
        choose(f.purpose);
        repin = f;
    }

    /** the chosen store into the URL's anchor, without a jump and without a history entry per tab */
    function choose(id: StoreID) {
        selected = id;
        history.replaceState(history.state, '', `${location.pathname}${location.search}#${id}`);
    }

    async function load() {
        try {
            const v = await api.get<TrustView>('/api/system/v1/trust');
            // the page's order, whatever the answer's
            v.stores = STORE_ORDER.map((id) => v.stores.find((s) => s.id === id)).filter((s) => !!s);
            view = v;
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
    }
    // the one-click fix: the System store's copy of the missing authority into the failing store
    async function fix(f: TrustFailure) {
        if (!f.candidate) return;
        busy = f.host;
        notice = '';
        try {
            await api.post(`/api/system/v1/trust/system/${encodeURIComponent(f.candidate.id)}/copy`, {to: f.store});
            notice = t('{subject} is trusted in the {store} store; {host} can be reached again.', {subject: commonName(f.candidate.subject), store: storeTitle(f.store, t), host: f.host});
            await load();
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }
    onMount(() => {
        void load();
        // a link to another store's anchor while the page is open (a pending failure's, a bookmark)
        const onHash = () => {
            if (STORE_ORDER.some((id) => location.hash === `#${id}`)) selected = storeFromHash(location.hash);
        };
        window.addEventListener('hashchange', onHash);
        const off = life.onReturn(() => void load());
        return () => {
            window.removeEventListener('hashchange', onHash);
            off?.();
        };
    });
</script>

<SystemTitle>
    <Help>{t('Which certificate authorities this system trusts, in four stores: the system-wide bundle every program uses, occulited\'s own for its downloads, and the ones trusted for the login through an identity provider and for a private ACME directory. In every store a certificate can be added (PEM or DER), removed, and copied into another store.')}</Help>
</SystemTitle>
{#if !view}
    <Loading {error} />
{:else}
    {#if error}<div class="ol-notice error" data-notice="trust-page-error">{error}</div>{/if}
    {#if notice}<div class="ol-notice" data-notice="trust-page">{notice}</div>{/if}
    {#each view.pending as f (f.store + f.host)}
        <div class="ol-panel ol-notice-panel err tp-pending" data-trust-pending={f.host} {...warnFor(['trust-ca'], f.host)}>
            <p>
                <strong>{t('{host} presents a certificate from {issuer}, which the {store} store does not hold.', {host: f.host, issuer: commonName(f.issuer), store: storeTitle(f.store, t)})}</strong>
                {t('The call fails until the authority is added.')}
            </p>
            {#if f.candidate}
                {#if admin}
                    <button type="button" class="hmm-button primary" onclick={() => fix(f)} disabled={busy !== ''} data-action="trust-fix">{t('Copy {subject} from the System store', {subject: commonName(f.candidate.subject)})}</button>
                {:else}
                    <span class="ol-muted">{t('The System store holds {subject}; an administrator copies it.', {subject: commonName(f.candidate.subject)})}</span>
                {/if}
            {:else}
                <span class="ol-muted">{t('The System store does not hold it either: add the authority to the {store} store.', {store: storeTitle(f.store, t)})}</span>
                {#if selected !== f.store}<button type="button" class="ol-textbutton" onclick={() => choose(f.store)} data-action="trust-show-store">{t('Show the {store} store', {store: storeTitle(f.store, t)})}</button>{/if}
            {/if}
            <div class="ol-muted tp-error hmm-mono">{f.error}</div>
        </div>
    {/each}
    {#each view.pin_failures ?? [] as f (f.purpose + f.host)}
        <!-- openccu-lite task 232: a connection whose certificate matched none of the purpose's pins -->
        <div class="ol-panel ol-notice-panel err tp-pending" data-trust-pin-failure={f.host} {...warnFor(['trust-pin'], f.host)}>
            <p>
                <strong>{t('{host} presents a certificate none of the {store} store\'s pins match.', {host: f.host, store: storeTitle(f.purpose, t)})}</strong>
                {t('The connection fails until the key is pinned here or the server presents a pinned key again.')}
            </p>
            {#if f.chain?.[0]}
                <dl class="ol-kv tp-presented">
                    <dt>{t('Presented')}</dt><dd>{commonName(f.chain[0].subject)}</dd>
                    <dt>SHA-256</dt><dd class="hmm-mono" data-presented-fingerprint>{f.chain[0].fingerprint}</dd>
                    <dt>{t('Public key')}</dt><dd class="hmm-mono" data-presented-spki>{f.chain[0].spki}</dd>
                </dl>
            {/if}
            {#if admin}
                <button type="button" class="hmm-button primary" onclick={() => startRepin(f)} disabled={busy !== ''} data-action="trust-repin">{t('Re-pin…')}</button>
            {:else}
                <span class="ol-muted">{t('An administrator compares the key with the server\'s and pins it again.')}</span>
            {/if}
            <div class="ol-muted tp-error hmm-mono">{f.error}</div>
        </div>
    {/each}
    <Tabs {tabs} value={selected} onchange={choose} label={t('Trust stores')} idPrefix="tp" class="tp-tabs" />
    {#if shownStore}
        <div role="tabpanel" id={`tp-panel-${shownStore.id}`} aria-labelledby={`tp-tab-${shownStore.id}`}>
            {#key shownStore.id}
                <TrustStoreSection store={shownStore} stores={view.stores} reload={load} repin={repin?.purpose === shownStore.id ? repin : null} />
            {/key}
        </div>
    {/if}
{/if}

<style>
    .tp-pending p { margin: 10px 0 8px; }
    .tp-presented { margin: 0 0 10px; gap: 2px 14px; }
    .tp-presented dd { overflow-wrap: anywhere; min-width: 0; }
    :global(.tp-tabs) { margin: 4px 0 14px; }
    .tp-error { font-size: var(--hmm-font-size-small); margin-top: 8px; overflow-wrap: anywhere; }
</style>
