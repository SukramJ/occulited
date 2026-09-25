<script lang="ts">
    /*
     * openccu-lite tasks 221-223 (the maintainer, 2026-09-24: "move the usb-devices, add gateway and
     * search again buttons below the area headings"): a section's heading and, when the section has
     * actions of its own - a button that adds a member of what it lists, one that searches again -
     * those actions in one toolbar row directly below it, left-aligned with the heading, the same
     * spacing under every heading. It replaces task 199's row with the button beside the heading.
     * The markup is `.ol-headrow` (app.css), so a page that writes it by hand looks the same.
     * Per-card buttons stay in their cards; a section without actions is its heading alone.
     */
    import type {Snippet} from 'svelte';
    import Help from './Help.svelte';

    interface Props {
        /** h2 for a section of the page, h3 for a part of one */
        level?: 2 | 3;
        id?: string;
        title: string;
        /** the explanation behind the heading's ? */
        help?: string;
        /** the section's buttons, below the heading */
        actions?: Snippet;
    }
    let {level = 2, id, title, help, actions}: Props = $props();
</script>

{#snippet heading()}
    {#if level === 2}
        <h2 {id}>{title}{#if help}<Help>{help}</Help>{/if}</h2>
    {:else}
        <h3 {id}>{title}{#if help}<Help>{help}</Help>{/if}</h3>
    {/if}
{/snippet}

{#if actions}
    <div class="ol-headrow">
        {@render heading()}
        <div class="ol-headrow-actions">{@render actions()}</div>
    </div>
{:else}
    {@render heading()}
{/if}
