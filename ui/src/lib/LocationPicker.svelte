<script lang="ts">
    /*
     * openccu-lite task 228, phases 3 and 4 (the maintainer, 2026-09-24: "these mounts of usb sticks
     * and shares should be made available with auto completion or something (make nice ux) where we
     * configure bbolt storage paths or journal"): the one location picker. First where - the
     * system's own storage, a USB stick by its label, a network share by its name, each with its
     * state, free space and what already uses it; a location this use may not take is shown with
     * the reason and cannot be picked. Then the folder there, completed from the folders that
     * exist (directory names only); a new one is made when it is first used. The parent keeps the
     * location id and the folder - never a mount path.
     */
    import {onMount, tick} from 'svelte';
    import {api, ApiError} from './api';
    import {t} from './i18n.svelte';
    import {link} from './router.svelte';
    import {formatBytes} from './netpanels';
    import {folderError, stateTone, type Location, type LocationKind, type LocationUse} from './locations';

    let {
        use,
        location = $bindable(''),
        folder = $bindable(''),
        kinds = ['userfs', 'usb', 'share'] as LocationKind[],
        disabled = false,
        label = '',
        onchange,
    }: {use: LocationUse; location?: string; folder?: string; kinds?: LocationKind[]; disabled?: boolean; label?: string; onchange?: () => void} = $props();

    const uid = `lp-${Math.random().toString(36).slice(2, 8)}`;
    let list = $state<Location[] | null>(null);
    let error = $state('');
    let open = $state(false);
    let active = $state(-1);
    let dirs = $state<string[]>([]);
    let exists = $state<boolean | null>(null);
    // B-214: why the folders could not be listed (a share that did not mount, a folder the system
    // may not read) - said at the folder field instead of the new-folder hint
    let dirsError = $state('');
    let button = $state<HTMLButtonElement>();
    let box = $state<HTMLDivElement>();

    export async function reload() {
        try {
            const r = await api.get<{locations: Location[]}>(`/api/system/v1/storage/locations?use=${use}`);
            list = r.locations.filter((l) => kinds.includes(l.kind));
            error = '';
        } catch (e) {
            error = (e as Error).message;
            list = [];
        }
    }
    onMount(() => {
        void reload();
        const away = (e: PointerEvent) => {
            if (open && box && !box.contains(e.target as Node)) open = false;
        };
        document.addEventListener('pointerdown', away);
        return () => document.removeEventListener('pointerdown', away);
    });

    const KIND: Record<LocationKind, () => string> = {userfs: () => t('System storage'), usb: () => t('USB stick'), share: () => t('Network share')};
    const STATE: Record<string, () => string> = {
        present: () => t('ready'),
        mounted: () => t('mounted'),
        idle: () => t('mounted when used'),
        missing: () => t('not plugged in'),
        unreachable: () => t('unreachable'),
        'auth-failed': () => t('login refused'),
        stale: () => t('not answering'),
        'read-only': () => t('not writable'),
        error: () => t('error'),
        unsupported: () => t('not available here'),
    };
    const stateText = (s: string) => (STATE[s] ?? (() => s))();
    // the reasons a use may not take a location, in the page's words
    const REASON: Record<string, () => string> = {
        'read-only': () => t('It is mounted read-only.'),
        'no-label': () => t('It has no label: format it on System → Storage, or give it one on another computer.'),
        'no-share-for-store': () => t('Never on a network share: the database maps its file into memory and needs file locking and fsync that NFS and SMB do not guarantee - a share that drops would corrupt it or hang the system.'),
        unsupported: () => t('Shares cannot be mounted here.'),
    };
    const reasonText = (l: Location) => {
        const f = l.code ? REASON[l.code] : undefined;
        return f ? f() : (l.reason ?? '');
    };
    const noteText = (l: Location) => {
        const f = l.code ? NOTE[l.code] : undefined;
        return f ? f() : '';
    };
    const NOTE: Record<string, () => string> = {
        'ram-sync-only': () => t('Takes the copies only: the journal stays in RAM and is copied there.'),
        'lost-with-system': () => t('A backup on the system\'s own storage is lost with the system.'),
        'copy-only': () => t('Takes a copy only: the database stays on the system storage and a copy goes to the stick at every write.'),
    };
    const useWord = (u: Location['uses'][number]) => (u.kind === 'journal' ? t('the journal\'s copies') : u.kind === 'store' ? t('the database') : t('the backup target {name}', {name: u.name ?? u.id ?? ''}));
    const title = (l: Location) => (l.kind === 'userfs' ? t('System storage (userfs)') : l.name);

    // the location chosen, or - when it is not in the list (a stick that is not plugged in, a share
    // removed) - a stand-in that says so
    const current = $derived.by((): Location | null => {
        if (!location || !list) return null;
        const found = list.find((l) => l.id === location);
        if (found) return found;
        const [kind, name] = location.split(':');
        return {id: location, kind: kind as LocationKind, name: name ?? location, state: kind === 'share' ? 'error' : 'missing', uses: [], allowed: true, state_detail: kind === 'share' ? t('There is no such share on System → Storage.') : undefined};
    });
    const rule = $derived(current ?? list?.find((l) => l.id === 'userfs') ?? null);
    const fixed = $derived(!!current && current.kind === 'userfs' && !!current.fixed);
    const allowEmpty = $derived(current?.kind === 'share');
    const problem = $derived(current ? folderError(folder, allowEmpty, current.kind === 'userfs' ? current.userfs_prefix : undefined, current.kind === 'userfs' ? current.fixed : undefined) : '');

    function pick(l: Location) {
        if (!l.allowed || disabled) return;
        const was = current?.kind;
        location = l.id;
        if (l.kind === 'userfs' && l.fixed) folder = l.userfs_prefix ?? '';
        // a folder already typed stays, unless the kind changed (the journal's var/log/journal is
        // no folder for a stick)
        else if (!folder || (was !== undefined && was !== l.kind)) folder = l.default ?? '';
        open = false;
        exists = null;
        onchange?.();
        void lookup();
        button?.focus();
    }

    async function toggle() {
        if (disabled) return;
        open = !open;
        if (open) {
            if (!list) await reload();
            active = Math.max(0, list?.findIndex((l) => l.id === location) ?? 0);
            await tick();
            (box?.querySelector(`[data-index="${active}"]`) as HTMLElement | null)?.focus();
        }
    }
    function keys(e: KeyboardEvent) {
        if (!open || !list?.length) return;
        if (e.key === 'Escape') {
            // the list closes, not the sheet around it
            open = false;
            button?.focus();
            e.preventDefault();
            e.stopPropagation();
            return;
        }
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            active = (active + (e.key === 'ArrowDown' ? 1 : list.length - 1)) % list.length;
            (box?.querySelector(`[data-index="${active}"]`) as HTMLElement | null)?.focus();
            e.preventDefault();
        }
    }

    // the folder's completion: the folders under what is typed so far, looked up as one types
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function lookup() {
        if (!current || current.state === 'missing' || fixed) {
            dirs = [];
            exists = null;
            return;
        }
        const at = `${location}|${folder}`;
        try {
            const r = await api.get<{dirs: string[]; exists: boolean}>(`/api/system/v1/storage/dirs?use=${use}&location=${encodeURIComponent(location)}&prefix=${encodeURIComponent(folder)}`);
            if (`${location}|${folder}` !== at) return;
            dirs = r.dirs;
            exists = folder === '' || r.exists || r.dirs.includes(folder.replace(/\/+$/, ''));
            dirsError = '';
        } catch (e) {
            if (`${location}|${folder}` !== at) return;
            dirs = [];
            exists = null;
            dirsError = e instanceof ApiError ? dirsErrorText(e, current?.kind) : '';
        }
    }
    function dirsErrorText(e: ApiError, kind?: LocationKind): string {
        switch (e.code) {
            case 'read-only':
                return kind === 'share'
                    ? t('The system may not read this folder on the share (permission denied).') + ' ' + t('An export that maps root to nobody (root_squash) needs a directory that user may write, or "map all users" to one account.')
                    : t('The system may not read this folder (permission denied).');
            case 'auth-failed':
                return t('The share refused the sign-in: check the user and the password on System → Storage.');
            case 'unreachable':
                return t('The share cannot be reached: it did not mount.');
            case 'stale':
                return t('The share does not answer.');
        }
        return e.message;
    }
    function typed() {
        clearTimeout(timer);
        timer = setTimeout(() => void lookup(), 250);
        onchange?.();
    }
    $effect(() => {
        // the first look once the list is there
        if (list && location) void lookup();
    });
</script>

<div class="lp" bind:this={box} onkeydown={keys} role="presentation" data-location-picker={use}>
    {#if label}<span class="lp-label" id="{uid}-label">{label}</span>{/if}
    <div class="lp-anchor">
    <button
        type="button"
        class="hmm-button lp-button"
        bind:this={button}
        onclick={() => void toggle()}
        {disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-labelledby={label ? `${uid}-label ${uid}-value` : undefined}
        data-location-button
    >
        <span id="{uid}-value" class="lp-value">
            {#if current}
                <span class="lp-dot {stateTone(current.state)}" aria-hidden="true"></span>
                <span class="lp-name">{title(current)}</span>
                <span class="ol-muted lp-kind">{current.kind === 'userfs' ? '' : KIND[current.kind]() + ' · '}{stateText(current.state)}</span>
            {:else}
                <span class="ol-muted">{t('Choose where…')}</span>
            {/if}
        </span>
        <span class="lp-caret" aria-hidden="true">▾</span>
    </button>
    {#if open && list}
        <div class="lp-list" role="listbox" aria-label={label || t('Location')} data-location-list>
            {#each list as l, i (l.id + i)}
                <div
                    class="lp-option"
                    class:chosen={l.id === location}
                    class:off={!l.allowed}
                    role="option"
                    tabindex="-1"
                    data-index={i}
                    aria-selected={l.id === location}
                    aria-disabled={!l.allowed}
                    data-location={l.id}
                    onclick={() => pick(l)}
                    onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(l); } }}
                >
                    <span class="lp-dot {stateTone(l.state)}" aria-hidden="true"></span>
                    <span class="lp-text">
                        <span class="lp-line"><strong>{title(l)}</strong> <span class="ol-muted">· {l.kind === 'userfs' ? '' : KIND[l.kind]() + ' · '}{stateText(l.state)}{l.free_bytes !== undefined && l.total_bytes ? ` · ${t('{free} free', {free: formatBytes(l.free_bytes)})}` : ''}</span></span>
                        {#if l.detail && l.detail !== l.name}<span class="ol-muted lp-detail">{l.detail}</span>{/if}
                        {#if l.uses.length}<span class="lp-detail">{t('In use by')}: {l.uses.map(useWord).join(', ')}</span>{/if}
                        {#if !l.allowed}<span class="lp-why" data-why={l.code}>{reasonText(l)}</span>{:else if noteText(l)}<span class="ol-muted lp-detail">{noteText(l)}</span>{/if}
                    </span>
                </div>
            {/each}
            {#if !list.some((l) => l.kind !== 'userfs') && kinds.some((k) => k !== 'userfs')}
                <div class="ol-muted lp-empty" data-location-none>{t('No USB stick is plugged in and no network share is set up.')}</div>
            {/if}
        </div>
    {/if}
    </div>
    {#if current}
        {#if current.state === 'missing'}
            <div class="ol-warn" data-location-missing>{t('The USB stick {name} is not plugged in now; what goes there is skipped until it is.', {name: current.name})}</div>
        {:else if current.state_detail && stateTone(current.state) === 'bad'}
            <div class="ol-warn" data-location-state>{current.state_detail}</div>
        {/if}
        <label class="lp-folder" for="{uid}-folder">
            <span>{current.kind === 'userfs' ? t('Folder on the system storage') : current.kind === 'usb' ? t('Folder on the stick') : t('Folder on the share')}</span>
            <input
                id="{uid}-folder"
                class="hmm-input hmm-mono"
                bind:value={folder}
                oninput={typed}
                onfocus={() => void lookup()}
                list="{uid}-dirs"
                readonly={fixed}
                disabled={disabled}
                placeholder={current.kind === 'share' ? t('(the share itself)') : (rule?.default ?? '')}
                autocomplete="off"
                spellcheck="false"
                data-location-folder
            />
            <datalist id="{uid}-dirs">
                {#each dirs as d (d)}<option value={d}></option>{/each}
            </datalist>
        </label>
        {#if problem}
            <div class="ol-warn" data-folder-problem>{t(problem, {prefix: current.userfs_prefix ?? ''})}</div>
        {:else if dirsError}
            <div class="ol-warn" data-folder-error>{dirsError}</div>
        {:else if exists === false && folder}
            <div class="ol-muted" data-folder-new>{t('A new folder: it is made when it is first used.')}</div>
        {/if}
    {/if}
    {#if error}<div class="ol-warn">{error}</div>{/if}
    <a class="lp-manage" href="/system/storage" use:link data-manage-storage>{t('Manage sticks and shares')}</a>
</div>

<style>
    .lp { position: relative; display: flex; flex-direction: column; gap: 6px; max-width: min(560px, 100%); min-width: 0; }
    .lp-label { font-weight: 600; }
    .lp-button { display: flex; align-items: center; justify-content: space-between; gap: 8px; text-align: left; white-space: normal; min-height: 36px; width: 100%; }
    .lp-value { display: flex; align-items: baseline; gap: 6px; flex-wrap: wrap; min-width: 0; overflow-wrap: anywhere; }
    .lp-caret { flex: 0 0 auto; }
    .lp-dot { width: 9px; height: 9px; border-radius: 50%; flex: 0 0 auto; align-self: center; background: var(--hmm-fg-faint); }
    .lp-dot.good { background: var(--hmm-ok); }
    .lp-dot.bad { background: var(--hmm-error); }
    .lp-anchor { position: relative; }
    .lp-list {
        position: absolute; z-index: 20; top: 100%; left: 0; right: 0; margin-top: 2px;
        background: var(--hmm-bg); border: 1px solid var(--hmm-border); border-radius: 6px; box-shadow: 0 6px 20px rgb(0 0 0 / 0.25);
        max-height: min(60vh, 420px); overflow-y: auto; padding: 4px;
    }
    .lp-option { display: flex; gap: 10px; align-items: flex-start; padding: 8px; border-radius: 4px; cursor: pointer; }
    .lp-option .lp-dot { margin-top: 5px; align-self: flex-start; }
    .lp-option:hover:not(.off), .lp-option:focus-visible { background: var(--hmm-accent-bg); outline: none; }
    .lp-option.chosen { box-shadow: inset 3px 0 0 var(--hmm-accent); }
    .lp-option.off { cursor: not-allowed; opacity: 0.75; }
    .lp-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; overflow-wrap: anywhere; }
    .lp-detail, .lp-why { font-size: var(--hmm-font-size-small); }
    .lp-why { color: var(--hmm-warn); }
    .lp-empty { padding: 8px; }
    .lp-folder { display: flex; flex-direction: column; gap: var(--ol-label-gap, 4px); }
    .lp-folder input { max-width: 100%; }
    .lp-manage { font-size: var(--hmm-font-size-small); align-self: flex-start; }
</style>
