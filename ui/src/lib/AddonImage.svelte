<script lang="ts">
    /*
     * An addon's icon or logo as an <img> that walks a list of candidates (openccu-lite task 100,
     * lib/addonimages.ts): the first URL that loads is shown; one the box answers with a 404 - a
     * declared file that is not in the tree, a file that is no image - gives way to the next, and
     * when the list is exhausted the fallback renders (the letter, as before). The list changes
     * when the theme does (the dark variant moves to the front), and the walk starts over then.
     */
    import type {Snippet} from 'svelte';

    let {
        candidates,
        size,
        class: cls = '',
        fallback,
    }: {
        /** the URLs to try, in order (lib/addonimages.ts imageCandidates, plus the caller's own) */
        candidates: string[];
        /** the square's edge in px for width and height; unset leaves the size to the CSS */
        size?: number;
        class?: string;
        /** what stands in when no candidate loads */
        fallback?: Snippet;
    } = $props();

    // the candidates that failed, so a new list (a theme switch) starts over without retrying them
    let failed = $state<Set<string>>(new Set());
    const src = $derived(candidates.find((c) => !failed.has(c)) ?? '');
    function fail() {
        if (src) failed = new Set([...failed, src]);
    }
</script>

{#if src}
    <img {src} alt="" class={cls} width={size} height={size} onerror={fail} data-addon-image />
{:else if fallback}
    {@render fallback()}
{/if}
