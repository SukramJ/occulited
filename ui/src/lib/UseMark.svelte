<script lang="ts">
    /*
     * openccu-lite task 238 (the maintainer): "add green checkmark icon to lan-devices that are used
     * by self. add another possibly grey symbol if they are used by some other ccu". A check in a
     * circle, green, for a device this system uses; a padlock, grey, for one another system uses -
     * two shapes, so the colour is not the only signal, and the words in the title and for screen
     * readers.
     */
    import Icon from './Icon.svelte';
    import {t} from './i18n.svelte';

    let {who, detail = ''}: {who: 'self' | 'other'; detail?: string} = $props();
    const label = $derived(who === 'self' ? t('Used by this system') : t('Used by another system'));
    const title = $derived(detail ? `${label}: ${detail}` : label);
</script>

<span class="ol-use-mark {who}" data-use={who} role="img" aria-label={title} {title}><Icon name={who === 'self' ? 'check-circle' : 'lock'} size={15} /></span>

<style>
    .ol-use-mark { display: inline-flex; align-items: center; flex: 0 0 auto; vertical-align: middle; }
    .ol-use-mark.self { color: var(--hmm-ok); }
    .ol-use-mark.other { color: var(--hmm-fg-muted); }
</style>
