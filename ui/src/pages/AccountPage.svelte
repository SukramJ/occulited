<script lang="ts">
    import {onMount} from 'svelte';
    import {api, REQUEST_HEADER} from '../lib/api';
    import {t} from '../lib/i18n.svelte';
    import {auth, logout} from '../lib/auth.svelte';
    import Loading from '../lib/Loading.svelte';
    import Help from '../lib/Help.svelte';
    import {link} from '../lib/router.svelte';
    import {ask, askText} from '../lib/dialog.svelte';
    import {confirmTicket} from '../lib/confirm';
    import {bareName, ceremonyError, onAddress, registerKey, supported as webauthnSupported, type KeyView} from '../lib/webauthn';

    // task 29: behind the user icon - the account itself: who, logout, own password, own sessions.
    interface Session { id: string; user: string; role: string; created: string; last_seen: string; remote?: string; agent?: string }
    let sessions = $state<Session[] | null>(null);
    let current = $state('');
    let error = $state('');
    let notice = $state('');
    let curPw = $state('');
    let chgPw = $state('');
    // task 19: with password login switched off the box takes no password change either
    let provider = $state<{enabled: boolean; name?: string; password_login?: boolean}>({enabled: false, password_login: true});
    const passwordLogin = $derived(provider.password_login !== false);

    async function load() {
        try {
            const s = await api.get<{sessions: Session[]; current: string}>('/api/auth/v1/sessions');
            sessions = s.sessions;
            current = s.current;
            try { provider = await api.get<typeof provider>('/api/auth/v1/oidc'); } catch { /* an older daemon */ }
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
    }
    onMount(load);
    async function run(fn: () => Promise<unknown>, ok: string): Promise<void> {
        notice = '';
        try {
            await fn();
            notice = ok;
            await load();
        } catch (e) {
            notice = (e as Error).message;
        }
    }
    const changePw = () => run(async () => { await api.post('/api/auth/v1/password', {current: curPw, password: chgPw}); curPw = ''; chgPw = ''; }, t('Password changed'));

    // task 262: the account's security keys and passkeys. Adding and removing ask for the
    // password again (the confirmed ticket of task 154, or the provider). A key is bound to the
    // system's name: the card says so when the page is on an address or a bare host name.
    let keys = $state<KeyView[] | null>(null);
    let keysName = $state('');
    let keysError = $state('');
    let keyBusy = $state(false);
    const canWebAuthn = webauthnSupported();
    const wrongPlace = $derived(onAddress() || bareName() || (keysName !== '' && location.hostname.toLowerCase() !== keysName && location.hostname !== 'localhost'));
    async function loadKeys() {
        try {
            const r = await api.get<{keys: KeyView[]; name: string}>('/api/auth/v1/me/webauthn');
            keys = r.keys;
            keysName = (r.name ?? '').toLowerCase();
            keysError = '';
        } catch (e) {
            keysError = (e as Error).message;
            keys = keys ?? [];
        }
    }
    onMount(loadKeys);
    const confirmTexts = {
        title: t('Confirm with your password'),
        message: t('Adding or removing a security key asks for your password every time.'),
        provider: t('Adding or removing a security key asks for a fresh login at the identity provider.'),
        impossible: t('This account has no password and no identity provider is configured, so it cannot confirm.'),
    };
    async function addKey() {
        const name = await askText({title: t('Add security key or passkey'), message: t('A name for the key, so you can tell them apart later (for example the make, or the device the passkey lives on).'), input: {placeholder: t('Name of the key')}, confirm: t('Continue')});
        if (!name || !name.trim()) return;
        keyBusy = true;
        keysError = '';
        try {
            const ticket = await confirmTicket('/api/auth/v1/me/webauthn', confirmTexts);
            if (!ticket) return;
            await registerKey(name.trim(), ticket);
            notice = t('Security key added');
            await loadKeys();
        } catch (e) {
            const status = (e as {status?: number}).status;
            if (status) keysError = (e as Error).message;
            else switch (ceremonyError(e)) {
                case 'cancelled': break;
                case 'not-allowed': keysError = t('No key was added: the browser cancelled or timed out, or this key is registered already.'); break;
                case 'security': keysError = t('The browser refused: a security key can only be added on the system\'s name over a trusted certificate (System → Certificate).'); break;
                case 'unsupported': keysError = t('This browser or device cannot register a security key here.'); break;
                default: keysError = (e as Error).message;
            }
        } finally {
            keyBusy = false;
        }
    }
    async function removeKey(k: KeyView) {
        if (!(await ask({title: t('Remove security key'), message: t('Remove the key {name}? Signing in will not ask for it any more.', {name: k.name}), confirm: t('Remove'), danger: true}))) return;
        keyBusy = true;
        keysError = '';
        try {
            const ticket = await confirmTicket('/api/auth/v1/me/webauthn', confirmTexts);
            if (!ticket) return;
            await api.delWith(`/api/auth/v1/me/webauthn/${encodeURIComponent(k.id)}`, {'X-Occulite-Confirm': ticket});
            notice = t('Security key removed');
            await loadKeys();
        } catch (e) {
            keysError = (e as Error).message;
        } finally {
            keyBusy = false;
        }
    }
    const signOutEverywhere = () => run(() => fetch('/api/auth/v1/sessions', {method: 'DELETE', headers: REQUEST_HEADER}), t('Other sessions ended'));
</script>

<h1>{t('Account')}</h1>
{#if !sessions}
    <Loading {error} />
{:else}
    {#if auth.authOff}
        <div class="ol-warn">{t('Login is switched off (System → Users → Authentication): this is the anonymous administrator, not a user account.')}</div>
    {/if}
    {#if notice}<div class="ol-notice">{notice}</div>{/if}
    <div class="ol-cards">
        <div class="ol-card">
            <div class="k">{t('Logged in as')}</div>
            <div class="v">{auth.user} <span class="ol-muted">({t(auth.role)})</span></div>
            {#if !auth.authOff}<div style="margin-top:8px"><button class="hmm-button" onclick={() => logout()}>{t('Logout')}</button></div>{/if}
        </div>
        {#if !auth.authOff && passwordLogin}
            <div class="ol-card">
                <div class="k">{t('Change password')}</div>
                <input class="hmm-input" type="password" placeholder={t('Current password')} bind:value={curPw} autocomplete="current-password" style="width:100%;margin-top:6px" />
                <input class="hmm-input" type="password" placeholder={t('New password (min. 8)')} bind:value={chgPw} autocomplete="new-password" style="width:100%;margin-top:6px" />
                <div style="margin-top:6px"><button class="hmm-button" onclick={changePw} disabled={chgPw.length < 8 || !curPw}>{t('Change password')}</button></div>
            </div>
        {:else if !auth.authOff}
            <div class="ol-card">
                <div class="k">{t('Change password')}</div>
                <div class="ol-muted" style="margin-top:6px">{t('Password login is switched off (System → Users → Authentication): accounts sign in through {name}.', {name: provider.name ?? 'SSO'})}</div>
            </div>
        {/if}
    </div>

    {#if !auth.authOff && passwordLogin}
        <!-- task 262: the account's keys; each user manages their own here, where the password is changed -->
        <section class="ol-card ol-keys" data-section="security-keys">
            <div class="k">{t('Security keys and passkeys')}<Help>{t('A security key (FIDO2, for example a YubiKey) or a passkey (the device\'s own, with PIN or biometrics) is a second factor: with one registered, signing in asks for the password and then the key. A passkey made with user verification also signs in alone. Register a second key and keep it somewhere safe: without a key, an administrator or the console (occulited webauthn) removes the lost one.')}</Help></div>
            {#if keysError}<div class="ol-notice error">{keysError}</div>{/if}
            {#if !keys}
                <Loading />
            {:else}
                {#if keys.length}
                    <!-- B-105's phone shape (ol-stack): the name and kind on the row, the rest as labelled lines -->
                    <table class="ol-table ol-stack ol-keys-table">
                        <thead><tr><th>{t('Name')}</th><th>{t('Kind')}</th><th>{t('Added')}</th><th>{t('Last used')}</th><th></th></tr></thead>
                        <tbody>
                            {#each keys as k (k.id)}
                                <tr data-key={k.id}>
                                    <td>{k.name}</td>
                                    <td>{k.passkey ? t('passkey (signs in alone)') : t('second factor')}</td>
                                    <td class="ol-stack-line" data-label={t('Added')}>{new Date(k.created).toLocaleDateString()}</td>
                                    <td class="ol-muted ol-stack-line" data-label={t('Last used')}>{k.last_used ? new Date(k.last_used).toLocaleString() : t('never')}</td>
                                    <td class="ol-actions ol-stack-line"><button class="hmm-button" onclick={() => removeKey(k)} disabled={keyBusy}>{t('Remove')}</button></td>
                                </tr>
                            {/each}
                        </tbody>
                    </table>
                    {#if keys.length === 1}<p class="ol-muted ol-keys-hint">{t('One key only: add a second one as the way in should this one be lost.')}</p>{/if}
                {:else}
                    <p class="ol-muted ol-keys-hint">{t('No security key yet: the password alone signs in.')}</p>
                {/if}
                {#if !canWebAuthn}
                    <p class="ol-muted ol-keys-hint" data-keys-unsupported>{t('This browser cannot register a security key here: it needs a secure context (HTTPS) and WebAuthn support.')}</p>
                {:else if wrongPlace}
                    <p class="ol-muted ol-keys-hint" data-keys-wrong-place>{t('A security key is bound to the system\'s name: to add one, open the system as {name} over a trusted certificate (System → Certificate: ACME or an own certificate, and the redirect from the bare name).', {name: keysName ? `https://${keysName}/` : t('its full name (host and domain)')})} <a href="/system/certificates" use:link>{t('Certificate')}</a></p>
                {:else}
                    <div style="margin-top:8px"><button class="hmm-button" onclick={addKey} disabled={keyBusy} data-add-key>{t('Add security key or passkey')}</button></div>
                {/if}
            {/if}
        </section>
    {/if}

    <h2>{t('My sessions')}</h2>
    <table class="ol-table">
        <thead><tr><th>{t('Session')}</th><th>{t('Last seen')}</th><th>{t('From')}</th></tr></thead>
        <tbody>
            {#each sessions as s (s.id)}
                <tr>
                    <td class="hmm-mono">{s.id}{s.id === current ? ` (${t('this one')})` : ''}</td>
                    <td>{new Date(s.last_seen).toLocaleString()}</td>
                    <td class="ol-muted">{s.remote ?? ''} {s.agent ? `· ${s.agent.slice(0, 40)}` : ''}</td>
                </tr>
            {/each}
        </tbody>
    </table>
    {#if !auth.authOff}<div style="margin-top:8px"><button class="hmm-button" onclick={signOutEverywhere}>{t('Sign out everywhere else')}</button></div>{/if}
{/if}

<style>
    .ol-keys { margin-top: 12px; }
    .ol-keys-table { margin-top: 8px; }
    .ol-keys-hint { margin: 8px 0 0; font-size: var(--hmm-font-size-small); }
</style>
