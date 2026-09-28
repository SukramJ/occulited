<script lang="ts">
    /*
     * The power button of the top bar: at the far right, right of the settings gear, for an
     * administrator only. Rare, consequential actions belong at the edge rather than between the
     * everyday icons, the far right is where a power control sits in most appliance UIs, and a
     * hairline before it keeps a click meant for Settings from landing here.
     *
     * The menu offers a reboot, a halt and a reboot into the recovery system; every entry asks
     * again through the shell's dialog, and after the box has answered the page turns into a
     * full-page state instead of a dead page: a reboot polls the health route until the box answers
     * with a new uptime and reloads, a halt polls nothing, and a recovery boot links to the box's
     * address, where the recovery system answers with a page of its own.
     *
     * A container has no recovery system, so the entry is left out; a staged system update would
     * be installed by one, so while an update is staged the entry says so and refuses.
     *
     * The recovery system has no HTTPS, and a browser that remembers HSTS for the box's name refuses
     * it by name (D-56): the question and the page after it link to the box's IP address, resolved
     * when the question opens (lib/recovery.ts). The name follows as a second link only while HSTS is
     * known to be off. A plain reboot with an update set to install ends in the recovery too, so its
     * question and waiting page name the same address.
     *
     * Task 292: the first entry is Logout, then a divider, then the three power entries. It asks
     * nothing - signing out is harmless and undone by signing in - and it is left out, with its
     * divider, while the login is switched off: the anonymous administrator has nothing to sign out
     * of. The Account page keeps its own Logout. The arrow keys, Home and End move between the
     * entries and pass over the divider, which is a separator and never takes the focus.
     */
    import {api, ApiError, type HTTPSView} from './api';
    import {auth, logout, refresh} from './auth.svelte';
    import {ask} from './dialog.svelte';
    import {i18n, t} from './i18n.svelte';
    import {link, router} from './router.svelte';
    import Icon, {type IconName} from './Icon.svelte';
    import ModalDialog from './ModalDialog.svelte';
    import BootBar from './BootBar.svelte';
    import {bootText, EASE_MS, type BootEntry, type BootKind} from './bootbar';
    import {beginBoot, removeEntry, watchBoot} from './bootwatch';
    import {boxUptime, haltKind, type HaltKind} from './power';
    import {isIPLiteral, lookupBoxAddress, LOOKUP_TIMEOUT_MS, nameLink, withTimeout, type BoxAddress, type NetworkView} from './recovery';
    import {hstsRemembered} from './hsts';
    import {step} from './systemmenu';

    type Action = 'reboot' | 'halt' | 'recovery';
    type Phase = '' | 'rebooting' | 'halted' | 'recovery';

    /** how often a reboot looks whether the box is back */
    const POLL_MS = 2000;

    let open = $state(false);
    let root = $state<HTMLElement | null>(null);
    let pop = $state<HTMLElement | null>(null);
    let button = $state<HTMLButtonElement | null>(null);
    // on a phone the header wraps and the button may sit anywhere: the menu is then laid over the
    // viewport's width under the button instead of hanging off its right edge
    let popStyle = $state('');
    // what the box says when the menu opens
    let container = $state(false);
    // what a halted box needs to start again (lib/power.ts), from /VERSION's PLATFORM
    let kind = $state<HaltKind>('unknown');
    let staged = $state<string | null>(null);
    // the staged update is set to install at the next boot: a reboot then ends in the recovery
    let armed = $state(false);
    let phase = $state<Phase>('');
    let refused = $state(false);
    let failure = $state<{title: string; message: string} | null>(null);
    // where the recovery answers, and the name as a second link while HSTS is off; set when a
    // question that leads into the recovery opens
    let address = $state<BoxAddress | null>(null);
    let nameURL = $state('');
    // task 94: the countdown of a reboot (lib/bootbar.ts), and whether a halted box stopped answering
    let platform = $state('');
    let boot = $state<BootEntry | null>(null);
    let halted = $state(false);

    const entries = $derived(
        ([
            {action: 'reboot', icon: 'restart', label: t('Reboot'), hint: t('Back in about a minute')},
            {action: 'halt', icon: 'power', label: t('Halt'), hint: t('Stays off until it is powered on again')},
            {action: 'recovery', icon: 'lifebuoy', label: t('Reboot into the recovery system'), hint: t('For repairs and firmware images; leave it from its own page')},
        ] satisfies {action: Action; icon: IconName; label: string; hint: string}[]).filter((e) => e.action !== 'recovery' || !container),
    );

    // nothing to sign out of while the login is off (task 29)
    const offerLogout = $derived(!auth.authOff);
    const buttonLabel = $derived(offerLogout ? t('Log out, reboot or shut down') : t('Reboot or shut down'));

    async function signOut() {
        open = false;
        try {
            await logout();
        } catch {
            // the session is dropped in the shell either way (auth.logout); ask the box what is left
            await refresh();
        }
    }

    // the entries as the arrow keys see them: the divider is no menuitem, so it is passed over
    function items(): HTMLElement[] {
        return pop ? [...pop.querySelectorAll<HTMLElement>('[role="menuitem"]')] : [];
    }
    function onMenuKey(ev: KeyboardEvent) {
        if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(ev.key)) return;
        const all = items();
        if (all.length === 0) return;
        ev.preventDefault();
        all[step(all.indexOf(document.activeElement as HTMLElement), all.length, ev.key)]?.focus();
    }
    // from the button of an open menu, Arrow down goes to the first entry and Arrow up to the last
    function onButtonKey(ev: KeyboardEvent) {
        if (!open || (ev.key !== 'ArrowDown' && ev.key !== 'ArrowUp')) return;
        const all = items();
        if (all.length === 0) return;
        ev.preventDefault();
        all[ev.key === 'ArrowDown' ? 0 : all.length - 1]?.focus();
    }

    async function toggle() {
        open = !open;
        if (!open) return;
        const r = button?.getBoundingClientRect();
        popStyle = r && window.innerWidth <= 700 ? `position:fixed;left:8px;right:8px;top:${Math.round(r.bottom + 4)}px;` : '';
        try {
            const u = await api.get<{staged: {file: string; recovery_armed?: boolean} | null; container?: string; running?: {platform?: string}}>('/api/system/v1/system-update');
            container = !!u.container;
            kind = haltKind(u.running?.platform, u.container);
            platform = u.running?.platform ?? '';
            staged = u.staged ? u.staged.file || '' : null;
            armed = !!u.staged?.recovery_armed;
        } catch {
            /* the menu works with what it knew */
        }
    }

    // closes on Escape and on a click outside it; the listeners exist only while it is open
    $effect(() => {
        if (!open) return;
        const onKey = (ev: KeyboardEvent) => {
            if (ev.key !== 'Escape') return;
            open = false;
            button?.focus();
        };
        const onDown = (ev: MouseEvent) => {
            if (root && !root.contains(ev.target as Node)) open = false;
        };
        document.addEventListener('keydown', onKey);
        document.addEventListener('mousedown', onDown);
        return () => {
            document.removeEventListener('keydown', onKey);
            document.removeEventListener('mousedown', onDown);
        };
    });
    $effect(() => {
        void router.path;
        open = false;
    });

    // the address as the question opens: the network API for it, GET /https for whether the name
    // may follow (an answer that does not come counts as HSTS on, and so does HSTS that is off but
    // still sends max-age=0 - task 96: a browser that has not visited since still remembers it)
    async function resolveAddress() {
        const host = location.hostname;
        const [a, hsts] = await Promise.all([
            lookupBoxAddress(host, () => api.get<{network?: NetworkView}>('/api/system/v1/network')),
            isIPLiteral(host) ? Promise.resolve(null) : withTimeout(api.get<HTTPSView>('/api/system/v1/https').then((v) => hstsRemembered(v)), LOOKUP_TIMEOUT_MS, null),
        ]);
        address = a;
        nameURL = nameLink(a, host, hsts);
    }

    // why the link is the address: one sentence, or the warning when only the name was left
    function addressReason(a: BoxAddress): string {
        return a.kind === 'name'
            ? t('No address of the system could be read, so the link uses its name: the recovery system has no HTTPS, and a browser that remembers HSTS for this name refuses it there - open the system by its IP address then.')
            : t('The link uses the address the system has now and not its name: the recovery system has no HTTPS, and a browser that remembers HSTS for the name refuses it there.');
    }

    async function choose(action: Action) {
        open = false;
        if (action === 'recovery' && staged !== null) {
            refused = true;
            return;
        }
        const intoRecovery = action === 'recovery' || (action === 'reboot' && armed);
        if (intoRecovery) await resolveAddress();
        const a = address;
        const question =
            action === 'reboot'
                ? armed && a
                    ? {title: t('Reboot'), message: [t('Reboot now? A system update is set to install at this boot: it stays in the recovery system for a few minutes, and the progress of the installation is shown at {url}. The interfaces and every addon are unreachable until it is back.', {url: a.url}), addressReason(a)].join(' '), confirm: t('Reboot')}
                    : {title: t('Reboot'), message: t('Reboot this system now? The interfaces and every addon are unreachable until it is back, about a minute.'), confirm: t('Reboot')}
                : action === 'halt'
                  ? {title: t('Halt'), message: haltQuestion(kind), confirm: t('Halt')}
                  : {title: t('Reboot into the recovery system'), message: [t('Reboot into the recovery system now? It answers with a page of its own at {url}; choose normal boot there, or unplug the power and plug it in again, to leave it.', {url: a?.url ?? ''}), a ? addressReason(a) : '', t('The interfaces and every addon are unreachable meanwhile.')].filter(Boolean).join(' '), confirm: t('Reboot into the recovery system')};
        if (!(await ask({...question, danger: true}))) return;
        await perform(action, question.title);
    }

    // The halt question and the shut-down page say what starts the box again: a board without a
    // power button is unplugged and plugged in again, a Raspberry Pi 5 has its button, a virtual
    // machine and a container are started on their host. A product the menu does not know (or an
    // answer that did not come) gets the general text naming all of them.
    function haltQuestion(k: HaltKind): string {
        switch (k) {
            case 'board': return t('Shut this system down now? It does not come back by itself and has no power button: to start it again, unplug its power supply and plug it in again.');
            case 'board-button': return t('Shut this system down now? It does not come back by itself: to start it again, press the power button on its board, or unplug its power supply and plug it in again.');
            case 'vm': return t('Shut this virtual machine down now? It does not come back by itself: start it again on its host.');
            case 'container': return t('Stop this container now? It does not come back by itself: start it again on its host.');
            default: return t('Shut this system down now? It does not come back by itself: a Raspberry Pi or a CCU3 needs its power supply unplugged and plugged in again; a virtual machine or a container needs its host to start it.');
        }
    }
    function haltedText(k: HaltKind): string {
        switch (k) {
            case 'board': return t('It has no power button: unplug its power supply and plug it in again to start it.');
            case 'board-button': return t('To start it again, press the power button on its board, or unplug its power supply and plug it in again.');
            case 'vm': return t('The virtual machine stays off until it is started again on its host.');
            case 'container': return t('The container stays stopped until it is started again on its host.');
            default: return t('It stays off until it is powered on again: unplug its power supply and plug it in again, or start the virtual machine or the container on its host.');
        }
    }

    async function perform(action: Action, title: string) {
        // a plain reboot's countdown ends in the recovery hint, which names the box's address
        const [before] = await Promise.all([action === 'reboot' ? boxUptime() : Promise.resolve(-1), action === 'reboot' && !address ? resolveAddress() : null]);
        const path = action === 'reboot' ? '/api/system/v1/reboot' : action === 'halt' ? '/api/system/v1/halt' : '/api/system/v1/reboot/recovery';
        // the countdown is written before the request: the waiting page lighttpd shows during the
        // boot reads it and goes on with the same bar. A reboot with an update set is the install.
        const bootKind: BootKind = action === 'reboot' ? (armed ? 'update' : 'reboot') : action;
        const entry = await beginBoot(bootKind, () => api.get(`/api/system/v1/boot-expect?kind=${bootKind}`), platform);
        try {
            await api.post(path, {confirm: true});
        } catch (e) {
            if (e instanceof ApiError) {
                // refused: nothing happens to the box, the page stays
                removeEntry();
                if (e.code === 'update-staged') {
                    staged = staged ?? '';
                    refused = true;
                } else {
                    failure = {title, message: e.message};
                }
                return;
            }
            // no answer at all: the box went away with the request, which is what was asked for
        }
        boot = entry;
        phase = action === 'reboot' ? 'rebooting' : action === 'halt' ? 'halted' : 'recovery';
        if (action === 'reboot') {
            // back = occulited answers with a smaller uptime, or after the box was gone
            // (lib/bootbar.ts); the bar runs out, then the page reloads where it is
            watchBoot({entry, before, pollMs: POLL_MS, onUpdate: (e) => (boot = e), onBack: (e) => {
                boot = e;
                setTimeout(() => location.reload(), EASE_MS);
            }});
        } else if (action === 'halt') {
            // it may be unplugged once it stops answering
            watchBoot({entry, before: -1, pollMs: POLL_MS, onDown: () => (halted = true), onBack: () => {}});
        }
    }
</script>

<div class="ol-menu ol-power" bind:this={root}>
    <button type="button" class="ol-iconlink ol-powerbtn" class:active={open} bind:this={button} aria-haspopup="menu" aria-expanded={open} title={buttonLabel} aria-label={buttonLabel} onclick={toggle} onkeydown={onButtonKey}>
        <Icon name="power" size={18} />
    </button>
    {#if open}
        <!-- svelte-ignore a11y_interactive_supports_focus -->
        <div class="ol-menupop ol-powerpop" role="menu" style={popStyle} bind:this={pop} onkeydown={onMenuKey}>
            {#if offerLogout}
                <button type="button" role="menuitem" class="ol-menuitem ol-poweritem" data-action="logout" onclick={signOut}>
                    <span class="ol-powericon"><Icon name="leave" size={16} /></span>
                    <span class="ol-powertext"><span class="ol-powerlabel">{t('Logout')}</span>{#if auth.user}<span class="ol-powerhint">{t('Logged in as')} {auth.user}</span>{/if}</span>
                </button>
                <div class="ol-menusep ol-powersep" role="separator"></div>
            {/if}
            {#each entries as e (e.action)}
                <button type="button" role="menuitem" class="ol-menuitem ol-poweritem" data-action={e.action} onclick={() => choose(e.action)}>
                    <span class="ol-powericon"><Icon name={e.icon} size={16} /></span>
                    <span class="ol-powertext"><span class="ol-powerlabel">{e.label}</span><span class="ol-powerhint">{e.hint}</span></span>
                </button>
            {/each}
        </div>
    {/if}
</div>

<ModalDialog open={refused} title={t('Reboot into the recovery system')} onclose={() => (refused = false)}>
    <p class="ol-powermsg">{t('A system update is staged, and the recovery system would install it at this boot. So it is not started from here: install the update or discard it under System update on the Status page first.')}</p>
    {#if staged}<p class="ol-powermsg hmm-mono ol-muted">{staged}</p>{/if}
    {#snippet footer()}
        <a class="hmm-button" href="/" use:link onclick={() => (refused = false)}>{t('Status')}</a>
        <button class="hmm-button" type="button" onclick={() => (refused = false)}>{t('Close')}</button>
    {/snippet}
</ModalDialog>

<ModalDialog open={failure !== null} title={failure?.title ?? ''} onclose={() => (failure = null)}>
    <p class="ol-powermsg">{failure?.message ?? ''}</p>
    {#snippet footer()}
        <button class="hmm-button" type="button" onclick={() => (failure = null)}>{t('Close')}</button>
    {/snippet}
</ModalDialog>

{#if phase}
    <!-- the page is gone with the box: what happens now, in place of a page that no longer answers -->
    <div class="ol-powerstate" role="status" aria-live="polite" data-phase={phase}>
        <div class="ol-card ol-powerbox">
            <span class="ol-card-icon ol-powerbig"><Icon name={phase === 'halted' ? 'power' : phase === 'recovery' ? 'lifebuoy' : 'restart'} size={22} /></span>
            {#if phase === 'rebooting'}
                <h2>{t('Rebooting…')}</h2>
                {#if armed && address}
                    <p>{t('A system update installs at this boot.')} {t('The installation runs in the recovery system; its progress is shown at')} <a class="hmm-mono" href={address.url}>{address.url}</a></p>
                    <p class="ol-muted">{addressReason(address)}</p>
                    <p class="ol-muted">{t('This page comes back by itself when the installation is done.')}</p>
                {:else}
                    <p>{t('The system restarts. This page reloads by itself when the system answers again, usually after about a minute.')}</p>
                {/if}
                {#if boot}<BootBar entry={boot} url={address?.url ?? ''} hint={!armed} />{/if}
            {:else if phase === 'halted' && halted}
                <h2>{t('The system is shut down')}</h2>
                <p>{haltedText(kind)}</p>
            {:else if phase === 'halted'}
                <h2>{bootText('shuttingDown', i18n.language)}</h2>
                <p>{bootText('haltWait', i18n.language)}</p>
            {:else}
                <h2>{t('Rebooting into the recovery system…')}</h2>
                {#if address}
                    <p>{t('In about a minute the recovery system answers with a page of its own at')} <a class="hmm-mono" href={address.url}>{address.url}</a></p>
                    <p class="ol-muted">{addressReason(address)}</p>
                    {#if nameURL}<p class="ol-muted ol-powername">{t('Also by name, unless this browser still remembers HSTS for it:')} <a class="hmm-mono" href={nameURL}>{nameURL}</a></p>{/if}
                {/if}
                <p class="ol-muted">{t('This page comes back when the system boots normally again.')}</p>
            {/if}
        </div>
    </div>
{/if}

<style>
    /* a hairline and a wider gap before it: a click meant for Settings does not land here */
    .ol-power { margin-left: 4px; padding-left: 6px; border-left: 1px solid var(--hmm-border-muted); }
    .ol-powerbtn { border: 0; background: none; font: inherit; cursor: pointer; }
    .ol-powerbtn:focus-visible { outline: 1px solid var(--hmm-focus); }
    /* right-aligned under the button, so it never runs off the right edge */
    .ol-powerpop { left: auto; right: 0; min-width: 270px; max-width: min(92vw, 340px); }
    .ol-poweritem { align-items: flex-start; white-space: normal; padding: 8px; }
    .ol-powericon { display: flex; padding-top: 1px; color: var(--hmm-fg-muted); }
    .ol-powertext { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
    .ol-powerlabel { font-weight: 600; }
    .ol-powerhint { color: var(--hmm-fg-muted); font-size: var(--hmm-font-size-small); }
    .ol-powermsg { margin: 0 0 10px; overflow-wrap: anywhere; }
    .ol-powerstate { position: fixed; inset: 0; z-index: 200; display: flex; align-items: center; justify-content: center; padding: 16px; background: var(--hmm-bg); color: var(--hmm-fg); }
    .ol-powerbox { max-width: 520px; width: 100%; text-align: center; padding: 28px 24px; }
    .ol-powerbox h2 { margin: 12px 0 8px; font-size: 18px; text-transform: none; letter-spacing: 0; color: var(--hmm-fg); }
    .ol-powerbox p { margin: 6px 0; overflow-wrap: anywhere; }
    .ol-powerbig { width: 48px; height: 48px; }
</style>
