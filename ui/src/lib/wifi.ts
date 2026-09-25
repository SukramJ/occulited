// Task 89: the Network page's Wi-Fi panel - the API's shapes and the small readings the panel makes.

export interface WiFiSettings {
    enabled: boolean;
    iface: string;
    country: string;
    mode: 'dhcp' | 'static';
    address?: string;
    netmask?: string;
    gateway?: string;
    dns?: string[];
    preferred: 'eth' | 'wlan';
}
export interface WiFiStatus { state: string; ssid?: string; bssid?: string; freq?: number; key_mgmt?: string; ip?: string }
export interface WiFiSignal { rssi: number; link_speed: number; freq: number }
export interface WiFiNetwork { ssid: string; security: string; hidden: boolean; priority: number }
export interface WiFiChip { iface: string; kind: 'onboard' | 'usb'; present: boolean }
export type WiFiState = 'no-chip' | 'off' | 'starting' | 'not-configured' | 'connecting' | 'connected' | 'disconnected';
export interface WiFiView {
    chips: WiFiChip[];
    settings: WiFiSettings;
    state: WiFiState;
    status?: WiFiStatus;
    signal?: WiFiSignal;
    addresses: string[];
    networks: WiFiNetwork[];
    confirm?: string;
    setup_error?: string;
}
export interface ScanResult { ssid: string; bssid: string; freq: number; signal: number; security: string; hidden?: boolean }

/** 2.4, 5 or 6 GHz and the channel, from the frequency in MHz */
export function band(freq: number): {band: string; channel: number} | null {
    if (freq >= 2412 && freq <= 2472) return {band: '2.4 GHz', channel: (freq - 2407) / 5};
    if (freq === 2484) return {band: '2.4 GHz', channel: 14};
    if (freq >= 5160 && freq <= 5885) return {band: '5 GHz', channel: (freq - 5000) / 5};
    if (freq >= 5955 && freq <= 7115) return {band: '6 GHz', channel: (freq - 5950) / 5};
    return null;
}

/** 1 to 4 bars from dBm: -55 and better 4, -67 3, -75 2, below 1 */
export function bars(dbm: number): number {
    if (dbm >= -55) return 4;
    if (dbm >= -67) return 3;
    if (dbm >= -75) return 2;
    return 1;
}

/** whether the page can connect to a network of this security (not enterprise, not WEP) */
export function joinable(security: string): boolean {
    return security === 'open' || security === 'wpa2' || security === 'wpa3' || security === 'wpa2-wpa3';
}

/** the regulatory domains offered first: the countries a CCU is sold in, then the rest by code */
export const COUNTRIES = ['DE', 'AT', 'CH', 'NL', 'BE', 'LU', 'FR', 'IT', 'ES', 'PL', 'CZ', 'DK', 'SE', 'NO', 'FI', 'GB', 'IE', 'US'];
