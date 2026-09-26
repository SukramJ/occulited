// Task 81: the Status page's warnings as occulited evaluates them (GET /api/system/v1/warnings),
// and their words. occulited decides which warnings are active and keeps the silences - so a
// silence can end when its warning clears while nobody has the page open - and the sentences stay
// here, keyed by id and variant and filled from the params. The page shows the list in the order
// it came, errors first.
import type {StorageReason} from './api';
import {stallSentence, type StallListener} from './rpcstalls';

export type Severity = 'error' | 'warning';

export interface Silence {
    id: string;
    variant: string;
    by: string;
    at: string;
    until: string;
}

/** an addon a warning names */
export interface WarnAddon {
    id: string;
    name?: string;
    enabled?: boolean;
    /** addon-ownership: the first root-owned entry */
    path?: string;
}

export interface WarnDevice {
    name: string;
    kind: string;
    model?: string;
}

export interface Warning {
    id: string;
    variant: string;
    severity: Severity;
    params?: {addons?: WarnAddon[]; account?: string; at?: string; path?: string; days?: number; verdict?: string; reasons?: StorageReason[]; devices?: WarnDevice[]; mode?: string; reason?: string; adapter?: string; cause?: string; line?: string; families?: string[]; unreachable?: number; total?: number; port?: number; addresses?: string; sgtin?: string; address?: string; name?: string; detail?: string; label?: string; dir?: string; target?: string; share?: string; reconnecting?: boolean; interface?: string; kind?: string; since?: string; checked?: boolean; listeners?: StallListener[]; folder?: string; device?: string; running?: string; newest?: string; node?: string; drops_off?: boolean; hosts?: string[]; issuer?: string; store?: string; candidate?: boolean};
    href?: string;
    /** this administrator's own silence (D-64) */
    silenced?: Silence;
}

export interface WarningsView {
    warnings?: Warning[];
    /** the silence periods in days, for an administrator (D-65) */
    periods?: number[];
}

export type Translate = (key: string, params?: Record<string, string | number>) => string;

export interface Words {
    t: Translate;
    /** an addon as a person knows it: the name it declares, else the catalogue's, else the id (B-69) */
    addonName: (a: WarnAddon) => string;
    /** the storage verdict and the reasons behind it, as the panel says them */
    storage: (w: Warning) => string;
    /** a time as this browser shows it */
    when: (iso: string) => string;
}

/** the periods a silence may have when the answer carries none */
export const PERIODS = [1, 7, 90];

export function warningText(w: Warning, words: Words): string {
    const {t} = words;
    const p = w.params ?? {};
    const addons = p.addons ?? [];
    const names = (list: WarnAddon[]) => list.map(words.addonName).join(', ');
    switch (w.id) {
        case 'rega': {
            // "disabled" is the sentence's word, once (B-69)
            const off = addons.filter((a) => !a.enabled);
            const on = addons.filter((a) => a.enabled);
            return [
                off.length ? t('Disabled incompatible Addon: {list}.', {list: names(off)}) : '',
                on.length ? t('Installed, not usable without ReGa, and still enabled: {list}.', {list: names(on)}) : '',
            ].filter(Boolean).join(' ');
        }
        case 'arch': {
            const off = addons.filter((a) => !a.enabled);
            const on = addons.filter((a) => a.enabled);
            return [
                off.length ? t('Built for another architecture, and therefore disabled: {list}.', {list: names(off)}) : '',
                on.length ? t('Built for another architecture, and still enabled: {list}.', {list: names(on)}) : '',
                t('The repair is a release built for this system.'),
            ].filter(Boolean).join(' ');
        }
        case 'addon-payload':
            // task 146: a restore brought these addons back without their program files
            return t('Addons without their program files (a backup carries no .nobackup directory; after a restore): {list}. Reinstall them from the catalogue on the Addons page.', {list: names(addons)});
        case 'addon-ended':
            // B-158: an addon that keeps a program running, and none of it runs
            return t('Addons whose program has ended: {list}. The Addons page starts them again; the log says why they ended.', {list: names(addons)});
        case 'addon-failed':
            // task 248: an addon's unit is in systemd's failed state
            return t('Addons that failed: {list}. The Addons page starts them again; the log says why they failed.', {list: names(addons)});
        case 'meta':
            return t('The metadata store had to be recovered from its backup. The last change before the failure may be lost.');
        case 'trust-ca': {
            // openccu-lite task 231: a server presents an authority occulited's store lacks (strict)
            const hosts = (p.hosts ?? [w.variant]).join(', ');
            const base = t('{hosts}: the server presents a certificate from {issuer}, which the {store} trust store does not hold. The call fails until the authority is added.', {hosts, issuer: p.issuer ?? '', store: p.store ?? 'occulited'});
            return p.candidate ? `${base} ${t('The System store holds it: one click on the Trust stores page copies it.')}` : base;
        }
        case 'app-public':
            return t('Control is public: anyone who reaches the web port operates the house, as {account}, without a login.', {account: String(p.account ?? w.variant)});
        case 'unclean':
            return t('The system was not shut down cleanly before this boot ({when}). Power loss or a hard reset.', {when: p.at ? words.when(p.at) : ''});
        case 'backup-target':
            return t('No backup target: {path} cannot be read, so the nightly backup probably writes nothing.', {path: p.path ?? w.variant});
        case 'backup-userfs':
            return t('The nightly backup writes to {path}, which is on the system itself: it is lost with the system, and it fills the storage the system runs on.', {path: p.path ?? w.variant});
        case 'backup-unencrypted':
            return t('Backups are not encrypted: anyone who can read the USB stick or share reads every key of this system.');
        case 'backup-delivery': {
            // task 86: a target whose last test or backup failed, or that has had no backup for 26 hours
            const name = String(p.name ?? w.variant);
            const why: Record<string, string> = {
                unreachable: t('The backup target {name} cannot be reached.', {name}),
                'auth-failed': t('The backup target {name} refuses the login.', {name}),
                'host-key-changed': t("The backup target {name} shows another server key than the one confirmed; nothing is written there until you check it.", {name}),
                'host-key-unknown': t("The server key of the backup target {name} is not confirmed yet.", {name}),
                'read-only': t('The backup target {name} cannot be written.', {name}),
                full: t('The backup target {name} is full.', {name}),
                stale: t('The backup target {name} does not answer.', {name}),
                // task 161: the stick of a USB directory target is not plugged in; the backup went to the other targets
                'no-medium': t('The USB stick of the backup target {name} is not plugged in; the other targets got their copy.', {name}),
                'no-sftp': t('The server of the backup target {name} offers no SFTP.', {name}),
                'too-old': t('No successful backup to {name} for more than a day.', {name}),
                // B-247: on the system's own storage the backup keeps room for a system update
                'update-room': t('The backup to {name} was skipped: on the system itself it would leave too little room for a system update, even with the older backups removed. Use a USB stick or a share.', {name}),
            };
            return why[String(p.cause ?? '')] ?? t('The last backup to {name} failed.', {name});
        }
        case 'certificate':
            if (w.variant === 'renewal-failed') return t('The last certificate renewal failed; the current certificate stays in service.');
            if (w.variant === 'expired') return t('The certificate has expired.');
            return t('The certificate expires in {n} days.', {n: p.days ?? 0});
        case 'storage':
            return words.storage(w);
        case 'addon-ownership':
            // B-92: the files an update wrote as root, in an addon that starts as its own user
            return [
                ...addons.map((a) => t('Files of {name} belong to root, but the addon runs as its own user and may fail to start on them ({path}).', {name: words.addonName(a), path: a.path ?? ''})),
                t("An update installed past occulited leaves such files: install_addon over SSH, or an addon's own updater."),
            ].join(' ');
        case 'security-key':
            return t('BidCos-RF uses the default security key. It is publicly known, so AES-protected BidCos devices are not protected against someone with a radio nearby. Setting your own key re-keys the AES-capable devices; write it down, a backup restored on another system asks for it.');
        case 'hmip-adapter':
            // D-102: hmipserver stopped on a known fatal error; the unit waits for the next run of the radio stack
            // task 155 (D-106): the cause behind the one rejection line picks the text
            if (w.variant === 'adapter-exchange-rejected' && p.cause === 'unreachable') return t("HmIP-RF is down: the system could not reach eQ-3's key server, which has to move the HmIP network to the module {adapter}. Check the network connection and the DNS settings; the Interfaces page tries again.", {adapter: p.adapter ?? ''});
            if (w.variant === 'adapter-exchange-rejected' && p.cause === 'refused') return t("HmIP-RF is down: eQ-3's key server does not know the module {adapter}, so the HmIP network of this system cannot move to it. Put the previous module back, or start fresh with this module on the Interfaces page (every HmIP device is paired again).", {adapter: p.adapter ?? ''});
            if (w.variant === 'adapter-exchange-rejected') return t("HmIP-RF is down: eQ-3's key server rejected the adapter exchange to {adapter}. This system's HmIP devices belong to the adapter it was set up with, and the key server does not hand them to this one. Put the previous adapter back, or set HmIP up afresh (all HmIP devices have to be paired again).", {adapter: p.adapter ?? ''});
            return t('HmIP-RF is down: hmipserver stopped on a known error ({code}).', {code: w.variant});
        case 'rpc-stalled':
            // B-201: an interface process held by a callback listener that never answers
            return stallSentence({interface: p.interface ?? w.variant, kind: p.kind ?? 'delivery', checked: p.checked ?? false, stuck: p.listeners ?? []}, t);
        case 'addon-update': {
            // task 248: what the Addons page's check found (the catalogue, or the addon's own check)
            const list = (p.addons ?? []) as {name?: string; id?: string; available?: string}[];
            const names = list.map((a) => (a.available ? `${a.name ?? a.id} ${a.available}` : (a.name ?? a.id ?? ''))).join(', ');
            return list.length === 1 ? t('An update is available for {names}.', {names}) : t('Updates are available for {n} addons: {names}.', {n: String(list.length), names});
        }
        case 'radio-firmware':
            // task 137 (D-89): the boot never flashes; a newer file waits for the administrator
            if (p.drops_off) return t('The {device} runs firmware {running}; {newest} is on the system. Adapters below 0.967 are known to drop off the USB bus: flash it in the radio firmware section.', {device: p.device ?? '', running: p.running ?? '', newest: p.newest ?? ''});
            return t('A newer radio firmware is on the system for the {device}: it runs {running}, {newest} is available. Nothing is flashed on its own; the radio firmware section flashes it.', {device: p.device ?? '', running: p.running ?? '', newest: p.newest ?? ''});
        case 'radio-module-unusable':
            // task 137: an HmIP-RFUSB the detection found but could not read
            return t('The radio module {device} at {node} does not answer with a usable firmware: the system has no radio on it. Flash it in the radio firmware section to use it.', {device: p.device ?? '', node: p.node ?? w.variant});
        case 'hb-rf-eth':
            // task 218: a configured board that does not answer; the system retries in the background
            if (p.reconnecting) return t('The HB-RF-ETH at {address} lost its connection: the radio module on it is not available until it is back. The system reconnects it on its own.', {address: p.address ?? w.variant});
            return t('The HB-RF-ETH at {address} is not connected: the radio module on it is not available. The system tries again every 30 seconds.', {address: p.address ?? w.variant});
        case 'firewall': {
            const fams = p.families ?? w.variant.split(',');
            const which = fams.length > 1 ? t('IPv4 and IPv6') : fams[0] === 'ipv6' ? 'IPv6' : 'IPv4';
            // task 157: INPUT has no jump to the rules' chain - they are not loaded
            return t('The firewall rules are not loaded ({families}): only the policy decides, whatever the Firewall page lists. Applying the rules on the Firewall page loads them again, and so does a restart of the system.', {families: which});
        }
        case 'classic-rpc-open': {
            // task 173: classic RPC on without a login
            const which = w.variant === 'plain,tls' ? t('the plain and the TLS ports') : w.variant === 'tls' ? t('the TLS ports') : t('the plain ports');
            return t('Classic RPC is on without a login ({ports}): anyone the firewall lets in controls every device.', {ports: which});
        }
        case 'hmip-port-open':
            // B-89: hmipserver's HTTP port is not held on the loopback - the bind shim did not load
            return t("hmipserver's port {port} listens on {addresses}, not only on this system: the image's loopback shim did not take. The firewall still closes it.", {port: String(p.port ?? ''), addresses: p.addresses ?? ''});
        case 'hmip-key-declined':
            // task 201: the device's key in the system's key list is not the device's, so hmipserver
            // declines its pairing and the device never appears
            return t('Pairing {sgtin} was declined: the key stored for that device does not match it, so the system cannot let it in. Scan or type the key from its sticker again, apply it, and pair the device once more.', {sgtin: p.sgtin ?? ''});
        case 'hmip-local-key':
            // task 149: after the switch to local key mode most HmIP devices went silent
            return t("{n} of {total} HmIP devices have not answered since the switch to local key mode: the key was probably not the network's. The way back to eQ-3's key server is on the Interfaces page.", {n: p.unreachable ?? 0, total: p.total ?? 0});
        case 'journal-target': {
            // task 216: the USB stick that is ram-sync's target is not plugged in
            if (w.variant === 'usb')
                return t('The USB stick {label} for the journal\'s copies is not plugged in. The journal is in RAM until it is: a reboot or a power loss before then loses what is only there.', {label: p.label ?? ''});
            // task 228: the network share that is ram-sync's target could not be reached
            if (w.variant === 'share')
                return t('The network share {share} for the journal\'s copies could not be reached. The journal is in RAM until a copy reaches it: a reboot or a power loss before then loses what is only there. {reason}', {share: p.share ?? '', reason: p.reason ?? ''});
            // task 85: ram-sync or persistent could not be set up at boot; the reason is the boot script's
            const mode = p.mode ?? w.variant;
            const name = mode === 'ram-sync' ? t('RAM, copied to the userfs') : mode === 'persistent' ? t('Persistent on the userfs') : mode;
            return t('The journal could not be kept on the userfs ({mode}) and is in RAM until the next boot. The boot script said: {reason}', {mode: name, reason: p.reason ?? ''});
        }
        case 'store-target':
            // task 229: the USB stick the database's copy goes to
            if (w.variant === 'failed')
                return t('The last copy of the database to the USB stick {label} failed: {reason}. The history stays on the userfs; a new card would start without it.', {label: p.label ?? '', reason: p.reason ?? ''});
            return t('The USB stick {label} for the database\'s copy is not plugged in. The history stays on the userfs; a new card would start without it.', {label: p.label ?? ''});
        case 'journal-sync':
            if (p.target === 'share')
                return t('The last copy of the journal to the network share {share} failed ({when}): {reason}. Until a copy works again, a power loss loses everything since the last good copy, and the RAM limit can drop the oldest lines.', {
                    share: p.share ?? '',
                    when: p.at ? words.when(p.at) : '—',
                    reason: p.reason ?? '',
                });
            if (p.target === 'usb')
                return t('The last copy of the journal to the USB stick {label} failed ({when}): {reason}. Until a copy works again, a power loss loses everything since the last good copy, and the RAM limit can drop the oldest lines.', {
                    label: p.label ?? '',
                    when: p.at ? words.when(p.at) : '—',
                    reason: p.reason ?? '',
                });
            return t('The last copy of the journal to the userfs failed ({when}): {reason}. Until a copy works again, a power loss loses everything since the last good copy, and the RAM limit can drop the oldest lines.', {
                when: p.at ? words.when(p.at) : '—',
                reason: p.reason ?? '',
            });
        case 'legacy-session':
            // task 125: the addons that get the session's alias in their URLs (the CCU's ?sid= convention)
            return [
                addons.length === 1
                    ? t('{name} receives your session in its URL (?sid=, the legacy CCU convention).', {name: names(addons)})
                    : t('{n} addons receive your session in their URLs (?sid=, the legacy CCU convention): {list}.', {n: addons.length, list: names(addons)}),
                t('What is in the URL is an alias that only addon pages accept, not the session itself, and it ends with your session. The way out: update the addon once a release reads the session header, or switch the legacy session off for it on the Addons page.'),
            ].join(' ');
    }
    // a warning newer than this page: at least its name
    return `${w.id}: ${w.variant}`;
}

const LINK_LABELS: Record<string, string> = {
    rega: 'Addons',
    arch: 'Addons',
    meta: 'Control',
    'app-public': 'Settings',
    'trust-ca': 'Trust stores',
    unclean: 'Log',
    'backup-target': 'Backup',
    'backup-userfs': 'Backup',
    'backup-unencrypted': 'Set up encryption',
    'backup-delivery': 'Backup',
    certificate: 'Certificate',
    storage: 'Storage health',
    'addon-ownership': 'Services',
    'addon-ended': 'Addons',
    'addon-failed': 'Addons',
    'security-key': 'Set security key',
    'hmip-adapter': 'Interfaces',
    'hb-rf-eth': 'LAN devices',
    'radio-firmware': 'Radio firmware',
    'addon-update': 'Manage addons',
    'radio-module-unusable': 'Radio firmware',
    'rpc-stalled': 'Interfaces',
    firewall: 'Firewall',
    'hmip-local-key': 'Local key mode',
    'hmip-key-declined': 'Device keys',
    'journal-target': 'Journal settings',
    'journal-sync': 'Journal settings',
    'store-target': 'History settings',
    'legacy-session': 'Installed addons',
};

/** the button to where the matter is handled (task 53): the page's name, or what it does there */
export function warningLink(w: Warning, t: Translate): {href: string; label: string} | null {
    if (!w.href) return null;
    return {href: w.href, label: t(LINK_LABELS[w.id] ?? 'Open page')};
}

/** the warnings on show, and the ones this administrator silenced, each in the order they came */
export function splitSilenced(list: Warning[]): {shown: Warning[]; silenced: Warning[]} {
    return {shown: list.filter((w) => !w.silenced), silenced: list.filter((w) => !!w.silenced)};
}

/** DELETE's path: the variant is URL-escaped, a backup target's is a path itself */
export function silencePath(w: {id: string; variant: string}): string {
    return `/api/system/v1/warnings/silence/${encodeURIComponent(w.id)}/${encodeURIComponent(w.variant)}`;
}

export function silencedLine(n: number, t: Translate): string {
    return n === 1 ? t('1 silenced warning') : t('{n} silenced warnings', {n});
}

export function periodLabel(days: number, t: Translate): string {
    return days === 1 ? t('Silence for 1 day') : t('Silence for {n} days', {n: days});
}
