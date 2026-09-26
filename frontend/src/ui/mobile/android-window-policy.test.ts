import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const manifest = readFileSync(
    new URL('../../../../build/android/app/src/main/AndroidManifest.xml', import.meta.url),
    'utf8',
);

describe('Android keyboard ownership', () => {
    it('keeps the activity fixed while the app moves only the focused sheet', () => {
        expect(manifest).toContain('android:windowSoftInputMode="adjustNothing"');
        expect(manifest).not.toContain('android:windowSoftInputMode="adjustResize"');
    });
});
