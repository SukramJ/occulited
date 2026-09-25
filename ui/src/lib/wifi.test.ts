import {describe, expect, it} from 'vitest';
import {band, bars, joinable} from './wifi';

describe('the Wi-Fi panel readings (task 89)', () => {
    it('names the band and the channel', () => {
        expect(band(2412)).toEqual({band: '2.4 GHz', channel: 1});
        expect(band(2437)).toEqual({band: '2.4 GHz', channel: 6});
        expect(band(2484)).toEqual({band: '2.4 GHz', channel: 14});
        expect(band(5240)).toEqual({band: '5 GHz', channel: 48});
        expect(band(5955)).toEqual({band: '6 GHz', channel: 1});
        expect(band(1000)).toBeNull();
    });
    it('turns dBm into bars', () => {
        expect([-38, -55, -60, -67, -70, -75, -80].map(bars)).toEqual([4, 4, 3, 3, 2, 2, 1]);
    });
    it('joins what WPA-PSK, SAE and open networks are, not enterprise or WEP', () => {
        expect(['open', 'wpa2', 'wpa3', 'wpa2-wpa3', 'enterprise', 'wep'].map(joinable)).toEqual([true, true, true, true, false, false]);
    });
});
