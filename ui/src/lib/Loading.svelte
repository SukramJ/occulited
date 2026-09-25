<script lang="ts">
    import {t} from './i18n.svelte';
    let {error = ''}: {error?: string} = $props();
</script>

{#if error}
    <div class="ol-notice error">{t('Request failed')}: {error}</div>
{:else}
    <div class="ol-muted" data-loading>{t('Loading')}…</div>
{/if}
