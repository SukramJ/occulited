<script lang="ts">
    // task 177: one kept page with its PageLife in context (lib/pagelife.svelte.ts)
    import type {Component} from 'svelte';
    import {providePageLife, type PageLife} from './pagelife.svelte';
    let {life, page: Page}: {life: PageLife; page: Component} = $props();
    // svelte-ignore state_referenced_locally
    providePageLife(life);
</script>

<Page />
