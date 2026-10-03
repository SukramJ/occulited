<!--
    occulited task 12: a region of a page that a warning of the Status page leads to, with the
    warning's 4 px left edge in its severity colour while it is active (app.css .ol-warn-edge) and
    nothing at all otherwise. For a panel or a card, put warnEdge() on its own class instead.

        <WarnEdge ids={['backup-unencrypted']}><BackupEncryption /></WarnEdge>
-->
<script lang="ts">
    import type {Snippet} from 'svelte';
    import {warnEdge, warnFor} from './systemmenu.svelte';

    let {ids, key, children}: {ids: string[]; key?: string; children: Snippet} = $props();
    const edge = $derived(warnEdge(ids, key));
</script>

<div class="ol-warn-edge {edge}" {...warnFor(ids, key)}>{@render children()}</div>
