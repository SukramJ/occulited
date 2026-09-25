<script lang="ts">
    /**
     * Task 162: the ⋮⋮ handle of a row that can be moved (lib/sortable.svelte.ts): drag it with a
     * mouse or a finger, or press ↑/↓ on it. A row that cannot move shows `<span class="ol-grip
     * ol-grip-none">` in its place, so the columns still line up.
     */
    import {t} from './i18n.svelte';
    import type {Sortable} from './sortable.svelte';

    let {sortable, index, key, name}: {sortable: Sortable; index: number; key: string; name: string} = $props();
</script>

<button
    type="button"
    class="ol-grip"
    aria-label={t('Move {name}', {name})}
    title={t('Drag to change the order, or Alt+↑/↓ on the keyboard')}
    onpointerdown={(ev) => sortable.down(ev, index, key)}
    onpointermove={(ev) => sortable.move(ev)}
    onpointerup={(ev) => sortable.up(ev)}
    onpointercancel={() => sortable.cancel()}
    onlostpointercapture={() => sortable.cancel()}
    onkeydown={(ev) => sortable.handleKey(ev, index)}
>
    <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor" aria-hidden="true" focusable="false">
        <circle cx="9" cy="5" r="1.8" /><circle cx="15" cy="5" r="1.8" /><circle cx="9" cy="12" r="1.8" /><circle cx="15" cy="12" r="1.8" /><circle cx="9" cy="19" r="1.8" /><circle cx="15" cy="19" r="1.8" />
    </svg>
</button>
