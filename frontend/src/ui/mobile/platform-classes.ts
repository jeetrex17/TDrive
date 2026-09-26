// Stamps the phone platform onto <html> so every mobile stylesheet can scope
// itself with `html.mobile ...` and leave desktop untouched. Called twice from
// main.ts: once at module load (so the `?mobile=` browser/harness override paints
// before mount) and once after the gateway hydrates (so real iOS/Android, whose
// platform is only known then, get the same classes). Both calls are idempotent.

import { isAndroidPlatform, isIOSPlatform, isMobilePlatform } from '../../api';

export function applyMobilePlatformClasses(): void {
    if (typeof document === 'undefined') return;
    if (!isMobilePlatform()) return;

    const root = document.documentElement;
    root.classList.add('mobile');
    // iOS and Android differ only in a few platform specifics (status bar,
    // no folder picker on Android); the shared shell keys off `.mobile`.
    if (isIOSPlatform()) root.classList.add('ios');
    else if (isAndroidPlatform()) root.classList.add('android');
}
