import {describe, expect, it} from 'vitest';
import {listenerName, stallSentence, stuck, type StallListener} from './rpcstalls';

const t = (key: string, params?: Record<string, string | number>) => {
    let s = key;
    for (const [k, v] of Object.entries(params ?? {})) s = s.replaceAll(`{${k}}`, String(v));
    return s;
};
const frozen: StallListener = {address: '127.0.0.1:8199', connected: true, local: true, owner: 'addon-frozen', verdict: 'no-answer'};
const named: StallListener = {address: '127.0.0.1:2048', id: 'nr_x_HmIP-RF', url: 'http://127.0.0.1:2048', connected: true, local: true, verdict: 'no-answer'};
const good: StallListener = {address: '127.0.0.1:2047', id: 'nr_y', connected: true, local: true, verdict: 'answers'};

describe('an interface process held by a listener (B-201)', () => {
    it('names a listener by its id, its address and its user', () => {
        expect(listenerName(frozen, t)).toBe('127.0.0.1:8199, user addon-frozen');
        expect(listenerName(named, t)).toBe('nr_x_HmIP-RF (127.0.0.1:2048)');
    });

    it('picks the listeners that do not answer', () => {
        expect(stuck({listeners: [good, frozen, {...good, verdict: 'refused'}]})).toEqual([frozen]);
        const unlisted: StallListener = {address: '127.0.0.1:8195', connected: true, local: true, verdict: 'holds'};
        expect(stuck({listeners: [good, unlisted, {...unlisted, address: '127.0.0.1:1', verdict: 'unregistered'}]})).toEqual([unlisted]);
        expect(stuck({listeners: []})).toEqual([]);
    });

    it('says what the process no longer does, and who holds it', () => {
        expect(stallSentence({interface: 'HmIP-RF', kind: 'delivery', checked: true, stuck: [frozen]}, t)).toBe(
            'HmIP-RF delivers no events to any client. The client at 127.0.0.1:8199, user addon-frozen takes its calls and never answers, and that holds it for everybody. Ending that connection releases it.',
        );
        expect(stallSentence({interface: 'BidCos-RF', kind: 'calls', checked: true, stuck: [frozen, named]}, t)).toBe(
            'BidCos-RF answers no client any more. The clients at 127.0.0.1:8199, user addon-frozen; nr_x_HmIP-RF (127.0.0.1:2048) take its calls and never answer, and that holds it for everybody. Ending their connections releases it.',
        );
        expect(stallSentence({interface: 'HmIP-RF', kind: 'delivery', checked: false, stuck: []}, t)).toContain('looking for the client');
        expect(stallSentence({interface: 'HmIP-RF', kind: 'delivery', checked: true, stuck: []}, t)).toContain('every registered client answers');
    });
});

describe('a listener that does not take its events (task 234)', () => {
    const blocked: StallListener = {address: '127.0.0.1:8234', id: 't234hang', url: 'http://127.0.0.1:8234', connected: true, local: true, verdict: 'blocked', blocked_for: 181};
    const gone: StallListener = {address: '', id: 'gone', connected: false, local: false, verdict: 'blocked', blocked_for: 70};

    it('is one of the listeners that hold', () => {
        expect(stuck({listeners: [good, blocked]})).toEqual([blocked]);
    });

    it('is named by its id alone when it is on no list any more', () => {
        expect(listenerName(gone, t)).toBe('gone');
    });

    it('says the others get their events', () => {
        expect(stallSentence({interface: 'HmIP-RF', kind: 'listener', checked: true, stuck: [blocked]}, t)).toBe(
            'HmIP-RF: the client t234hang (127.0.0.1:8234) does not take its events. HmIP-RF keeps them back for it and writes a warning to its log every second; the other clients get theirs. Ending that connection releases it.',
        );
        expect(stallSentence({interface: 'HmIP-RF', kind: 'listener', checked: true, stuck: [blocked, named]}, t)).toContain('the clients t234hang (127.0.0.1:8234); nr_x_HmIP-RF (127.0.0.1:2048) do not take their events');
        expect(stallSentence({interface: 'HmIP-RF', kind: 'listener', checked: true, stuck: [gone]}, t)).toContain('cannot be ended here; a restart of the interface process');
    });
});
