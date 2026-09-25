// The files of a device firmware bundle the Firmware page opens in its viewer: the bundle's info
// file and any .txt, .md or .log - the same rule as the API's, which refuses everything else.
// Firmware images (.efw, .eq3, .hex, .bin, .gbl, and an access point's .zip, B-204) stay plain
// text in the list.
export function viewableBundleFile(name: string): boolean {
    if (name === 'info') return true;
    return /\.(txt|md|log)$/i.test(name);
}

/** The read route of one bundle file. */
export function bundleFilePath(typeCode: string, name: string): string {
    return `/api/system/v1/firmware/bundles/${encodeURIComponent(typeCode)}/files/${encodeURIComponent(name)}`;
}
