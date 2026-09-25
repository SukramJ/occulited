<script lang="ts">
    import {t, i18n, setLanguage} from '../lib/i18n.svelte';
    import {theme, setTheme, type Theme} from '../lib/theme.svelte';
    import Help from '../lib/Help.svelte';
    import {prefs, setAppPreferences, type StartPage} from '../lib/prefs.svelte';
    import {auth} from '../lib/auth.svelte';
    import PublicControl from '../lib/PublicControl.svelte';

    // task 29: the Settings page behind the gear - the two switches that used to sit in the top
    // bar. How the box authenticates was here too until 2026-09-10; it is on System → Users now,
    // with the API tokens (task 132; on a Security page in between).

</script>

<h1>{t('Settings')}</h1>

<!-- task 51: where the two choices are kept, behind the section's ? -->
<h2>{t('Appearance')}<Help>{t('Both are kept in this browser and handed to embedded addons.')}</Help></h2>
<div class="ol-cards">
    <div class="ol-card">
        <div class="k">{t('Language')}</div>
        <select class="hmm-select" value={i18n.language} onchange={(e) => setLanguage((e.currentTarget as HTMLSelectElement).value as 'de' | 'en')} style="margin-top:6px">
            <option value="de">Deutsch</option>
            <option value="en">English</option>
        </select>
    </div>
    <div class="ol-card">
        <div class="k">{t('Theme')}</div>
        <select class="hmm-select" value={theme.value} onchange={(e) => setTheme((e.currentTarget as HTMLSelectElement).value as Theme)} style="margin-top:6px">
            <option value="system">{t('Theme: system')}</option>
            <option value="light">{t('Theme: light')}</option>
            <option value="dark">{t('Theme: dark')}</option>
        </select>
    </div>
</div>

<!-- task 193: the App's choices, kept with the account (with the login off: in this browser) -->
<h2>{t('Control')}<Help>{t('Control is the everyday view - favorites, rooms, functions with their controls. Both choices are kept with your account, so a phone and a desktop agree.')}</Help></h2>
<div class="ol-cards">
    <div class="ol-card" data-setting="start-page">
        <div class="k">{t('Start page')}</div>
        <select class="hmm-select" value={prefs.startPage || 'status'} onchange={(e) => setAppPreferences({startPage: (e.currentTarget as HTMLSelectElement).value as StartPage})} style="margin-top:6px" aria-label={t('Start page')} disabled={!prefs.loaded}>
            <option value="status">{t('Status')}</option>
            <option value="app">{t('Control')}</option>
        </select>
        <div class="ol-card-detail">{t('Where the UI opens after a login and when the address is the system alone.')}</div>
    </div>
    <div class="ol-card" data-setting="app-fullscreen">
        <div class="k">{t('Control without the top bar')}</div>
        <label class="ol-check" style="margin-top:6px"><input type="checkbox" checked={prefs.appFullscreen} onchange={(e) => setAppPreferences({appFullscreen: e.currentTarget.checked})} disabled={!prefs.loaded} /> {t('Show Control as the whole window')}</label>
        <div class="ol-card-detail">{t('The tab bar and the icons stay away while Control is open; its menu keeps the way back to Status and to these settings.')}</div>
    </div>
</div>

<!-- openccu-lite task 223 (the maintainer, 2026-09-24): Control without a login, from the Remote
     access page - beside the Control choices, for an administrator, who alone may switch it -->
{#if auth.role === 'admin'}
    <PublicControl />
{/if}
