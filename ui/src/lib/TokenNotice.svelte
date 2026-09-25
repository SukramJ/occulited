<script lang="ts">
    /*
     * B-174: on the admin pages, a session opened with an API token says why nothing can be
     * changed, instead of looking silently disabled. Tokens do not administer in the browser
     * (maintainer, 2026-09-23); signing in with a password ends the token's browser session - the
     * token itself stays valid (logout drops sessions only).
     */
    import {isTokenSession, logout, refresh} from './auth.svelte';
    import {t} from './i18n.svelte';

    let busy = $state(false);

    async function signIn() {
        busy = true;
        try {
            await logout();
        } catch {
            // a token without the self scope gets no logout: forgetting it is enough
            await refresh();
        } finally {
            busy = false;
        }
    }
</script>

{#if isTokenSession()}
    <div class="ol-notice" role="note" data-testid="token-readonly">
        {t("This session was opened with an API token: it shows the system's settings but cannot change them. Sign in with a password to change them.")}
        <button type="button" class="hmm-button" onclick={signIn} disabled={busy}>{t('Sign in with a password')}</button>
    </div>
{/if}
