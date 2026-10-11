package com.wails.app;

import static org.junit.Assert.*;

import org.junit.Test;

public final class BiometricVaultKeyPolicyTest {
    @Test public void acceptsAuthPerUseReportedByModernAndLegacyKeystore() {
        assertTrue(BiometricVaultKeyPolicy.accepts(true, 0, 256, true, true));
        assertTrue(BiometricVaultKeyPolicy.accepts(true, -1, 256, true, true));
    }

    @Test public void rejectsTimedAuthenticationAndMalformedTimeouts() {
        for (int seconds : new int[] {-2, 1, 30, 300}) {
            assertFalse("Accepted timeout " + seconds,
                    BiometricVaultKeyPolicy.accepts(true, seconds, 256, true, true));
        }
    }

    @Test public void requiresAuthenticationStrongBiometricsAndEnrollmentInvalidation() {
        assertFalse(BiometricVaultKeyPolicy.accepts(false, 0, 256, true, true));
        assertFalse(BiometricVaultKeyPolicy.accepts(true, 0, 128, true, true));
        assertFalse(BiometricVaultKeyPolicy.accepts(true, 0, 256, false, true));
        assertFalse(BiometricVaultKeyPolicy.accepts(true, 0, 256, true, false));
    }
}
