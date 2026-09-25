<script lang="ts">
    /*
     * Rooms and functions in the App (task 193; the maintainer, 2026-09-22): a sheet from the
     * drawer's foot where an account that may configure adds, renames and deletes the nodes of
     * every enum, at every level - the store's API does the rest (docs/meta-api.md). Large rows,
     * mobile first; a delete asks and detaches the channels (they are not deleted).
     */
    import ModalDialog from '../ModalDialog.svelte';
    import Icon from '../Icon.svelte';
    import {api, type MetaSnapshot, type MetaNode} from '../api';
    import {ask, askText} from '../dialog.svelte';
    import {t} from '../i18n.svelte';

    let {snap, onclose, onchanged}: {snap: MetaSnapshot; onclose: () => void; onchanged: () => Promise<void>} = $props();
    const FAVORITE_ENUM = 'favorite';
    let busy = $state(false);
    let error = $state('');
    let adding = $state<Record<string, string>>({}); // per enum: the new top-level node's name being typed

    const enums = $derived(Object.entries(snap.enums).filter(([id]) => id !== FAVORITE_ENUM).map(([id, e]) => {
        const lang = document.documentElement.lang === 'de' ? 'de' : 'en';
        return {id, e, label: e.name[lang] ?? e.name.en ?? id};
    }));
    type Row = {path: string; node: MetaNode; depth: number; parent: string};
    function flatten(nodes: MetaNode[], parent: string, depth: number, out: Row[]) {
        for (const n of nodes) {
            const path = parent ? `${parent}/${n.id}` : n.id;
            out.push({path, node: n, depth, parent});
            if (n.children?.length) flatten(n.children, path, depth + 1, out);
        }
    }
    const rows = (e: {tree: MetaNode[]}) => {
        const out: Row[] = [];
        flatten(e.tree, '', 0, out);
        return out;
    };
    function nodeURL(enumId: string, path: string): string {
        return `/api/meta/v1/enums/${encodeURIComponent(enumId)}/nodes/${path.split('/').map(encodeURIComponent).join('/')}`;
    }
    // an id from a name: lower case, umlauts spelt out, anything else a hyphen, unique among the siblings
    function slug(name: string, siblings: MetaNode[]): string {
        let s = name.toLowerCase().replace(/ä/g, 'ae').replace(/ö/g, 'oe').replace(/ü/g, 'ue').replace(/ß/g, 'ss').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 28);
        if (!s || !/^[a-z0-9]/.test(s)) s = 'n' + (s || Date.now().toString(36));
        let id = s;
        for (let n = 2; siblings.some((x) => x.id === id); n++) id = `${s}-${n}`;
        return id;
    }
    async function run(fn: () => Promise<unknown>) {
        busy = true;
        error = '';
        try {
            await fn();
            await onchanged();
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = false;
        }
    }
    function siblingsOf(enumId: string, parent: string): MetaNode[] {
        let list = snap.enums[enumId]?.tree ?? [];
        for (const id of parent ? parent.split('/') : []) {
            const n = list.find((x) => x.id === id);
            if (!n) return [];
            list = n.children ?? [];
        }
        return list;
    }
    function add(enumId: string, parent: string, name: string) {
        const n = name.trim();
        if (!n) return;
        const id = slug(n, siblingsOf(enumId, parent));
        void run(() => api.post(`/api/meta/v1/enums/${encodeURIComponent(enumId)}/nodes`, {parent: parent ? `${enumId}/${parent}` : null, id, name: n})).then(() => {
            adding = {...adding, [enumId]: ''};
        });
    }
    async function addChild(enumId: string, row: Row) {
        const name = await askText({title: t('Add below {name}', {name: row.node.name}), message: t('A new entry inside this one - a room on a floor, say.'), input: {label: t('Name'), minLength: 1}, confirm: t('Add')});
        if (name) add(enumId, row.path, name);
    }
    async function rename(enumId: string, row: Row) {
        const name = await askText({title: t('Rename {name}', {name: row.node.name}), message: t('The name of this room or function, as the drawer and the tiles show it.'), input: {label: t('Name'), initial: row.node.name, minLength: 1}, confirm: t('Rename')});
        if (name === null || !name.trim() || name.trim() === row.node.name) return;
        void run(() => api.patch(nodeURL(enumId, row.path), {name: name.trim()}));
    }
    async function remove(enumId: string, row: Row) {
        const members = Object.values(snap.objects).filter((o) => o.enums.some((p) => p === `${enumId}/${row.path}` || p.startsWith(`${enumId}/${row.path}/`))).length;
        const ok = await ask({
            title: t('Delete {name}?', {name: row.node.name}),
            message: [t('The entry and everything below it go. Channels are not deleted: they only lose this assignment.'), members ? t('{n} channels are assigned here.', {n: String(members)}) : t('No channel is assigned here.')].join('\n\n'),
            confirm: t('Delete'),
            danger: true,
            focusCancel: true,
        });
        if (ok) void run(() => api.del(`${nodeURL(enumId, row.path)}?members=detach`));
    }
</script>

<ModalDialog open={true} title={t('Rooms and functions')} size="medium" {onclose} class="app-sheet app-taxonomy">
    <div class="tx-head">
        <span class="tx-circle" aria-hidden="true"><Icon name="edit" size={26} /></span>
        <div class="tx-titles">
            <div class="tx-name">{t('Rooms and functions')}</div>
            <div class="ol-muted">{t('Add, rename, delete - at every level.')}</div>
        </div>
    </div>
    {#if error}<div class="ol-notice error">{error}</div>{/if}
    {#each enums as en (en.id)}
        <h3 class="tx-section">{en.label}</h3>
        <div class="tx-rows" data-taxonomy={en.id}>
            {#each rows(en.e) as row (row.path)}
                <div class="tx-row" style={`--depth:${row.depth}`} data-taxonomy-node={`${en.id}/${row.path}`}>
                    <span class="tx-label">{row.node.name || row.node.id}</span>
                    <button type="button" class="ol-iconlink tx-btn" title={t('Add below')} aria-label={t('Add below {name}', {name: row.node.name})} disabled={busy} onclick={() => void addChild(en.id, row)}><Icon name="plus" size={18} /></button>
                    <button type="button" class="ol-iconlink tx-btn" title={t('Rename')} aria-label={t('Rename {name}', {name: row.node.name})} disabled={busy} onclick={() => void rename(en.id, row)}><Icon name="edit" size={18} /></button>
                    <button type="button" class="ol-iconlink tx-btn tx-danger" title={t('Delete')} aria-label={t('Delete {name}', {name: row.node.name})} disabled={busy} onclick={() => void remove(en.id, row)}><Icon name="trash" size={18} /></button>
                </div>
            {/each}
            <form class="tx-add" onsubmit={(ev) => { ev.preventDefault(); add(en.id, '', adding[en.id] ?? ''); }}>
                <input class="hmm-input tx-input" type="text" placeholder={t('New entry')} aria-label={t('New entry in {name}', {name: en.label})} value={adding[en.id] ?? ''} oninput={(ev) => (adding = {...adding, [en.id]: ev.currentTarget.value})} disabled={busy} />
                <button type="submit" class="hmm-button tx-addbtn" disabled={busy || !(adding[en.id] ?? '').trim()}>{t('Add')}</button>
            </form>
        </div>
    {/each}
</ModalDialog>

<style>
    .tx-head { display: flex; align-items: center; gap: 16px; margin: 0 0 12px; }
    .tx-circle { width: 56px; height: 56px; border-radius: 50%; border: 2px solid var(--hmm-border-muted); display: inline-flex; align-items: center; justify-content: center; color: var(--hmm-fg-muted); flex: 0 0 auto; }
    .tx-name { font-size: 1.25em; font-weight: 600; }
    .tx-section { margin: 14px 0 6px; font-size: 1em; color: var(--hmm-fg-muted); font-weight: 600; }
    .tx-row { display: flex; align-items: center; gap: 4px; min-height: 48px; padding-left: calc(var(--depth, 0) * 18px); border-radius: 8px; font-size: 1.1em; }
    .tx-row:hover { background: var(--hmm-bg-hover, rgba(127, 127, 127, 0.12)); }
    .tx-label { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding-left: 8px; }
    .tx-btn { display: inline-flex; align-items: center; justify-content: center; width: 44px; height: 44px; border-radius: 8px; color: var(--hmm-fg); background: none; border: 0; cursor: pointer; flex: 0 0 auto; }
    .tx-btn:hover { background: var(--hmm-bg-hover, rgba(127, 127, 127, 0.12)); }
    .tx-danger:hover { color: var(--hmm-error); }
    .tx-add { display: flex; gap: 8px; margin: 6px 0 4px; }
    .tx-input { flex: 1 1 auto; min-height: 44px; font-size: 1.05em; }
    .tx-addbtn { min-height: 44px; padding: 0 16px; }
</style>
