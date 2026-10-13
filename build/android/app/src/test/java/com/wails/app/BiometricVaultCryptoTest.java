package com.wails.app;

import static org.junit.Assert.*;

import java.nio.charset.StandardCharsets;
import java.security.AlgorithmParameters;
import java.security.GeneralSecurityException;
import java.security.InvalidAlgorithmParameterException;
import java.security.InvalidKeyException;
import java.security.Key;
import java.security.NoSuchAlgorithmException;
import java.security.Provider;
import java.security.SecureRandom;
import java.security.spec.AlgorithmParameterSpec;
import java.util.Collections;

import javax.crypto.AEADBadTagException;
import javax.crypto.BadPaddingException;
import javax.crypto.Cipher;
import javax.crypto.CipherSpi;
import javax.crypto.IllegalBlockSizeException;
import javax.crypto.NoSuchPaddingException;
import javax.crypto.ShortBufferException;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;

import org.junit.Test;

public final class BiometricVaultCryptoTest {
    private static final String SCOPE = "a".repeat(64);
    private static final String CONFIG = "b".repeat(64);
    private static final SecretKeySpec KEY = new SecretKeySpec(new byte[32], "AES");
    private static final byte[] IV = new byte[12];
    private static final byte[] SECRET = new byte[64];

    @Test public void enrollmentUsesTheNativeAuthenticatedCipherBeforeSupplyingAAD() throws Exception {
        AuthenticatedCipher fixture = new AuthenticatedCipher(Cipher.ENCRYPT_MODE, null);
        BiometricVaultCrypto operation = new BiometricVaultCrypto(fixture.cipher, SCOPE, CONFIG);
        assertSame("The prompt must authorize the initialized operation", fixture.cipher, operation.cipher());

        Cipher nativeCipher = fixture.authenticate(operation.cipher());
        byte[] ciphertext = operation.finish(nativeCipher, SECRET);

        assertEquals(80, ciphertext.length);
        Cipher verifier = Cipher.getInstance("AES/GCM/NoPadding");
        verifier.init(Cipher.DECRYPT_MODE, KEY, new GCMParameterSpec(128, fixture.cipher.getIV()));
        verifier.updateAAD((SCOPE + ":" + CONFIG).getBytes(StandardCharsets.US_ASCII));
        assertArrayEquals(SECRET, verifier.doFinal(ciphertext));
        assertEquals("Reinitializing would discard the operation's native authentication", 1, fixture.spi.initializations);
    }

    @Test public void unlockSuppliesAADOnlyAfterNativeAuthentication() throws Exception {
        byte[] ciphertext = referenceCiphertext(SCOPE, CONFIG);
        AuthenticatedCipher fixture = new AuthenticatedCipher(Cipher.DECRYPT_MODE, IV);
        BiometricVaultCrypto operation = new BiometricVaultCrypto(fixture.cipher, SCOPE, CONFIG);

        Cipher nativeCipher = fixture.authenticate(operation.cipher());
        assertArrayEquals(SECRET, operation.finish(nativeCipher, ciphertext));
        assertEquals(1, fixture.spi.initializations);
    }

    @Test public void rejectedOrCanceledAuthenticationDoesNotSupplyAADOrData() throws Exception {
        AuthenticatedCipher fixture = new AuthenticatedCipher(Cipher.ENCRYPT_MODE, null);
        BiometricVaultCrypto operation = new BiometricVaultCrypto(fixture.cipher, SCOPE, CONFIG);

        assertThrows(GeneralSecurityException.class, () -> operation.finish(null, SECRET));
        assertEquals("A canceled native result must not access the protected operation", 0, fixture.spi.aadUpdates);
        assertEquals(0, fixture.spi.finalizations);
    }

    @Test public void anotherNativeCryptoObjectCannotAuthorizeThisOperation() throws Exception {
        AuthenticatedCipher prepared = new AuthenticatedCipher(Cipher.ENCRYPT_MODE, null);
        AuthenticatedCipher other = new AuthenticatedCipher(Cipher.ENCRYPT_MODE, null);
        BiometricVaultCrypto operation = new BiometricVaultCrypto(prepared.cipher, SCOPE, CONFIG);

        Cipher otherNativeCipher = other.authenticate(other.cipher);
        assertThrows(GeneralSecurityException.class, () -> operation.finish(otherNativeCipher, SECRET));
        assertEquals(0, prepared.spi.aadUpdates);
        assertEquals(0, prepared.spi.finalizations);
    }

    @Test public void cipherIdentityAloneDoesNotBypassNativeAuthorization() throws Exception {
        AuthenticatedCipher fixture = new AuthenticatedCipher(Cipher.ENCRYPT_MODE, null);
        BiometricVaultCrypto operation = new BiometricVaultCrypto(fixture.cipher, SCOPE, CONFIG);

        // Only the native prompt can give this operation its authentication token.
        assertThrows(IllegalBlockSizeException.class, () -> operation.finish(fixture.cipher, SECRET));
        assertEquals(0, fixture.spi.finalizations);
    }

    @Test public void anotherAccountOrVaultConfigurationCannotUnwrapTheSecret() throws Exception {
        String[][] differentBindings = {
                {"c".repeat(64), CONFIG},
                {SCOPE, "d".repeat(64)}
        };
        for (String[] binding : differentBindings) {
            byte[] ciphertext = referenceCiphertext(binding[0], binding[1]);
            AuthenticatedCipher fixture = new AuthenticatedCipher(Cipher.DECRYPT_MODE, IV);
            BiometricVaultCrypto operation = new BiometricVaultCrypto(fixture.cipher, SCOPE, CONFIG);

            Cipher nativeCipher = fixture.authenticate(operation.cipher());
            assertThrows(AEADBadTagException.class, () -> operation.finish(nativeCipher, ciphertext));
        }
    }

    private static byte[] referenceCiphertext(String scope, String config) throws GeneralSecurityException {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.ENCRYPT_MODE, KEY, new GCMParameterSpec(128, IV));
        cipher.updateAAD((scope + ":" + config).getBytes(StandardCharsets.US_ASCII));
        return cipher.doFinal(SECRET);
    }

    private static final class AuthenticatedCipher {
        final AuthPerUseCipherSpi spi = new AuthPerUseCipherSpi();
        final Cipher cipher;

        AuthenticatedCipher(int mode, byte[] iv) throws GeneralSecurityException {
            Provider provider = new Provider("TestAuthPerUse", 1.0, "Operation-specific native authentication") {
                {
                    putService(new Provider.Service(this, "Cipher", "AES/GCM/NoPadding",
                            AuthPerUseCipherSpi.class.getName(), Collections.emptyList(), Collections.emptyMap()) {
                        @Override public Object newInstance(Object ignored) { return spi; }
                    });
                }
            };
            cipher = Cipher.getInstance("AES/GCM/NoPadding", provider);
            if (iv == null) cipher.init(mode, KEY);
            else cipher.init(mode, KEY, new GCMParameterSpec(128, iv));
        }

        Cipher authenticate(Cipher nativeCryptoObjectCipher) {
            assertSame(cipher, nativeCryptoObjectCipher);
            spi.authenticated = true;
            return nativeCryptoObjectCipher;
        }
    }

    /**
     * Models Android Keystore's auth-per-use boundary: pre-auth AAD failure is
     * cached, so a later successful biometric prompt cannot repair the cipher.
     * Successful operations use the JVM's independent AES/GCM implementation.
     */
    private static final class AuthPerUseCipherSpi extends CipherSpi {
        private final Cipher delegate;
        private boolean authenticated;
        private GeneralSecurityException cachedFailure;
        private int initializations;
        private int aadUpdates;
        private int finalizations;

        AuthPerUseCipherSpi() {
            try { delegate = Cipher.getInstance("AES/GCM/NoPadding"); }
            catch (GeneralSecurityException unavailable) { throw new AssertionError(unavailable); }
        }

        @Override protected void engineSetMode(String mode) throws NoSuchAlgorithmException {
            if (!"GCM".equals(mode)) throw new NoSuchAlgorithmException(mode);
        }

        @Override protected void engineSetPadding(String padding) throws NoSuchPaddingException {
            if (!"NoPadding".equals(padding)) throw new NoSuchPaddingException(padding);
        }

        @Override protected int engineGetBlockSize() { return delegate.getBlockSize(); }
        @Override protected int engineGetOutputSize(int length) { return delegate.getOutputSize(length); }
        @Override protected byte[] engineGetIV() { return delegate.getIV(); }
        @Override protected AlgorithmParameters engineGetParameters() { return delegate.getParameters(); }

        @Override protected void engineInit(int mode, Key key, SecureRandom random) throws InvalidKeyException {
            delegate.init(mode, key, random);
            initialized();
        }

        @Override protected void engineInit(int mode, Key key, AlgorithmParameterSpec parameters, SecureRandom random)
                throws InvalidKeyException, InvalidAlgorithmParameterException {
            delegate.init(mode, key, parameters, random);
            initialized();
        }

        @Override protected void engineInit(int mode, Key key, AlgorithmParameters parameters, SecureRandom random)
                throws InvalidKeyException, InvalidAlgorithmParameterException {
            delegate.init(mode, key, parameters, random);
            initialized();
        }

        private void initialized() {
            authenticated = false;
            cachedFailure = null;
            initializations++;
        }

        @Override protected void engineUpdateAAD(byte[] input, int offset, int length) {
            aadUpdates++;
            if (cachedFailure != null) return;
            if (!authenticated) {
                cachedFailure = new GeneralSecurityException("KEY_USER_NOT_AUTHENTICATED");
                return;
            }
            delegate.updateAAD(input, offset, length);
        }

        private void requireAuthentication() throws IllegalBlockSizeException {
            if (cachedFailure != null || !authenticated) {
                throw (IllegalBlockSizeException) new IllegalBlockSizeException()
                        .initCause(cachedFailure != null ? cachedFailure
                                : new GeneralSecurityException("KEY_USER_NOT_AUTHENTICATED"));
            }
        }

        @Override protected byte[] engineUpdate(byte[] input, int offset, int length) {
            if (!authenticated || cachedFailure != null) throw new IllegalStateException("Authentication required");
            return delegate.update(input, offset, length);
        }

        @Override protected int engineUpdate(byte[] input, int offset, int length, byte[] output, int outputOffset)
                throws ShortBufferException {
            if (!authenticated || cachedFailure != null) throw new IllegalStateException("Authentication required");
            return delegate.update(input, offset, length, output, outputOffset);
        }

        @Override protected byte[] engineDoFinal(byte[] input, int offset, int length)
                throws IllegalBlockSizeException, BadPaddingException {
            requireAuthentication();
            finalizations++;
            return delegate.doFinal(input, offset, length);
        }

        @Override protected int engineDoFinal(byte[] input, int offset, int length, byte[] output, int outputOffset)
                throws ShortBufferException, IllegalBlockSizeException, BadPaddingException {
            requireAuthentication();
            finalizations++;
            return delegate.doFinal(input, offset, length, output, outputOffset);
        }
    }
}
