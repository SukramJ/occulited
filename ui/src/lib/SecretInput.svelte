<script lang="ts">
    /*
     * A field whose value is hidden until the eye is pressed (the maintainer, 2026-09-20: a LAN
     * gateway's access key is read off a sticker and typed by hand, so it has to be checkable).
     * The value never leaves the field; showing it is this browser's business, and every field
     * starts hidden again on the next render.
     */
    import {t} from './i18n.svelte';
    import Icon from './Icon.svelte';

    interface Props {
        value: string;
        /** what the field is called, for the button's label ("Show the access key") */
        label: string;
        id?: string;
        placeholder?: string;
        disabled?: boolean;
        /** the input's own class beside hmm-input; the keys are monospace */
        mono?: boolean;
        autocomplete?: 'off' | 'new-password' | 'current-password';
    }
    let {value = $bindable(''), label, id, placeholder, disabled = false, mono = true, autocomplete = 'off'}: Props = $props();
    let shown = $state(false);
</script>

<span class="ol-secret">
    <!-- two inputs, not one with a dynamic type: Svelte binds a value only to a static type -->
    {#if shown}
        <input {id} class="hmm-input" class:hmm-mono={mono} type="text" bind:value {placeholder} {disabled} {autocomplete} aria-label={label} spellcheck="false" />
    {:else}
        <input {id} class="hmm-input" class:hmm-mono={mono} type="password" bind:value {placeholder} {disabled} {autocomplete} aria-label={label} spellcheck="false" />
    {/if}
    <button
        type="button"
        class="hmm-button ol-secret-eye"
        {disabled}
        aria-pressed={shown}
        aria-label={shown ? t('Hide {what}', {what: label}) : t('Show {what}', {what: label})}
        title={shown ? t('Hide {what}', {what: label}) : t('Show {what}', {what: label})}
        data-secret-toggle
        onclick={() => (shown = !shown)}
    ><Icon name={shown ? 'eye-off' : 'eye'} size={14} /></button>
</span>

<style>
    .ol-secret { display: inline-flex; align-items: center; gap: 6px; min-width: 0; }
    .ol-secret .hmm-input { flex: 1 1 auto; min-width: 0; }
    .ol-secret-eye { flex: 0 0 auto; display: inline-flex; align-items: center; justify-content: center; width: 30px; height: 30px; padding: 0; }
</style>
