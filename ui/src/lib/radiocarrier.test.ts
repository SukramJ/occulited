import {describe, expect, it} from 'vitest';
import {carrierOf} from './radiocarrier';

describe('what carries a radio module (task 240)', () => {
    it('reads the device types the lab systems write', () => {
        expect(carrierOf('HB-RF-ETH@192.0.2.209')).toEqual({via: 'HB-RF-ETH@192.0.2.209', full: 'HB-RF-ETH@192.0.2.209'});
        expect(carrierOf('HB-RF-USB-TK@usb-0000:01:00.0-1.3')?.via).toBe('HB-RF-USB-TK@usb-0000:01:00.0-1.3');
        expect(carrierOf('HB-RF-USB-2@usb-0000:01:00.0-1.2')?.via).toBe('HB-RF-USB-2@usb-0000:01:00.0-1.2');
        expect(carrierOf('GPIO@3f201000.serial')).toEqual({via: 'GPIO', full: 'GPIO@3f201000.serial'});
        expect(carrierOf('eQ-3 HM-MOD-RPI-PCB@platform-3f201000.serial')?.via).toBe('GPIO');
    });

    it('has no Via for a stick that is the radio, or nothing', () => {
        expect(carrierOf('eQ-3 HmIP-RFUSB@usb-0000:01:00.0-1.3')).toBeNull();
        expect(carrierOf('USB')).toBeNull();
        expect(carrierOf('')).toBeNull();
        expect(carrierOf(undefined)).toBeNull();
    });
});
