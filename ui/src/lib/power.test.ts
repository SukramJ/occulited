import {afterEach, describe, expect, it, vi} from 'vitest';
import {boxUptime, haltKind} from './power';

// the health route as a sequence of answers: a number is an uptime, null a failed request, 503 a refusal;
// the polls until the box is back are tested with watchBoot in bootwatch.test.ts
function healthAnswers(answers: (number | null | 503)[]) {
    let n = 0;
    const fetch = vi.fn(async () => {
        const a = answers[Math.min(n++, answers.length - 1)];
        if (a === null) throw new TypeError('Failed to fetch');
        if (a === 503) return new Response('', {status: 503});
        return new Response(JSON.stringify({uptime_s: a}), {status: 200});
    });
    vi.stubGlobal('fetch', fetch);
    return fetch;
}

describe('waiting for the box to come back', () => {
    afterEach(() => {
        vi.useRealTimers();
        vi.unstubAllGlobals();
    });

    it('the uptime, or -1 without an answer', async () => {
        healthAnswers([4711]);
        expect(await boxUptime()).toBe(4711);
        healthAnswers([null]);
        expect(await boxUptime()).toBe(-1);
        healthAnswers([503]);
        expect(await boxUptime()).toBe(-1);
    });
});

describe('what a halted box needs to start again', () => {
    it.each([
        // the lab's boxes: the Pi 4, the Charly (a CCU3-class board, PLATFORM=rpi3), the OVA
        ['rpi4', '', 'board'],
        ['rpi3', '', 'board'],
        ['ova', '', 'vm'],
        ['rpi5', '', 'board-button'],
        ['RPI2', '', 'board'],
        ['tinkerboard', '', 'board'],
        ['odroid-c4', '', 'board'],
        // a container, by its marker or by the product
        ['ova', 'lxc', 'container'],
        ['lxc', '', 'container'],
        ['oci', '', 'container'],
        ['rpi4', 'docker', 'container'],
        // nothing known: the general text
        ['generic-x86_64', '', 'unknown'],
        ['intelnuc', '', 'unknown'],
        ['', '', 'unknown'],
        [undefined, undefined, 'unknown'],
        ['rpi', '', 'board'],
        ['rpix', '', 'unknown'],
    ])('PLATFORM=%s, container %s: %s', (platform, container, want) => {
        expect(haltKind(platform, container)).toBe(want);
    });
});
