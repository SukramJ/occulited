import {describe, expect, it} from 'vitest';
import {bundleFilePath, viewableBundleFile} from './bundlefiles';

describe('the files of a firmware bundle', () => {
    it('opens info and the text files, not the images', () => {
        for (const name of ['info', 'changelog.txt', 'README.MD', 'update.log']) expect(viewableBundleFile(name), name).toBe(true);
        for (const name of ['HmIPW-DRS8_update_V1_2_6_220928.efw', 'HMIP_HAP_update_V3_0_18_2023_09_29.zip', 'fw.eq3', 'fw.hex', 'fw.bin', 'fw.gbl', 'information', 'info.bak', 'txt']) expect(viewableBundleFile(name), name).toBe(false);
    });

    it('escapes the type code and the name into one path segment each', () => {
        expect(bundleFilePath('4107', 'changelog.txt')).toBe('/api/system/v1/firmware/bundles/4107/files/changelog.txt');
        expect(bundleFilePath('a b', '../x')).toBe('/api/system/v1/firmware/bundles/a%20b/files/..%2Fx');
    });
});
