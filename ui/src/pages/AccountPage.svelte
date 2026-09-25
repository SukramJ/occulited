<script lang="ts">
    import {onMount} from 'svelte';
    import {api} from '../lib/api';
    import {t} from '../lib/i18n.svelte';
    import {auth, logout} from '../lib/auth.svelte';
    import Loading from '../lib/Loading.svelte';

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
    const signOutEverywhere = () => run(() => fetch('/api/auth/v1/sessions', {method: 'DELETE'}), t('Other sessions ended'));
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
