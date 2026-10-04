<script lang="ts">
    import {t} from '../lib/i18n.svelte';
    import {auth, login, finishLogin} from '../lib/auth.svelte';
    import {onMount, onDestroy, untrack} from 'svelte';
    import {ceremonyError, conditionalAvailable, passkeyLogin, supported as webauthnSupported, type WebAuthnInfo} from '../lib/webauthn';
    import {api} from '../lib/api';
    import {router, replace, link} from '../lib/router.svelte';
    import {mayBounce, onPage, parseBounce, safeReturn} from '../lib/routes';

    // task 19 (D-53): the provider's button under *Login*, and whether the password form is on at
    // all - the switch auth.oidc.password_login; off, the provider is the only way in and the
    // console the way back (the Security page says so before it lets the switch be turned off)
    let sso = $state<{enabled: boolean; name?: string; password_login?: boolean}>({enabled: false, password_login: true});
    const passwordLogin = $derived(sso.password_login !== false);
    // the lighttpd gate and occulited's CGI guard send /login?return=<addon page>; only a path of
    // this origin is honoured (lib/routes.ts says which strings look like one and are not)
    const returnTo = safeReturn(new URLSearchParams(location.search).get('return'), location.origin);
    onMount(async () => {
        if (auth.authenticated && !auth.public) return;
        try { sso = await api.get<{enabled: boolean; name?: string; password_login?: boolean}>('/api/auth/v1/oidc'); } catch { /* none */ }
        const q = new URLSearchParams(location.search);
        const e = q.get('error');
        if (e) {
            // the callback's refusal comes as a code with the name the provider sent, so the
            // sentence is the page's own; every other failure is the provider's or the flow's text
            error = e === 'no-account' ? t('There is no account {name} on this system — an administrator has to create it first.', {name: q.get('user') ?? ''}) : e;
            history.replaceState(null, '', location.pathname);
        }
    });

    // B-63: a signed-in session on /login - a bookmark, or an addon page whose gate did not see the
    // session - goes on to the page it was sent from, or to Status. Once per target: when that page
    // sends it straight back (its gate does not take this session), a second round would repeat
    // forever, in an addon's frame too, so the round is remembered for the tab and the second one
    // is a notice instead.
    const BOUNCE_KEY = 'ol.loginBounce';
    let looped = $state(false);
    $effect(() => {
        if (!auth.authenticated || auth.public || !onPage(router.path, '/login')) return;
        untrack(goOn);
    });
    function goOn() {
        if (!returnTo) {
            replace('/');
            return;
        }
        let last = null;
        try { last = parseBounce(sessionStorage.getItem(BOUNCE_KEY)); } catch { /* no storage */ }
        if (!mayBounce(last, returnTo, Date.now())) {
            looped = true;
            return;
        }
        try { sessionStorage.setItem(BOUNCE_KEY, JSON.stringify({target: returnTo, at: Date.now()})); } catch { /* no storage */ }
        // the origin in front: a path is never read as another host
        location.replace(location.origin + returnTo);
    }
    function forgetBounce() {
        try { sessionStorage.removeItem(BOUNCE_KEY); } catch { /* no storage */ }
    }

    let username = $state('');
    let password = $state('');
    let repeat = $state('');
    let error = $state('');
    let busy = $state(false);
    const setup = $derived(auth.setupRequired);

    // task 262, occulited task 14: passkeys. A passkey signs in without name and password, through
    // the button or the browser's autofill in the name field where it offers that (conditional UI);
    // the password form beside it is not touched by an account's passkeys - there is no second
    // factor. The button shows whenever at least one account has a passkey.
    let webauthn = $state<WebAuthnInfo>({passkeys: false, registered: false, name: ''});
    let keyBusy = $state(false);
    const canWebAuthn = webauthnSupported();
    let conditional: AbortController | null = null;
    let conditionalUI = $state(false);
    onMount(async () => {
        if (auth.authenticated && !auth.public) return;
        try { webauthn = await api.get<WebAuthnInfo>('/api/auth/v1/webauthn'); } catch { /* an older daemon */ }
        if (webauthn.passkeys && canWebAuthn && (await conditionalAvailable())) startConditional();
    });
    onDestroy(() => conditional?.abort());
    // the autofill offer runs in the background until a passkey is picked or the form is used
    function startConditional() {
        conditional = new AbortController();
        conditionalUI = true;
        passkeyLogin('conditional', conditional.signal).then((r) => finishLogin(r), (e) => { if (ceremonyError(e) !== 'cancelled') conditionalUI = false; });
    }
    function keyError(e: unknown): string {
        switch (ceremonyError(e)) {
            case 'cancelled': return '';
            case 'not-allowed': return t('The passkey was not used: the browser cancelled, timed out or found no passkey for this system.');
            case 'security': return t('The browser refused the passkey: the page must be opened on the system\'s name over a trusted certificate.');
            case 'unsupported': return t('This browser or device cannot use a passkey here.');
            default: return (e as Error).message;
        }
    }
    async function usePasskey() {
        conditional?.abort();
        conditionalUI = false;
        keyBusy = true;
        error = '';
        try {
            forgetBounce();
            finishLogin(await passkeyLogin('required'));
            if (returnTo && !onPage(router.path, '/login')) location.assign(location.origin + returnTo);
        } catch (e) {
            error = (e as {status?: number}).status ? (e as Error).message : keyError(e);
        } finally {
            keyBusy = false;
        }
    }

    async function submit(e: Event) {
        e.preventDefault();
        error = '';
        if (setup && password !== repeat) {
            error = t('Passwords do not match');
            return;
        }
        busy = true;
        try {
            // a login with fresh credentials is no bounce of an old session
            forgetBounce();
            await login(username.trim(), password, setup);
            // on /login the signed-in branch above takes it on; elsewhere the page stays
            if (returnTo && !onPage(router.path, '/login')) location.assign(location.origin + returnTo);
        } catch (err) {
            error = (err as Error).message;
        } finally {
            busy = false;
        }
    }
</script>

{#if auth.authenticated && !auth.public}
    {#if looped}
        <div class="ol-notice">
            {t('{path} asked for a login again although this browser is signed in: the page did not accept the session.', {path: returnTo})}
            <a href={returnTo} onclick={forgetBounce}>{t('Try again')}</a> · <a href="/" use:link>{t('Status')}</a>
        </div>
    {/if}
{:else}
<div class="ol-login">
    <form class="ol-card" onsubmit={submit}>
        <h1>{setup ? t('Welcome — set the administrator password') : t('Login')}</h1>
        {#if setup}
            <p class="ol-muted">{t('This system has no users yet. Choose the name and password of the first administrator.')}</p>
        {/if}
        {#if passwordLogin || setup}
            <!-- B-112: the text above its field with the shared gap (app.css .ol-labelled), not a line break -->
            <label class="ol-labelled"><span>{t('Username')}</span><input class="hmm-input" bind:value={username} autocomplete={conditionalUI ? 'username webauthn' : 'username'} required /></label>
            <label class="ol-labelled"><span>{t('Password')}</span><input class="hmm-input" type="password" bind:value={password} autocomplete={setup ? 'new-password' : 'current-password'} required minlength={setup ? 8 : undefined} /></label>
            {#if setup}
                <label class="ol-labelled"><span>{t('Repeat password')}</span><input class="hmm-input" type="password" bind:value={repeat} autocomplete="new-password" required /></label>
            {/if}
        {:else}
            <p class="ol-muted">{t('Password login is switched off on this system: sign in through {name}.', {name: sso.name ?? 'SSO'})}</p>
        {/if}
        <!-- the message slot is always there, sized for two lines, so a wrong password does not
             move the button or grow the card (maintainer, 2026-09-09) -->
        <div class="ol-login-msg" aria-live="polite">{#if error}<div class="ol-notice error">{error}</div>{/if}</div>
        {#if passwordLogin || setup}
            <button class="hmm-button primary ol-login-btn" type="submit" disabled={busy}>{setup ? t('Create administrator') : t('Login')}</button>
        {/if}
        {#if webauthn.passkeys && canWebAuthn && passwordLogin && !setup}
            <!-- task 262, occulited task 14: a passkey signs in without name and password; the
                 browser may offer it in the name field as well -->
            <button class="hmm-button ol-login-passkey" type="button" onclick={usePasskey} disabled={keyBusy} data-passkey>{t('Sign in with a passkey')}</button>
        {/if}
        {#if auth.public}
            <!-- task 193: the Control app is public on this system - the way there without signing in -->
            <p class="ol-muted ol-login-public" data-login-public><a href="/app" use:link>{t('To Control without signing in')}</a></p>
        {/if}
        {#if sso.enabled && !setup}
            <a class="hmm-button ol-login-sso" class:primary={!passwordLogin} class:ol-login-btn={!passwordLogin} href={'/api/auth/v1/oidc/start' + (returnTo ? '?return=' + encodeURIComponent(returnTo) : '')}>{t('Sign in with {name}', {name: sso.name ?? 'SSO'})}</a>
        {/if}
    </form>
</div>
{/if}

<style>
    .ol-login { display: flex; justify-content: center; padding: 8vh 12px; }
    form { width: 100%; max-width: 380px; display: flex; flex-direction: column; gap: 10px; padding-bottom: 28px; }
    input { width: 100%; }
    .primary { background: var(--hmm-accent); color: #fff; border-color: var(--hmm-accent); }
    /* room for a two-line "wrong credentials" notice between the password and the button, whether
       or not one is showing; the notice's own margins are inside the slot */
    .ol-login-msg { min-height: 58px; display: flex; flex-direction: column; justify-content: flex-end; }
    .ol-login-msg .ol-notice { margin: 0; }
    .ol-login-btn { min-height: 42px; font-size: 1.05em; padding: 8px 14px; margin-top: 2px; }
    /* the provider's link is a button under Login; alone (password login off) it takes Login's size */
    .ol-login-sso { display: flex; align-items: center; justify-content: center; text-decoration: none; }
    .ol-login-passkey { min-height: 36px; }
</style>
