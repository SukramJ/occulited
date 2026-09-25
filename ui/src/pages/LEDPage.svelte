<script lang="ts">
    /*
     * Task 95: System → Status LED. The RPI-RF-MOD's RGB LED (Charly, CCU3) is occulited's now; this
     * page says what it shows and why, and decides what it shows. The simple view is what most
     * people need - on or off, the look when all is fine, the night, six switches - and everything
     * else (order, colours, patterns, the external control, locate) sits behind "Customise",
     * remembered per browser. On a box without a status LED the page says so, and offers the red
     * power LED as an error light and the board LEDs' switch, which moved here from Network.
     */
    import {onMount} from 'svelte';
    import {pageLife} from '../lib/pagelife.svelte';
    import {api} from '../lib/api';
    import {t} from '../lib/i18n.svelte';
    import SystemTitle from '../lib/SystemTitle.svelte';
    import {auth} from '../lib/auth.svelte';
    import {ask} from '../lib/dialog.svelte';
    import {link} from '../lib/router.svelte';
    import Loading from '../lib/Loading.svelte';
    import Help from '../lib/Help.svelte';
    import Notice from '../lib/Notice.svelte';
    import LEDSwatch from '../lib/LEDSwatch.svelte';
    import {revealDetails} from '../lib/reveal';
    import {moved} from '../lib/sortable';
    import {Sortable} from '../lib/sortable.svelte';
    import SortHandle from '../lib/SortHandle.svelte';
    import {SIMPLE_STATES, backgroundOf, changed, normalChoice, normalizeLook, overNormalOption, secondColors, type LEDConfig, type LEDLook, type LEDState, type LEDStateConfig, type LEDView} from '../lib/led';

    interface BoardLEDs {
        disabled: boolean;
        leds: {name: string; path: string; trigger: string; normal: string}[];
    }

    let view = $state<LEDView | null>(null);
    let st = $state<LEDState | null>(null);
    let boards = $state<BoardLEDs | null>(null);
    let draft = $state<LEDConfig | null>(null);
    let error = $state('');
    let notice = $state('');
    let busy = $state('');
    let advanced = $state(false);
    const admin = $derived(auth.role === 'admin');
    const dirty = $derived(changed(view?.config ?? null, draft));
    const colors = $derived(view?.hardware.colors?.length ? view.hardware.colors : ['red', 'green', 'blue', 'yellow', 'cyan', 'magenta', 'white', 'off']);

    const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T;

    const life = pageLife();
    onMount(() => {
        try {
            advanced = localStorage.getItem('ol.led.advanced') === '1';
        } catch {
            /* storage may be unavailable */
        }
        void load();
        // task 177: no polls while another page shows, a refresh when it comes back
        const id = setInterval(() => life.active && loadState(), 5000);
        const stopReturn = life.onReturn(() => void loadState());
        return () => {
            stopReturn();
            clearInterval(id);
        };
    });

    async function load() {
        try {
            const [v, s] = await Promise.all([api.get<LEDView>('/api/system/v1/led'), api.get<LEDState>('/api/system/v1/led/state')]);
            const keep = draft !== null && changed(view?.config ?? null, draft);
            view = v;
            st = s;
            if (!keep) draft = clone(v.config);
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
        try {
            boards = await api.get<BoardLEDs>('/api/system/v1/leds');
        } catch {
            boards = null;
        }
    }

    async function loadState() {
        try {
            st = await api.get<LEDState>('/api/system/v1/led/state');
        } catch {
            /* the next poll tries again */
        }
    }

    function onToggleAdvanced(e: Event) {
        advanced = (e.currentTarget as HTMLDetailsElement).open;
        try {
            localStorage.setItem('ol.led.advanced', advanced ? '1' : '0');
        } catch {
            /* ignore */
        }
    }

    async function run(what: string, fn: () => Promise<void>) {
        busy = what;
        notice = '';
        try {
            await fn();
            error = '';
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = '';
        }
    }

    const save = () =>
        run('save', async () => {
            if (!draft) return;
            view = await api.put<LEDView>('/api/system/v1/led', draft);
            draft = clone(view.config);
            notice = t('Saved.');
            await loadState();
        });

    function discard() {
        if (view) draft = clone(view.config);
    }

    function reset() {
        if (view && draft) draft = {...clone(view.defaults), pwr_error_light: draft.pwr_error_light};
    }

    async function setEnabled(e: Event) {
        const box = e.currentTarget as HTMLInputElement;
        if (!draft) return;
        if (!box.checked && !(await ask({message: t('The LED will stay dark, errors included. Only booting, shutdown and the recovery system still light it.'), confirm: t('Switch off')}))) {
            box.checked = true;
            return;
        }
        draft.enabled = box.checked;
    }

    const locate = () =>
        run('locate', async () => {
            st = st?.locate ? await api.del<LEDState>('/api/system/v1/led/locate') : await api.post<LEDState>('/api/system/v1/led/locate', {});
        });

    const test = (l: LEDLook) =>
        run('test', async () => {
            st = await api.post<LEDState>('/api/system/v1/led/preview', normalizeLook(l, colors));
        });

    const clearOverrides = () =>
        run('overrides', async () => {
            st = await api.del<LEDState>('/api/system/v1/led/overrides');
        });

    const toggleBoards = () =>
        run('boards', async () => {
            if (boards) boards = await api.put<BoardLEDs>('/api/system/v1/leds', {disabled: !boards.disabled});
        });

    function stateRow(id: string): LEDStateConfig | undefined {
        return draft?.states.find((s) => s.id === id);
    }

    function lookOf(s: Partial<LEDLook>): LEDLook {
        return {color: s.color ?? 'off', pattern: s.pattern ?? 'solid', ...(s.color2 ? {color2: s.color2} : {}), ...(s.over_normal ? {over_normal: true} : {})};
    }

    /** Applies a change of colour, pattern or blinking over normal to a row (or to normal, or locate) in the API's spelling. */
    function setLook(target: Partial<LEDLook>, patch: Partial<LEDLook>) {
        const n = normalizeLook({...lookOf(target), ...patch}, colors);
        target.color = n.color;
        target.pattern = n.pattern;
        if (n.color2) target.color2 = n.color2;
        else delete target.color2;
        if (n.over_normal) target.over_normal = true;
        else delete target.over_normal;
    }

    /** The colour a look's dark phase shows on the box with the draft's settings (blinking over normal), '' for dark. */
    function bgOf(l: Partial<LEDLook>): string {
        return draft ? backgroundOf(l, draft.normal, draft.enabled) : '';
    }

    function setNormal(choice: 'blue' | 'green' | 'off') {
        if (draft) draft.normal = {color: choice, pattern: 'solid'};
    }

    // task 162: the states are ordered by their handle - drag it, ↑/↓ on it, Alt+↑/↓ in the row; the
    // fixed rows have none, and neither has a user who may only look
    let stateList = $state<HTMLElement | null>(null);
    const sort = new Sortable({
        rows: () => Array.from(stateList?.querySelectorAll<HTMLElement>(':scope > li[data-led-row]') ?? []),
        length: () => draft?.states.length ?? 0,
        name: (i) => stateLabel(draft?.states[i]?.id ?? ''),
        commit: (from, to) => {
            if (draft) draft.states = moved(draft.states, from, to);
        },
        enabled: () => admin,
    });

    function toggleWarning(id: string, shown: boolean) {
        if (!draft) return;
        draft.warnings_off = shown ? draft.warnings_off.filter((w) => w !== id) : [...draft.warnings_off, id];
    }

    function patternsFor(current: string, withAlternate = true): string[] {
        const list = (view?.hardware.patterns?.length ? view.hardware.patterns : ['solid', 'slow', 'fast', 'flash', 'double', 'alternate']).filter((p) => withAlternate || p !== 'alternate');
        return list.includes(current) || !current ? list : [...list, current];
    }

    function colorName(c: string): string {
        switch (c) {
            case 'red':
                return t('Red');
            case 'green':
                return t('Green');
            case 'blue':
                return t('Blue');
            case 'yellow':
                return t('Yellow');
            case 'cyan':
                return t('Cyan');
            case 'magenta':
                return t('Magenta');
            case 'white':
                return t('White');
        }
        return t('Dark');
    }

    function patternName(p: string): string {
        switch (p) {
            case 'slow':
                return t('slow blink');
            case 'fast':
                return t('fast blink');
            case 'flash':
                return t('short flash');
            case 'double':
                return t('double flash');
            case 'alternate':
                return t('alternating');
        }
        return t('steady');
    }

    function lookText(l: LEDLook, background = ''): string {
        if (l.color === 'off') return t('Dark');
        if (l.pattern === 'alternate' && l.color2) return t('{a} and {b}, alternating', {a: colorName(l.color), b: colorName(l.color2).toLowerCase()});
        if (background && background !== 'off') return t('{color}, {pattern} over {background}', {color: colorName(l.color), pattern: patternName(l.pattern), background: colorName(background).toLowerCase()});
        return t('{color}, {pattern}', {color: colorName(l.color), pattern: patternName(l.pattern)});
    }

    function stateLabel(id: string): string {
        switch (id) {
            case 'radio-down':
                return t('Radio or interface down');
            case 'no-network':
                return t('No network');
            case 'service-failed':
                return t('A service has failed');
            case 'storage-replace':
                return t('Storage should be replaced');
            case 'interfaces-starting':
                return t('Interfaces starting');
            case 'external':
                return t('External control');
            case 'status-warning':
                return t('Status page warnings');
            case 'system-update':
                return t('System update available');
            case 'addon-update':
                return t('Addon updates available');
            case 'no-internet':
                return t('No internet');
            case 'booting':
                return t('Booting');
            case 'shutdown':
                return t('Shutting down');
            case 'preview':
                return t('Test');
            case 'locate':
                return t('Locate');
        }
        return t('Everything fine');
    }

    function stateMeaning(id: string): string {
        switch (id) {
            case 'radio-down':
                // hs485d counts only where a wired gateway is configured (task 158's follow-up)
                return t('multimacd, rfd or hmipserver is not running, or hs485d where a wired gateway is configured');
            case 'no-network':
                return t('no network link or no address');
            case 'service-failed':
                return t('a system service has failed');
            case 'storage-replace':
                return t('the storage check says the SD card or disk should be replaced');
            case 'interfaces-starting':
                // task 158: at the boot and whenever one of them is restarted
                return t('multimacd, rfd, hmipserver or hs485d is starting');
            case 'external':
                return t('a colour set through the API, for example by Home Assistant or Node-RED');
            case 'status-warning':
                return t('a warning on the Status page that nobody silenced');
            case 'system-update':
                return t('a newer openccu-lite release is available');
            case 'addon-update':
                return t('an installed addon has an update');
            case 'no-internet':
                // task 95 (D-67): the check connects to hosts the box talks to anyway, and the page names them
                return view?.internet_hosts?.length
                    ? t('the system has an address but reaches none of the hosts it talks to anyway: {hosts}', {hosts: view.internet_hosts.join(', ')})
                    : t('nothing to check against: the addon catalogue is switched off or local, and ACME is not configured');
            case 'booting':
                return t('the system is starting, until the radio is known');
            case 'shutdown':
                return t('the system is shutting down or restarting');
            case 'preview':
                return t('the Test button, for ten seconds');
            case 'locate':
                return t('the Locate button, to find the system');
        }
        return t('nothing else is active');
    }

    function stateHref(id: string): string {
        switch (id) {
            case 'radio-down':
            case 'interfaces-starting':
                return '/radio';
            case 'no-network':
            case 'no-internet':
                return '/system/network';
            case 'service-failed':
                return '/system/services';
            case 'status-warning':
            case 'storage-replace':
            case 'system-update':
                return '/';
            case 'addon-update':
                return '/addons';
        }
        return '';
    }

    function reasonText(s: LEDState): string {
        const sh = s.shown;
        switch (sh.source) {
            case 'state':
                return sh.detail ? `${stateLabel(sh.id)}: ${sh.detail}` : stateLabel(sh.id);
            case 'override':
                return t('Set through the API as {id}', {id: sh.id});
            case 'locate':
                return t('Locating the system');
            case 'preview':
                return t('Testing a look');
            case 'night':
                return t('Night mode');
            case 'off':
                return t('The status LED is switched off');
            case 'fixed':
                return stateLabel(sh.id);
        }
        return t('Everything is fine');
    }

    function noLEDReason(r?: string): string {
        switch (r) {
            case 'no-module':
                return t('The radio module has no status LED.');
            case 'no-leds':
                return t('The LED devices of the radio module are missing.');
            case 'virtual':
                return t('This is a virtual machine.');
            case 'container':
                return t('In a container the LED belongs to the host.');
            case 'hm-lgw':
                return t('In LAN gateway mode the LED shows the blue of the gateway.');
            case 'detecting':
                return t('The radio module is still being detected.');
        }
        return '';
    }

    function timeOf(iso: string): string {
        return new Date(iso).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'});
    }

    // the two fixed states the scripts share with the controller, in the advanced list's order
    const FIXED_ROWS: {id: string; look: LEDLook}[] = [
        {id: 'shutdown', look: {color: 'yellow', pattern: 'fast'}},
        {id: 'booting', look: {color: 'yellow', pattern: 'solid'}},
    ];
</script>

<SystemTitle><Help>{t('The RGB LED on the radio module of a Charly or a CCU3. The system shows its state on it, and this page decides what it shows. Booting (yellow) and the recovery system (magenta) are fixed.')}</Help></SystemTitle>

{#if !view || !st || !draft}
    <Loading {error} />
{:else}
    {#if error}<div class="ol-notice error">{error}</div>{/if}
    {#if notice}<div class="ol-notice">{notice}</div>{/if}

    {#if !view.available}
        <Notice id="no-led"><strong>{t('This system has no status LED.')}</strong> {noLEDReason(view.reason)}</Notice>
        {#if view.pwr}
            <h2>{t('Power LED')}</h2>
            <fieldset class="ol-led-settings" disabled={!admin}>
                <label class="ol-led-check">
                    <input type="checkbox" bind:checked={draft.pwr_error_light} data-led="pwr" />
                    {t('Use the red power LED as an error light')}
                    <Help>{t('It blinks fast while the radio or a service is down, or the storage should be replaced, and returns to its normal light afterwards.')}</Help>
                </label>
                {#if st.pwr?.error}<div class="ol-warn">{t('It is blinking now.')}</div>{/if}
            </fieldset>
            {#if admin}
                <div class="ol-led-save">
                    <button class="hmm-button primary" onclick={save} disabled={!dirty || busy !== ''}>{t('Save')}</button>
                    <button class="hmm-button" onclick={discard} disabled={!dirty || busy !== ''}>{t('Discard')}</button>
                </div>
            {/if}
        {/if}
    {:else}
        <div class="ol-card ol-led-now" data-led-now={st.shown.id}>
            <LEDSwatch look={st.shown} background={st.shown.background} size={44} label={lookText(st.shown, st.shown.background)} />
            <div class="ol-led-now-text">
                <div class="ol-led-now-look">{lookText(st.shown, st.shown.background)}</div>
                <div class="ol-muted">{reasonText(st)} · {t('since {time}', {time: timeOf(st.shown.since)})}</div>
                {#if st.shown.source === 'state' && stateHref(st.shown.id)}<a href={stateHref(st.shown.id)} use:link>{t('Open page')}</a>{/if}
            </div>
            {#if admin}
                <div class="ol-led-now-actions">
                    <button class="hmm-button" onclick={locate} disabled={busy !== ''}>{st.locate ? t('Stop locating') : t('Locate')}</button>
                    <span class="ol-muted">{t('{look} for {n} minutes', {look: lookText(view.config.locate), n: Math.round(view.config.locate.duration_s / 60)})}</span>
                </div>
            {/if}
        </div>
        {#if st.conflict}<Notice kind="warning" id="led-conflict">{t('Another process keeps changing the LED. The system sets it again when its own state changes.')}</Notice>{/if}
        {#if st.write_error}<Notice kind="error" id="led-write">{t('The LED could not be written:')} {st.write_error}</Notice>{/if}
        {#if st.overrides.length}
            <div class="ol-led-overrides" data-led-overrides>
                <span class="ol-muted">{t('Set through the API:')}</span>
                {#each st.overrides as o (o.id)}
                    <span class="ol-led-override"><LEDSwatch look={o} /> <span class="hmm-mono">{o.id}</span> <span class="ol-muted">{o.until ? t('until {time}', {time: timeOf(o.until)}) : t('until cleared')}</span></span>
                {/each}
                {#if admin}<button class="hmm-button" onclick={clearOverrides} disabled={busy !== ''}>{t('Clear')}</button>{/if}
            </div>
        {/if}

        <h2>{t('Settings')}</h2>
        <fieldset class="ol-led-settings" disabled={!admin}>
            <label class="ol-led-check"><input type="checkbox" checked={draft.enabled} onchange={setEnabled} data-led="enabled" /> {t('Status LED on')}</label>

            <div class="ol-led-line">
                <span class="ol-led-key">{t('When everything is fine')}</span>
                <div class="ol-seg" role="radiogroup" aria-label={t('When everything is fine')}>
                    <button type="button" role="radio" aria-checked={normalChoice(draft.normal) === 'blue'} onclick={() => setNormal('blue')}>{t('Blue')}</button>
                    <button type="button" role="radio" aria-checked={normalChoice(draft.normal) === 'green'} onclick={() => setNormal('green')}>{t('Green')}</button>
                    <button type="button" role="radio" aria-checked={normalChoice(draft.normal) === 'off'} onclick={() => setNormal('off')}>{t('Off')}</button>
                </div>
                {#if normalChoice(draft.normal) === 'custom'}<span class="ol-muted">{lookText(draft.normal)}</span>{/if}
            </div>

            <label class="ol-led-check">
                <input type="checkbox" bind:checked={draft.night.enabled} data-led="night" />
                {t('Night mode')}
                <Help>{t('Between the two times the LED shows less: only errors, or nothing. Locate and Test still light it.')}</Help>
            </label>
            {#if draft.night.enabled}
                <div class="ol-led-line ol-led-sub">
                    <label class="ol-led-check">{t('from')} <input class="hmm-input" type="time" bind:value={draft.night.from} /></label>
                    <label class="ol-led-check">{t('to')} <input class="hmm-input" type="time" bind:value={draft.night.to} /></label>
                </div>
                <div class="ol-led-line ol-led-sub">
                    <span class="ol-led-key">{t('At night show')}</span>
                    <div class="ol-seg" role="radiogroup" aria-label={t('At night show')}>
                        <button type="button" role="radio" aria-checked={draft.night.show === 'errors'} onclick={() => draft && (draft.night.show = 'errors')}>{t('Only errors')}</button>
                        <button type="button" role="radio" aria-checked={draft.night.show === 'off'} onclick={() => draft && (draft.night.show = 'off')}>{t('Nothing')}</button>
                    </div>
                </div>
                {#if draft.night.show === 'off'}<div class="ol-warn ol-led-sub">{t('A failed radio will not be visible at night.')}</div>{/if}
            {/if}

            <h3>{t('Show on the LED')}</h3>
            <ul class="ol-led-rows">
                {#each SIMPLE_STATES as id (id)}
                    {@const s = stateRow(id)}
                    {#if s}
                        <li data-led-simple={id}>
                            <label class="ol-led-row">
                                <input type="checkbox" checked={s.enabled} onchange={(e) => (s.enabled = e.currentTarget.checked)} />
                                <LEDSwatch look={lookOf(s)} background={bgOf(s)} />
                                <span class="ol-led-row-text"><span>{stateLabel(id)}</span> <span class="ol-muted">{lookText(lookOf(s), bgOf(s))} · {stateMeaning(id)}</span></span>
                            </label>
                        </li>
                    {/if}
                {/each}
            </ul>

            <!-- task 98: both disclosures of this page open and close in place (lib/reveal.ts) -->
            <details class="ol-led-advanced" open={advanced} ontoggle={onToggleAdvanced} use:revealDetails data-led-advanced>
                <summary>{t('Customise states and order')}</summary>
                <p class="ol-muted">{t('The first active row decides what the LED shows. The fixed rows stay on top, and Everything fine is what is left.')}</p>
                <ol class="ol-led-list" bind:this={stateList}>
                    {#each FIXED_ROWS as row (row.id)}
                        <li class="ol-led-item fixed" data-led-fixed={row.id}>
                            <span class="ol-badge">{t('fixed')}</span>
                            <LEDSwatch look={row.look} />
                            <span class="ol-led-name">{stateLabel(row.id)}</span>
                            <span class="ol-muted">{lookText(row.look)} · {stateMeaning(row.id)}</span>
                        </li>
                    {/each}
                    <li class="ol-led-item fixed" data-led-fixed="locate">
                        <span class="ol-badge">{t('fixed')}</span>
                        <LEDSwatch look={lookOf(draft.locate)} />
                        <span class="ol-led-name">{stateLabel('locate')}</span>
                        <span class="ol-led-controls">
                            <select class="hmm-select" aria-label={t('Colour of {name}', {name: stateLabel('locate')})} value={draft.locate.color} onchange={(e) => draft && setLook(draft.locate, {color: e.currentTarget.value})}>
                                {#each colors.filter((c) => c !== 'off') as c (c)}<option value={c}>{colorName(c)}</option>{/each}
                            </select>
                            <select class="hmm-select" aria-label={t('Pattern of {name}', {name: stateLabel('locate')})} value={draft.locate.pattern} onchange={(e) => draft && setLook(draft.locate, {pattern: e.currentTarget.value})}>
                                {#each patternsFor(draft.locate.pattern, false) as p (p)}<option value={p}>{patternName(p)}</option>{/each}
                            </select>
                            <label class="ol-led-check">
                                <input class="hmm-input ol-led-num" type="number" min="1" max="60" value={Math.round(draft.locate.duration_s / 60)} onchange={(e) => draft && (draft.locate.duration_s = Math.max(1, Math.min(60, Number(e.currentTarget.value) || 5)) * 60)} aria-label={t('Minutes')} />
                                {t('min')}
                            </label>
                        </span>
                    </li>
                    {#each draft.states as s, i (s.id)}
                        <!-- the row takes Alt+↑/↓ from the controls inside it, bubbling up: it is no control itself -->
                        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
                        <li class="ol-led-item ol-sortrow" data-led-row={s.id} class:ol-dragging={sort.dragging(s.id)} class:ol-drop-before={sort.before(i)} class:ol-drop-after={sort.after(i)} onkeydown={(ev) => sort.rowKey(ev, i)}>
                            {#if admin}<SortHandle sortable={sort} index={i} key={s.id} name={stateLabel(s.id)} />{/if}
                            <input type="checkbox" checked={s.enabled} onchange={(e) => (s.enabled = e.currentTarget.checked)} aria-label={stateLabel(s.id)} />
                            {#if s.id === 'external'}
                                <span class="ol-led-name">{stateLabel(s.id)}<Help>{t('Home Assistant, Node-RED and other programs set a colour with an API token of the role led. Where this row stands decides which states they may cover.')}</Help></span>
                                <span class="ol-led-controls">
                                    <label class="ol-led-check">
                                        {t('at most')}
                                        <input class="hmm-input ol-led-num" type="number" min="1" max="1440" value={Math.round(draft.external.max_duration_s / 60)} onchange={(e) => draft && (draft.external.max_duration_s = Math.max(1, Math.min(1440, Number(e.currentTarget.value) || 60)) * 60)} aria-label={t('Minutes')} />
                                        {t('min')}
                                    </label>
                                    <label class="ol-led-check"><input type="checkbox" bind:checked={draft.external.allow_until_cleared} /> {t('until cleared allowed')}</label>
                                </span>
                            {:else}
                                <LEDSwatch look={lookOf(s)} background={bgOf(s)} />
                                <span class="ol-led-name">{stateLabel(s.id)}</span>
                                <span class="ol-led-controls">
                                    <select class="hmm-select" aria-label={t('Colour of {name}', {name: stateLabel(s.id)})} value={s.color} onchange={(e) => setLook(s, {color: e.currentTarget.value})}>
                                        {#each colors as c (c)}<option value={c}>{colorName(c)}</option>{/each}
                                    </select>
                                    {#if s.color !== 'off'}
                                        <select class="hmm-select" aria-label={t('Pattern of {name}', {name: stateLabel(s.id)})} value={s.pattern} onchange={(e) => setLook(s, {pattern: e.currentTarget.value})}>
                                            {#each patternsFor(s.pattern ?? '') as p (p)}<option value={p}>{patternName(p)}</option>{/each}
                                        </select>
                                    {/if}
                                    {#if s.pattern === 'alternate'}
                                        <select class="hmm-select" aria-label={t('Second colour of {name}', {name: stateLabel(s.id)})} value={s.color2} onchange={(e) => setLook(s, {color2: e.currentTarget.value})}>
                                            {#each secondColors(s.color ?? '', colors) as c (c)}<option value={c}>{colorName(c)}</option>{/each}
                                        </select>
                                    {/if}
                                    {#if admin}<button type="button" class="hmm-button" onclick={() => test(lookOf(s))} disabled={busy !== '' || s.color === 'off'}>{t('Test')}</button>{/if}
                                </span>
                                <!-- task 134: a blink over the normal colour instead of over dark, for the patterns with a dark phase -->
                                {#if overNormalOption(s, draft.normal) === 'yes'}
                                    <div class="ol-led-subs" data-led-over={s.id}>
                                        <label class="ol-led-check">
                                            <input type="checkbox" checked={!!s.over_normal} onchange={(e) => setLook(s, {over_normal: e.currentTarget.checked})} />
                                            {t('Blink over the normal colour')}
                                            <Help>{t('The dark phase of the blink shows the colour of Everything fine. While that is off, and at night, the LED blinks over dark.')}</Help>
                                        </label>
                                    </div>
                                {:else if overNormalOption(s, draft.normal) === 'same'}
                                    <div class="ol-led-subs ol-muted" data-led-over={s.id}>{t('Blinking over the normal colour is not offered: it is the same colour, the blink would not be seen.')}</div>
                                {/if}
                            {/if}
                            {#if s.id === 'status-warning' && view.warning_ids.length}
                                <div class="ol-led-subs">
                                    {#each view.warning_ids as w (w)}
                                        <label class="ol-led-check"><input type="checkbox" checked={!draft.warnings_off.includes(w)} onchange={(e) => toggleWarning(w, e.currentTarget.checked)} /> <span class="hmm-mono">{w}</span></label>
                                    {/each}
                                </div>
                            {/if}
                            {#if s.id === 'service-failed'}
                                <div class="ol-led-subs"><label class="ol-led-check"><input type="checkbox" bind:checked={draft.addon_units} /> {t('A failed addon counts as well')}</label></div>
                            {/if}
                            {#if s.id === 'no-internet'}
                                <!-- task 95 (D-67): which host the check connects to -->
                                <div class="ol-led-subs ol-muted" data-led-internet>{stateMeaning(s.id)}</div>
                            {/if}
                        </li>
                    {/each}
                    <li class="ol-led-item fixed" data-led-fixed="normal">
                        <span class="ol-badge">{t('last')}</span>
                        <LEDSwatch look={draft.normal} />
                        <span class="ol-led-name">{stateLabel('normal')}</span>
                        <span class="ol-led-controls">
                            <select class="hmm-select" aria-label={t('Colour of {name}', {name: stateLabel('normal')})} value={draft.normal.color} onchange={(e) => draft && setLook(draft.normal, {color: e.currentTarget.value})}>
                                {#each colors as c (c)}<option value={c}>{colorName(c)}</option>{/each}
                            </select>
                            {#if draft.normal.color !== 'off'}
                                <select class="hmm-select" aria-label={t('Pattern of {name}', {name: stateLabel('normal')})} value={draft.normal.pattern} onchange={(e) => draft && setLook(draft.normal, {pattern: e.currentTarget.value})}>
                                    {#each patternsFor(draft.normal.pattern) as p (p)}<option value={p}>{patternName(p)}</option>{/each}
                                </select>
                            {/if}
                            {#if draft.normal.pattern === 'alternate'}
                                <select class="hmm-select" aria-label={t('Second colour of {name}', {name: stateLabel('normal')})} value={draft.normal.color2} onchange={(e) => draft && setLook(draft.normal, {color2: e.currentTarget.value})}>
                                    {#each secondColors(draft.normal.color, colors) as c (c)}<option value={c}>{colorName(c)}</option>{/each}
                                </select>
                            {/if}
                        </span>
                    </li>
                </ol>
                <div class="ol-sronly" role="status" aria-live="polite">{sort.live}</div>
                {#if admin}<div class="ol-actions"><button type="button" class="hmm-button" onclick={reset}>{t('Reset to defaults')}</button></div>{/if}
            </details>
        </fieldset>
        {#if admin}
            <div class="ol-led-save">
                <button class="hmm-button primary" onclick={save} disabled={!dirty || busy !== ''}>{t('Save')}</button>
                <button class="hmm-button" onclick={discard} disabled={!dirty || busy !== ''}>{t('Discard')}</button>
                {#if dirty}<span class="ol-muted">{t('Unsaved changes')}</span>{/if}
            </div>
        {/if}

        <details class="ol-led-legend" use:revealDetails data-led-legend>
            <summary>{t('What the colours mean')}</summary>
            <ul class="ol-led-rows">
                {#each [...FIXED_ROWS].reverse() as row (row.id)}
                    <li><LEDSwatch look={row.look} /> <span>{lookText(row.look)}</span> <span class="ol-muted">{stateLabel(row.id)}</span></li>
                {/each}
                <li><LEDSwatch look={{color: 'magenta', pattern: 'slow'}} /> <span>{lookText({color: 'magenta', pattern: 'slow'})}</span> <span class="ol-muted">{t('Recovery system starting')}</span></li>
                <li><LEDSwatch look={{color: 'magenta', pattern: 'solid'}} /> <span>{lookText({color: 'magenta', pattern: 'solid'})}</span> <span class="ol-muted">{t('Recovery system active')}</span></li>
                <li><LEDSwatch look={{color: 'magenta', pattern: 'fast'}} /> <span>{lookText({color: 'magenta', pattern: 'fast'})}</span> <span class="ol-muted">{t('Update installing')}</span></li>
                <li><LEDSwatch look={lookOf(draft.locate)} /> <span>{lookText(lookOf(draft.locate))}</span> <span class="ol-muted">{stateLabel('locate')}</span></li>
                {#if draft.enabled}
                    {#each draft.states.filter((s) => s.enabled && s.id !== 'external') as s (s.id)}
                        <li><LEDSwatch look={lookOf(s)} background={bgOf(s)} /> <span>{lookText(lookOf(s), bgOf(s))}</span> <span class="ol-muted">{stateLabel(s.id)}</span></li>
                    {/each}
                    <li><LEDSwatch look={draft.normal} /> <span>{lookText(draft.normal)}</span> <span class="ol-muted">{stateLabel('normal')}</span></li>
                {/if}
            </ul>
        </details>
    {/if}

    {#if boards && boards.leds.length}
        <h2>{t('Board LEDs')}<Help>{t('The green and red LEDs of the Raspberry Pi board itself.')}</Help></h2>
        <div class="ol-led-boards">
            <div>{boards.leds.map((l) => `${l.name}: ${l.trigger || '–'}`).join(' · ')}</div>
            {#if admin}
                <div class="ol-actions"><button class="hmm-button" onclick={toggleBoards} disabled={busy !== ''}>{boards.disabled ? t('Switch the onboard LEDs on') : t('Switch the onboard LEDs off')}</button></div>
            {/if}
        </div>
    {/if}
{/if}

<style>
    .ol-led-now { display: flex; flex-wrap: wrap; align-items: center; gap: 14px; margin-bottom: 12px; }
    .ol-led-now-text { flex: 1 1 220px; min-width: 0; }
    .ol-led-now-look { font-size: 1.15em; font-weight: 600; }
    .ol-led-now-actions { display: flex; flex-direction: column; align-items: flex-start; gap: 4px; }
    .ol-led-overrides { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; margin-bottom: 12px; }
    .ol-led-override { display: inline-flex; align-items: center; gap: 4px; }
    .ol-led-settings { border: 0; padding: 0; margin: 0; min-width: 0; display: flex; flex-direction: column; gap: 10px; }
    .ol-led-settings h3 { margin: 8px 0 0; }
    .ol-led-check { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px; }
    .ol-led-line { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
    .ol-led-key { min-width: 13em; }
    .ol-led-sub { margin-left: 24px; }
    .ol-seg > [aria-checked='true'] { color: var(--hmm-accent); background: var(--hmm-accent-bg); font-weight: 600; }
    .ol-led-rows { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
    .ol-led-rows > li { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; }
    .ol-led-row { display: flex; align-items: center; gap: 8px; min-width: 0; }
    .ol-led-row-text { display: flex; flex-wrap: wrap; column-gap: 6px; min-width: 0; }
    .ol-led-advanced, .ol-led-legend { margin: 6px 0; }
    .ol-led-advanced > summary, .ol-led-legend > summary { cursor: pointer; font-weight: 600; margin: 10px 0 6px; }
    .ol-led-list { list-style: none; margin: 8px 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
    .ol-led-item { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; padding: 6px 8px; border: 1px solid var(--hmm-border-muted); border-radius: var(--hmm-radius); background: var(--hmm-card-bg); min-width: 0; }
    .ol-led-item.fixed { background: var(--hmm-bg-sunken); }
    .ol-led-name { font-weight: 600; min-width: 13em; }
    .ol-led-controls { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
    .ol-led-subs { flex-basis: 100%; display: flex; flex-wrap: wrap; gap: 4px 14px; padding-left: 64px; }
    .ol-led-num { width: 4.5em; }
    .ol-led-save { position: sticky; bottom: 0; z-index: 1; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 8px 0; background: var(--hmm-bg); }
    .ol-led-boards { display: flex; flex-direction: column; gap: 8px; max-width: 640px; margin-bottom: 8px; }
    @media (max-width: 640px) {
        .ol-led-key, .ol-led-name { min-width: 0; }
        .ol-led-name { flex: 1 1 auto; }
        .ol-led-sub { margin-left: 0; }
        .ol-led-subs { padding-left: 0; }
    }
</style>
