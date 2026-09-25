import {describe, expect, it} from 'vitest';
import {calendarOf, defaultForm, formFromFiles, goDuration, runTime, runTimeFull, serviceText, TIMER_NAME, timeLeft, timerText, type TimerForm} from './timers';

// B-68: the Timers table's runs, localized, and the countdown the box sends
describe('a timer run in the table', () => {
    // local clock components, so the expectations hold in any time zone the tests run in
    const now = new Date(2026, 8, 11, 13, 1, 28);
    const at = (y: number, mo: number, d: number, h: number, mi: number, s = 0) => new Date(y, mo - 1, d, h, mi, s).toISOString();
    it.each([
        ['de', at(2026, 9, 11, 15, 4, 29), '11.09., 15:04'],
        ['en', at(2026, 9, 11, 15, 4, 29), '11/09, 15:04'],
        // another year carries the year
        ['de', at(2025, 12, 31, 23, 59), '31.12.2025, 23:59'],
        ['en', at(2027, 1, 2, 0, 5), '02/01/2027, 00:05'],
    ])('%s: %s is %s', (lang, iso, text) => {
        expect(runTime(iso, lang, now)).toBe(text);
    });
    it('is empty without a run, and passes on what it cannot read', () => {
        expect(runTime(undefined, 'de', now)).toBe('');
        expect(runTime('soon', 'en', now)).toBe('soon');
        expect(runTimeFull(undefined, 'en')).toBe('');
        expect(runTimeFull(at(2026, 9, 11, 15, 4, 29), 'de')).toBe('11.09.2026, 15:04:29');
    });
    it.each([
        ['2h3m1s', 7381],
        ['45m0s', 2700],
        ['1h0m0s', 3600],
        ['3.5s', 3],
        ['26h0m0s', 93600],
        ['0s', 0],
        ['1h', 3600],
        ['2h 20min', undefined],
        ['150ms', undefined],
        ['', undefined],
        [undefined, undefined],
    ])('reads Go duration %s as %s seconds', (v, s) => {
        expect(goDuration(v)).toBe(s);
    });
    it('counts down with the box\'s left, else from next against the browser clock', () => {
        const ms = now.getTime();
        expect(timeLeft('2h3m1s', at(2026, 9, 11, 20, 0), ms)).toBe(7381);
        expect(timeLeft('6h', undefined, ms)).toBe(21600);
        expect(timeLeft('2h 20min', at(2026, 9, 11, 14, 1, 28), ms)).toBe(3600);
        expect(timeLeft(undefined, at(2026, 9, 11, 12, 0), ms)).toBeUndefined();
        expect(timeLeft(undefined, undefined, ms)).toBeUndefined();
    });
});

// task 50: the new-timer form and the two files it stands for
const form = (over: Partial<TimerForm>): TimerForm => ({...defaultForm(), name: 'test', command: 'logger hello', ...over});

describe('the files of an own timer', () => {
    it('daily at 03:00 running logger hello - the acceptance example', () => {
        const f = form({});
        expect(timerText(f)).toBe('[Unit]\nDescription=test\n\n[Timer]\nOnCalendar=*-*-* 03:00:00\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n');
        expect(serviceText(f)).toBe('[Unit]\nDescription=test\n\n[Service]\nType=oneshot\nExecStart=logger hello\n');
    });
    it('turns each preset into its expression', () => {
        expect(calendarOf(form({schedule: 'hourly'}))).toBe('hourly');
        expect(calendarOf(form({schedule: 'daily', time: '7:05'}))).toBe('*-*-* 07:05:00');
        expect(calendarOf(form({schedule: 'weekly', weekday: 'Sat', time: '22:30'}))).toBe('Sat *-*-* 22:30:00');
        expect(calendarOf(form({schedule: 'custom', calendar: ' Mon..Fri *-*-* 07:30 '}))).toBe('Mon..Fri *-*-* 07:30');
        expect(calendarOf(form({schedule: 'boot'}))).toBe('');
        // a cleared time input is midnight, not an expression systemd refuses
        expect(calendarOf(form({schedule: 'daily', time: ''}))).toBe('*-*-* 00:00:00');
    });
    it('writes a boot timer without Persistent=, the random delay and a user other than root', () => {
        const t = timerText(form({schedule: 'boot', bootDelay: '10min', persistent: true, randomDelay: '30s'}));
        expect(t).toContain('OnBootSec=10min\nRandomizedDelaySec=30s\n');
        expect(t).not.toContain('Persistent');
        expect(t).not.toContain('OnCalendar');
        expect(serviceText(form({user: 'nobody'}))).toContain('ExecStart=logger hello\nUser=nobody\n');
        expect(serviceText(form({user: 'root'}))).not.toContain('User=');
    });
    it.each<Partial<TimerForm>>([
        {schedule: 'hourly', persistent: false},
        {schedule: 'daily', time: '03:00'},
        {schedule: 'weekly', weekday: 'Sun', time: '04:15', randomDelay: '5min'},
        {schedule: 'boot', bootDelay: '2min', persistent: false},
        {schedule: 'custom', calendar: '*-*-01 06:00:00', user: 'nobody'},
    ])('reads the form back out of its own files: %o', (over) => {
        const f = form(over);
        expect(formFromFiles('test', timerText(f), serviceText(f))).toEqual(f);
    });
    it('reads a file the form did not write as far as it can', () => {
        const f = formFromFiles('x', '[Timer]\nOnUnitActiveSec=1h\n', '[Service]\nExecStart=/bin/true\n');
        expect(f.schedule).toBe('custom');
        expect(f.calendar).toBe('');
        expect(f.command).toBe('/bin/true');
    });
    it('knows the API\'s name rule', () => {
        for (const ok of ['a', 'backup-share', 'x_1', 'A'.repeat(32)]) expect(TIMER_NAME.test(ok), ok).toBe(true);
        for (const bad of ['', '-a', '_a', 'a b', 'a.timer', 'ä', 'A'.repeat(33)]) expect(TIMER_NAME.test(bad), bad).toBe(false);
    });
});
