<script lang="ts">
    /*
     * B-132: what a frame shows when the addon in it sent the reader back to the shell's own page.
     * The app is not mounted at all in that case (main.ts) - one notice, two ways on, and a word to
     * the shell above, which names the addon in the browser console.
     *
     * The two buttons are the only two things that help here. *Open in new tab* leaves the frame for a
     * shell of its own, which is what a reader who just wanted to get somewhere needs; *Reload* gives
     * the addon another go from the top, since the usual cause - a session the addon did not accept -
     * is often over after one.
     */
    import {onMount} from 'svelte';
    import {t} from './i18n.svelte';
    import {FRAMED_BACK} from './framed';

    onMount(() => {
        try {
            window.parent.postMessage({type: FRAMED_BACK, href: location.href}, location.origin);
        } catch {
            /* the parent went away, or is not ours after all: the notice is enough */
        }
    });

    function openTab() {
        window.open(location.href, '_blank', 'noopener');
    }
    function reload() {
        // the whole page, not this frame: reloading the frame alone would only fetch `/` again
        try {
            window.top?.location.reload();
        } catch {
            location.reload();
        }
    }
</script>

<div class="ol-framed">
    <div class="ol-notice ol-framed-box">
        <p class="ol-framed-text">{t('This addon sent you back to openccu-lite, for example because it did not accept the login.')}</p>
        <div class="ol-framed-actions">
            <button type="button" class="hmm-button primary" onclick={openTab}>{t('Open in new tab')}</button>
            <button type="button" class="hmm-button" onclick={reload}>{t('Reload the page')}</button>
        </div>
    </div>
</div>

<style>
    .ol-framed { display: flex; align-items: flex-start; justify-content: center; padding: 24px 16px; }
    .ol-framed-box { max-width: 460px; margin: 0; }
    .ol-framed-text { margin: 0 0 10px; }
    .ol-framed-actions { display: flex; flex-wrap: wrap; gap: 8px; }
</style>
