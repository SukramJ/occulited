// openccu-lite task 220: the last find of eQ-3's LAN devices, shared by the LAN devices section
// (which runs it when the user presses Search) and the access point list (which shows each access
// point's address from it).
import {api} from './api';

export interface LANAddresses { ip: string; gateway: string; netmask: string; dns1: string; dns2: string }
export interface LANConfig extends LANAddresses { dhcp: boolean; auto_ip: boolean; crypt: number; name_max?: number; name: string }
export interface LANDevice {
    type: string;
    serial: string;
    version: string;
    protocol_version: number;
    ip: string;
    services?: {protocol: number; port: number}[];
    runtime?: LANAddresses;
    config?: LANConfig;
    kind: 'gateway' | 'access-point' | 'ccu' | 'other';
    writable: boolean;
    password?: 'configured' | 'sticker';
    configured?: 'rf' | 'wired';
    name?: string;
    paired?: boolean;
    same_subnet: boolean;
    link?: string;
}
/** scanned: when the last search ran; absent before the first (task 237) */
export interface LANScan { scanned?: string; devices: LANDevice[]; error?: string }

export const lan = $state<{scan: LANScan | null; busy: boolean; error: string}>({scan: null, busy: false, error: ''});

/**
 * task 237 (the maintainer): nothing goes out to the network until the user presses Search. load
 * reads the last search's result the system keeps (GET, sends nothing); search runs the eQ-3
 * discovery broadcast (POST).
 */
export async function loadLAN(): Promise<void> {
    await askLAN(() => api.get<LANScan>('/api/system/v1/radio/lan-devices'));
}

export async function searchLAN(): Promise<void> {
    await askLAN(() => api.post<LANScan>('/api/system/v1/radio/lan-devices/search', {}));
}

async function askLAN(call: () => Promise<LANScan>): Promise<void> {
    lan.busy = true;
    try {
        lan.scan = await call();
        lan.error = '';
    } catch (e) {
        lan.error = (e as Error).message;
    } finally {
        lan.busy = false;
    }
}

/** The address a device runs with: its own `n` answer, else the one its answer came from. */
export function runningIP(d: LANDevice): string {
    return d.runtime && d.runtime.ip !== '0.0.0.0' ? d.runtime.ip : d.ip;
}

/** The found device with that serial (an access point's SGTIN), case aside. */
export function foundBySerial(serial: string | undefined): LANDevice | undefined {
    if (!serial) return undefined;
    return lan.scan?.devices.find((d) => d.serial.toUpperCase() === serial.toUpperCase());
}
