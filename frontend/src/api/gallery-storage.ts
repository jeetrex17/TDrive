import { ClearGalleryCache, GetGalleryStorage } from '../../bindings/TDrive/app';
import { invokeBackend } from './gateway';
import { asRecord } from './shared';

export interface GalleryStorage { cacheBytes: number; cacheLimit: number; catalogBytes: number }

function normalize(value: unknown): GalleryStorage {
    const raw = asRecord(value);
    const bytes = (key: string): number => {
        const value = Number(raw[key]);
        if (!Number.isSafeInteger(value) || value < 0) throw new Error('Invalid storage information');
        return value;
    };
    return { cacheBytes: bytes('cache_bytes'), cacheLimit: bytes('cache_limit'), catalogBytes: bytes('catalog_bytes') };
}

export async function getGalleryStorage(): Promise<GalleryStorage> {
    return normalize(await invokeBackend(GetGalleryStorage));
}
export async function clearGalleryCache(): Promise<GalleryStorage> {
    return normalize(await invokeBackend(ClearGalleryCache));
}
