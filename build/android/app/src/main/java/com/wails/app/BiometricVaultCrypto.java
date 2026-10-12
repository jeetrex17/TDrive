package com.wails.app;

import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;

import javax.crypto.Cipher;

/** Prepared native operation and scoped AES-GCM completion. */
final class BiometricVaultCrypto {
    private final Cipher prepared;
    private final byte[] aad;

    BiometricVaultCrypto(Cipher prepared, String scope, String config) {
        this.prepared = prepared;
        aad = (scope + ":" + config).getBytes(StandardCharsets.US_ASCII);
    }

    Cipher cipher() { return prepared; }

    byte[] finish(Cipher authenticated, byte[] input) throws GeneralSecurityException {
        if (authenticated == null || authenticated != prepared) {
            throw new GeneralSecurityException("Native authentication did not authorize this operation");
        }
        // Android Keystore sends AAD to the protected operation immediately.
        // Supplying it before the native prompt succeeds caches an auth failure
        // that a later successful prompt cannot repair.
        prepared.updateAAD(aad);
        return prepared.doFinal(input);
    }
}
