import {describe, expect, it} from 'vitest';
import {periodLabel, silencedLine, silencePath, splitSilenced, warningLink, warningText, type Warning, type Words} from './warnings';

const t = (key: string, params?: Record<string, string | number>) => {
    let s = key;
    for (const [k, v] of Object.entries(params ?? {})) s = s.replaceAll(`{${k}}`, String(v));
    return s;
};
const words: Words = {t, addonName: (a) => a.name || a.id, storage: (w) => `storage ${w.variant}`, when: (iso) => `at ${iso}`};
const w = (id: string, variant: string, params: Warning['params'] = {}, href = ''): Warning => ({id, variant, severity: 'error', params, href});

describe('the Status page warnings (task 81)', () => {
    it('names the addons that get the legacy session, one or several', () => {
        const one = warningText(w('legacy-session', 'mosquitto', {addons: [{id: 'mosquitto', name: 'Mosquitto', enabled: true}]}), words);
        expect(one).toContain('Mosquitto receives your session in its URL');
        expect(one).toContain('Addons page');
        const two = warningText(w('legacy-session', 'hmm,mosquitto', {addons: [{id: 'hmm', name: 'Homematic Manager', enabled: true}, {id: 'mosquitto', name: 'Mosquitto', enabled: true}]}), words);
        expect(two).toContain('2 addons receive your session in their URLs');
        expect(two).toContain('Homematic Manager, Mosquitto');
        expect(warningLink(w('legacy-session', 'mosquitto', {}, '/addons'), t)).toEqual({href: '/addons', label: 'Installed addons'});
    });

    it('names the addons and says disabled once', () => {
        const text = warningText(w('rega', 'email,hm-print', {addons: [{id: 'hm-print', name: 'Print', enabled: false}, {id: 'email', name: 'E-Mail', enabled: true}]}), words);
        expect(text).toBe('Disabled incompatible Addon: Print. Installed, not usable without ReGa, and still enabled: E-Mail.');
        expect(warningText(w('arch', 'cuxd', {addons: [{id: 'cuxd', enabled: false}]}), words)).toBe('Built for another architecture, and therefore disabled: cuxd. The repair is a release built for this system.');
    });

    it('says an HB-RF-ETH whose link is lost is reconnected, not retried (B-218)', () => {
        expect(warningText(w('hb-rf-eth', '192.0.2.209', {address: '192.0.2.209', reconnecting: true}), words)).toBe('The HB-RF-ETH at 192.0.2.209 lost its connection: the radio module on it is not available until it is back. The system reconnects it on its own.');
        expect(warningText(w('hb-rf-eth', '192.0.2.209', {address: '192.0.2.209', reconnecting: false}), words)).toContain('tries again every 30 seconds');
    });

    it('names a backup target and its cause (task 86)', () => {
        expect(warningText(w('backup-delivery', 'tabc:auth-failed', {name: 'NAS', cause: 'auth-failed'}), words)).toBe('The backup target NAS refuses the login.');
        expect(warningText(w('backup-delivery', 'directory:too-old', {name: 'USB', cause: 'too-old'}), words)).toBe('No successful backup to USB for more than a day.');
        expect(warningText(w('backup-delivery', 'tabc:last-failed', {name: 'NAS', cause: 'last-failed'}), words)).toBe('The last backup to NAS failed.');
        expect(warningLink(w('backup-delivery', 'tabc:full', {name: 'NAS', cause: 'full'}, '/backup#targets'), t)).toEqual({href: '/backup#targets', label: 'Backup'});
    });

    it('says the certificate variants apart', () => {
        expect(warningText(w('certificate', 'renewal-failed', {days: 40}), words)).toContain('renewal failed');
        expect(warningText(w('certificate', 'expired', {days: -1}), words)).toBe('The certificate has expired.');
        expect(warningText(w('certificate', 'expiring', {days: 9}), words)).toBe('The certificate expires in 9 days.');
    });

    it('fills the time, the path and the root-owned files', () => {
        expect(warningText(w('unclean', '1', {at: '2026-09-12T03:00:00Z'}), words)).toContain('(at 2026-09-12T03:00:00Z)');
        expect(warningText(w('backup-target', '/media/usb0/backup', {path: '/media/usb0/backup'}), words)).toContain('/media/usb0/backup cannot be read');
        const own = warningText(w('addon-ownership', 'hm2mqtt', {addons: [{id: 'hm2mqtt', path: '/usr/local/addons/hm2mqtt/var/pid'}]}), words);
        expect(own).toContain('Files of hm2mqtt belong to root');
        expect(own).toContain('(/usr/local/addons/hm2mqtt/var/pid)');
        expect(warningText(w('storage', 'watch'), words)).toBe('storage watch');
        expect(warningText(w('security-key', 'default'), words)).toMatch(/^BidCos-RF uses the default security key\./);
        // a warning this page does not know yet still says which it is
        expect(warningText(w('future', 'x'), words)).toBe('future: x');
    });

    it('labels the way to where it is handled', () => {
        expect(warningLink(w('security-key', 'default', {}, '/radio#security-key'), t)).toEqual({href: '/radio#security-key', label: 'Set security key'});
        expect(warningLink(w('storage', 'watch', {}, '#storage'), t)).toEqual({href: '#storage', label: 'Storage health'});
        expect(warningLink(w('meta', '1'), t)).toBeNull();
    });

    it('splits off the silenced ones and escapes the variant', () => {
        const silenced = {...w('backup-target', '/media/usb0/backup'), silenced: {id: 'backup-target', variant: '/media/usb0/backup', by: 'admin', at: '', until: ''}};
        const {shown, silenced: off} = splitSilenced([w('meta', '1'), silenced]);
        expect(shown.map((x) => x.id)).toEqual(['meta']);
        expect(off.map((x) => x.id)).toEqual(['backup-target']);
        expect(silencePath(silenced)).toBe('/api/system/v1/warnings/silence/backup-target/%2Fmedia%2Fusb0%2Fbackup');
        expect(silencePath({id: 'rega', variant: 'email,hm-print'})).toBe('/api/system/v1/warnings/silence/rega/email%2Chm-print');
    });

    it('counts and names the periods', () => {
        expect(silencedLine(1, t)).toBe('1 silenced warning');
        expect(silencedLine(2, t)).toBe('2 silenced warnings');
        expect(periodLabel(1, t)).toBe('Silence for 1 day');
        expect(periodLabel(90, t)).toBe('Silence for 90 days');
    });
});

describe('hmip-port-open (B-89)', () => {
    it('names the port and the addresses', () => {
        const text = warningText({id: 'hmip-port-open', variant: '39292', severity: 'warning', params: {port: 39292, addresses: '0.0.0.0, ::'}}, words);
        expect(text).toBe("hmipserver's port 39292 listens on 0.0.0.0, ::, not only on this system: the image's loopback shim did not take. The firewall still closes it.");
    });
});
