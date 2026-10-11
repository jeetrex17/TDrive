package com.wails.app;

/** Validates provider-reported key authorizations before preparing a prompt. */
final class BiometricVaultKeyPolicy {
    static boolean accepts(boolean authenticationRequired, int validitySeconds, int keyBits,
                           boolean biometricOnly, boolean enrollmentBound) {
        // AOSP SecretKeyFactorySpi reports auth-per-use as 0 when AUTH_TIMEOUT
        // is absent. Older public KeyInfo documentation describes -1 instead.
        // Positive time windows must never authorize vault-key recovery.
        return authenticationRequired && (validitySeconds == 0 || validitySeconds == -1) && keyBits == 256
                && biometricOnly && enrollmentBound;
    }
}
