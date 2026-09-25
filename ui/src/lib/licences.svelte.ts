// Task 179: the Licences page's data, kept for the session - the SBOM is parsed once, and the
// search survives a visit to a component's page and the way back.
import {api, ApiError} from './api';
import {parseSbom, type CdxBom, type Sbom} from './sbom';

export const licences = $state<{data: Sbom | null; error: string; missing: boolean; query: string}>({data: null, error: '', missing: false, query: ''});

let loading: Promise<void> | null = null;

export function loadLicences(): Promise<void> {
    if (licences.data || licences.missing) return Promise.resolve();
    loading ??= api
        .get<CdxBom>('/api/system/v1/sbom')
        .then((bom) => {
            licences.data = parseSbom(bom);
            licences.error = '';
        })
        .catch((e: unknown) => {
            if (e instanceof ApiError && e.status === 404) licences.missing = true;
            else licences.error = (e as Error).message;
        })
        .finally(() => {
            loading = null;
        });
    return loading;
}
