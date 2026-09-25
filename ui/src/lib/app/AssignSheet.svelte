<script lang="ts">
    /*
     * The assignment sheet (task 193; the maintainer, 2026-09-22): a very-long press on a card
     * opens it - the channel's name, and its rooms, functions and favorite as large check rows,
     * written to the metadata store at once. Same look as the channel sheet, grown out of the
     * card; everything sized for a thumb.
     */
    import {onMount} from 'svelte';
    import ModalDialog from '../ModalDialog.svelte';
    import Icon from '../Icon.svelte';
    import {api, type MetaSnapshot, type MetaNode} from '../api';
    import {t} from '../i18n.svelte';

    let {ref, snap, favoritesPath, from = null, onclose, onchanged}: {ref: string; snap: MetaSnapshot; favoritesPath: string; from?: DOMRect | null; onclose: () => void; onchanged: () => void} = $props();
    const FAVORITE_ENUM = 'favorite';
    const object = $derived(snap.objects[ref]);
    let name = $state('');
    let enums = $state<string[]>([]);
    let busy = $state(false);
    let error = $state('');
    let saved = $state(false);
    // the field follows the store's name only when that changes (a reload after a toggle must not
    // clobber what is being typed); the memberships always follow
    let seenName: string | null = null;
    $effect(() => {
        const n = object?.name ?? '';
        if (n !== seenName) {
            seenName = n;
            name = n;
        }
        enums = [...(object?.enums ?? [])];
    });
    const url = $derived(`/api/meta/v1/objects/${encodeURIComponent(ref)}`);

    // every enum but the favorites, flattened with its depth
    type Row = {path: string; label: string; depth: number};
    function flatten(enumId: string, nodes: MetaNode[], parent: string, depth: number, out: Row[]) {
        for (const n of nodes) {
            const path = parent ? `${parent}/${n.id}` : n.id;
            out.push({path: `${enumId}/${path}`, label: n.name || n.id, depth});
            if (n.children?.length) flatten(enumId, n.children, path, depth + 1, out);
        }
    }
    const sections = $derived(Object.entries(snap.enums).filter(([id, e]) => id !== FAVORITE_ENUM && e.tree.length > 0).map(([id, e]) => {
        const rows: Row[] = [];
        flatten(id, e.tree, '', 0, rows);
        const lang = document.documentElement.lang === 'de' ? 'de' : 'en';
        return {id, label: e.name[lang] ?? e.name.en ?? id, rows};
    }));
    const inFavorites = $derived(!!favoritesPath && enums.includes(favoritesPath));

    async function write(body: {name?: string; enums?: string[]}) {
        busy = true;
        error = '';
        try {
            await api.patch(url, body);
            saved = true;
            setTimeout(() => (saved = false), 1200);
            onchanged();
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = false;
        }
    }
    function toggle(path: string) {
        const next = enums.includes(path) ? enums.filter((p) => p !== path) : [...enums, path];
        enums = next;
        void write({enums: next});
    }
    function rename() {
        const n = name.trim();
        if (!n || n === object?.name) return;
        void write({name: n});
    }

    // the same growing out of the card as the channel sheet
    function frames(el: HTMLElement): [Keyframe, Keyframe] | null {
        if (!from || typeof el.animate !== 'function' || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return null;
        const to = el.getBoundingClientRect();
        if (to.width === 0 || to.height === 0) return null;
        el.style.transformOrigin = 'top left';
        return [{transform: `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(${from.width / to.width}, ${from.height / to.height})`, opacity: 0.4}, {transform: 'none', opacity: 1}];
    }
    const sheetEl = () => document.querySelector<HTMLElement>('.ol-modal.app-assign');
    onMount(() => {
        const el = sheetEl();
        const f = el && frames(el);
        if (!el || !f) return;
        el.animate(f, {duration: 240, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)'});
        el.parentElement?.animate([{backgroundColor: 'transparent', backdropFilter: 'blur(0px)'}, {}], {duration: 240, easing: 'ease-out'});
    });
    let closing = false;
    function close() {
        if (closing) return;
        closing = true;
        const el = sheetEl();
        const f = el && frames(el);
        if (!el || !f) {
            onclose();
            return;
        }
        el.style.pointerEvents = 'none';
        const a = el.animate([f[1], f[0]], {duration: 200, easing: 'cubic-bezier(0.4, 0, 1, 1)', fill: 'forwards'});
        el.parentElement?.animate([{}, {backgroundColor: 'transparent', backdropFilter: 'blur(0px)'}], {duration: 200, easing: 'ease-in', fill: 'forwards'});
        a.finished.then(onclose, onclose);
    }
</script>

<ModalDialog open={true} title={t('Assign {name}', {name: object?.name ?? ref})} size="medium" onclose={close} class="app-sheet app-assign">
    <div class="as-head">
        <span class="as-circle" aria-hidden="true"><Icon name="edit" size={26} /></span>
        <div class="as-titles">
            <div class="as-name">{object?.name ?? ref}</div>
            <div class="ol-muted hmm-mono as-ref">{ref}</div>
        </div>
    </div>
    {#if error}<div class="ol-notice error">{error}</div>{/if}
    <label class="as-field"><span>{t('Name')}</span>
        <input class="hmm-input as-input" type="text" bind:value={name} aria-label={t('Name')} disabled={busy} onblur={rename} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); rename(); } }} />
        <button type="button" class="hmm-button as-btn" disabled={busy || !name.trim() || name.trim() === object?.name} onclick={rename}>{t('Rename')}</button>
    </label>
    {#if favoritesPath}
        <label class="as-row as-fav" data-assign="favorite">
            <input type="checkbox" checked={inFavorites} disabled={busy} onchange={() => toggle(favoritesPath)} />
            <Icon name="pin" size={18} /> <span>{t('In my favorites')}</span>
        </label>
    {/if}
    {#each sections as s (s.id)}
        <h3 class="as-section">{s.label}</h3>
        <div class="as-rows" data-assign={s.id}>
            {#each s.rows as r (r.path)}
                <label class="as-row" style={`--depth:${r.depth}`} data-assign-node={r.path}>
                    <input type="checkbox" checked={enums.includes(r.path)} disabled={busy} onchange={() => toggle(r.path)} />
                    <span>{r.label}</span>
                </label>
            {/each}
        </div>
    {/each}
    {#if saved}<div class="ol-muted as-saved" data-assign-saved>{t('Saved')}</div>{/if}
</ModalDialog>

<style>
    .as-head { display: flex; align-items: center; gap: 16px; margin: 0 0 16px; }
    .as-circle { width: 56px; height: 56px; border-radius: 50%; border: 2px solid var(--hmm-border-muted); display: inline-flex; align-items: center; justify-content: center; color: var(--hmm-fg-muted); flex: 0 0 auto; }
    .as-titles { min-width: 0; }
    .as-name { font-size: 1.25em; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .as-ref { font-size: var(--hmm-font-size-small); }
    .as-field { display: grid; grid-template-columns: 5em 1fr auto; align-items: center; gap: 10px; margin: 0 0 14px; font-size: 1.05em; }
    .as-input { min-height: 44px; font-size: 1.05em; }
    .as-btn { min-height: 44px; padding: 0 16px; }
    .as-section { margin: 14px 0 6px; font-size: 1em; color: var(--hmm-fg-muted); font-weight: 600; }
    .as-rows { display: flex; flex-direction: column; }
    .as-row { display: flex; align-items: center; gap: 12px; min-height: 48px; padding: 0 8px 0 calc(8px + var(--depth, 0) * 18px); border-radius: 8px; font-size: 1.1em; cursor: pointer; }
    .as-row:hover { background: var(--hmm-bg-hover, rgba(127, 127, 127, 0.12)); }
    .as-row input { width: 24px; height: 24px; flex: 0 0 auto; }
    .as-fav { margin-bottom: 4px; }
    .as-saved { margin-top: 10px; text-align: right; }
</style>
