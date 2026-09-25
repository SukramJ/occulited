// openccu-lite task 240 (the maintainer): a module panel shows the module on one line and what
// carries it on the next - "Device HM-MOD-RPI-PCB" and "Via HB-RF-ETH@192.0.2.209". The radio
// detection's device type is "<carrier>@<path>" (/var/hm_mode's HM_*_DEVTYPE); it is read here, once,
// for every page that shows a module. Seen on the lab systems:
//
//   HB-RF-ETH@192.0.2.209                 a module on the network board        Via HB-RF-ETH@192.0.2.209
//   HB-RF-USB-TK@usb-0000:01:00.0-1.3       a module on a USB carrier board      Via HB-RF-USB-TK@usb-…
//   GPIO@3f201000.serial                    the RPI-RF-MOD on the Pi's header    Via GPIO
//   eQ-3 HM-MOD-RPI-PCB@platform-….serial   a PCB on the header                  Via GPIO
//   eQ-3 HmIP-RFUSB@usb-0000:01:00.0-1.3    a USB stick that is the radio        (no Via line)
//   USB                                     the HM-CFG-USB-2, the radio itself   (no Via line)

export interface Carrier {
    /** what the Via line says */
    via: string;
    /** the whole device type, for the title */
    full: string;
}

/** What carries a module, or null when the module is its own device (a USB stick) or unknown. */
export function carrierOf(deviceType: string | undefined): Carrier | null {
    const full = (deviceType ?? '').trim();
    if (full === '' || full.toUpperCase() === 'USB') return null;
    const at = full.indexOf('@');
    const carrier = at < 0 ? full : full.slice(0, at);
    const path = at < 0 ? '' : full.slice(at + 1);
    if (carrier.toUpperCase() === 'GPIO' || path.startsWith('platform-')) return {via: 'GPIO', full};
    // the stick names itself: it is the radio, nothing carries it
    if (carrier.startsWith('eQ-3 ') && path.startsWith('usb-')) return null;
    return {via: full, full};
}
