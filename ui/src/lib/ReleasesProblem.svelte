<script lang="ts">
    /*
     * B-21: why a release list could not be read, in the reader's language - the refused install's
     * progress and the Addons page's notice on the last check share it. Renders nothing for a code
     * it does not know; the caller shows the box's message then (`known` tells it).
     */
    import {releasesProblemKind} from './catalog';
    import {t} from './i18n.svelte';

    interface Props {
        code?: string;
        minutes?: number;
    }
    let {code, minutes}: Props = $props();
    const kind = $derived(releasesProblemKind(code, minutes));
</script>

{#if kind === 'rate-limit-wait'}{t('GitHub rate limit, try again in {n} min.', {n: minutes ?? 0})}{:else if kind === 'rate-limit'}{t('GitHub rate limit, try again later.')}{:else if kind === 'unreachable'}{t('The list of releases could not be read from GitHub.')}{/if}
