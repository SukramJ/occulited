<script lang="ts">
    /*
     * openccu-lite task 244 (the maintainer: "i just want a checkbox behind the 'search now' button
     * with label 'check daily'"): one control for the three update checks - device firmware, the
     * system's releases, the addon catalogue. The check button, and directly behind it on the same
     * line *Check daily*, bound to that check's own daily setting; the host a check calls is the
     * hint behind the ?. Nothing goes out unless the button is pressed or the box is ticked (tasks
     * 220, 237). The pair wraps as one unit on a narrow screen; the box goes below the button only
     * when the two do not fit on a line.
     */
    import {t} from './i18n.svelte';
    import Help from './Help.svelte';

    interface Props {
        /** the button's label, e.g. "Check now" */
        label: string;
        /** its label while the check runs */
        busyLabel?: string;
        busy?: boolean;
        disabled?: boolean;
        onCheck: () => void;
        /** the daily setting as the page read it */
        daily: boolean;
        /** switches it; the box shows the new state at once and the old one again if this throws */
        onDaily: (on: boolean) => unknown;
        /** the box is shown but cannot be changed (a user without the right) */
        dailyDisabled?: boolean;
        /** what a check calls, for the hint */
        host: string;
        /** which check it is, as data-check-daily (firmware, system-update, catalog) */
        name?: string;
    }
    let {label, busyLabel, busy = false, disabled = false, onCheck, daily, onDaily, dailyDisabled = false, host, name = ''}: Props = $props();
    let saving = $state(false);
    // the box's own state while a save runs; the page's value otherwise
    let shown = $state<boolean | null>(null);

    async function change(on: boolean) {
        shown = on;
        saving = true;
        try {
            await onDaily(on);
        } catch {
            shown = !on;
        } finally {
            saving = false;
            shown = null;
        }
    }
</script>

<span class="ol-checkdaily" data-check-daily={name}>
    <button type="button" class="hmm-button" onclick={onCheck} disabled={disabled || busy} data-check-now>{busy && busyLabel ? busyLabel : label}</button>
    <span class="ol-checkdaily-box">
        <label><input type="checkbox" checked={shown ?? daily} disabled={dailyDisabled || saving} onchange={(e) => void change((e.currentTarget as HTMLInputElement).checked)} data-daily /> {t('Check daily')}</label><Help>{t('A check calls {host}. Nothing is sent unless you press the button or Check daily is on.', {host})}</Help>
    </span>
</span>

<style>
    .ol-checkdaily { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; max-width: 100%; }
    .ol-checkdaily-box { display: inline-flex; align-items: center; white-space: nowrap; }
    .ol-checkdaily-box label { display: inline-flex; align-items: center; gap: 6px; }
</style>
