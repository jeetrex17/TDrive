package com.wails.app;

import android.content.Context;
import android.os.Build;
import android.os.Looper;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyInfo;
import android.security.keystore.KeyPermanentlyInvalidatedException;
import android.security.keystore.KeyProperties;
import android.util.AtomicFile;

import androidx.annotation.RequiresApi;
import androidx.biometric.BiometricManager;
import androidx.biometric.BiometricPrompt;
import androidx.core.content.ContextCompat;

import java.io.DataInputStream;
import java.io.File;
import java.io.FileNotFoundException;
import java.io.FileOutputStream;
import java.io.IOException;
import java.lang.ref.WeakReference;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.KeyStore;
import java.security.SecureRandom;
import java.util.Arrays;
import java.util.Enumeration;
import java.util.LinkedHashMap;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

import javax.crypto.AEADBadTagException;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.SecretKeyFactory;
import javax.crypto.spec.GCMParameterSpec;

/**
 * Dedicated native-only vault bridge. The OS authorizes the actual wrapping
 * operation with a CryptoObject; no key or password passes through JavaScript.
 * Ciphertext lives under noBackupFilesDir, excluded from backup and transfer by
 * Android itself. The non-exportable Keystore key requires a strong biometric
 * for each use and is invalidated when biometric enrollment changes.
 */
final class BiometricVault {
    private static final int OK = 0;
    private static final int UNAVAILABLE = 1;
    private static final int NOT_ENROLLED = 2;
    private static final int CANCELED = 3;
    private static final int INVALIDATED = 4;
    private static final int FAILED = 5;
    private static final String PREFIX = "tdrive.biometric.v1.";
    private static volatile BiometricVault current;
    // A cancellation can reach JNI before its worker registers the prompt.
    // Keep bounded tombstones so that request cannot later open a dialog.
    private static final LinkedHashMap<Long, Boolean> canceledRequests = new LinkedHashMap<>();
    private final WeakReference<MainActivity> activity;
    private final Context context;
    private final Object storeLock = new Object();
    private final Object requestLock = new Object();
    private Request active;
    private volatile boolean closed;
    private volatile boolean foreground;

    private static native void nativeInit();

    private BiometricVault(MainActivity host) {
        activity = new WeakReference<>(host);
        context = host.getApplicationContext();
    }

    static synchronized void initialize(MainActivity activity) {
        if (current != null) current.close();
        current = new BiometricVault(activity);
        nativeInit();
    }

    static synchronized void shutdown(MainActivity host) {
        BiometricVault vault = current;
        if (vault != null && vault.activity.get() == host) {
            vault.close();
            current = null;
        }
    }

    static void cancelForActivity(MainActivity host) {
        BiometricVault vault = current;
        if (vault != null && vault.activity.get() == host) {
            vault.foreground = false;
            vault.cancelActive(null);
        }
    }

    static void resumeForActivity(MainActivity host) {
        BiometricVault vault = current;
        if (vault != null && vault.activity.get() == host) vault.foreground = true;
    }

    private void close() {
        closed = true;
        cancelActive(null);
        activity.clear();
    }

    // Called on an attached Go worker, never on Android's main thread.
    static byte[] invoke(long id, int operation, String scope, String config, byte[] secret) {
        try {
            if (!BiometricVaultRecord.validID(scope, 64)
                    || !BiometricVaultRecord.validID(config, 64)) return error(FAILED);
            if (isCanceled(id)) return error(CANCELED);
            BiometricVault vault = current;
            if (vault == null || vault.closed || Looper.myLooper() == Looper.getMainLooper()) {
                return error(UNAVAILABLE);
            }
            if (operation == 1) return vault.status(scope, config);
            if (operation == 4) {
                vault.cancelActive(scope);
                synchronized (vault.storeLock) { vault.deleteScope(scope); }
                return error(OK);
            }
            if (operation != 2 && operation != 3) return error(FAILED);
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return error(UNAVAILABLE);
            if (operation == 2 && (secret == null || secret.length != 64)) return error(FAILED);
            return vault.authenticate(id, operation, scope, config, secret);
        } catch (GeneralSecurityException | IOException | RuntimeException failure) {
            // Native callers receive a stable code; exceptions never expose key
            // material or decrypted data through logs or WebView events.
            return error(FAILED);
        } finally {
            if (secret != null) Arrays.fill(secret, (byte) 0);
            synchronized (canceledRequests) { canceledRequests.remove(id); }
        }
    }

    static void cancel(long id) {
        synchronized (canceledRequests) {
            canceledRequests.put(id, true);
            if (canceledRequests.size() > 128) {
                canceledRequests.remove(canceledRequests.keySet().iterator().next());
            }
        }
        BiometricVault vault = current;
        if (vault == null) return;
        synchronized (vault.requestLock) {
            if (vault.active != null && vault.active.id == id) vault.active.cancel();
        }
    }

    private static boolean isCanceled(long id) {
        synchronized (canceledRequests) { return canceledRequests.containsKey(id); }
    }

    private void cancelActive(String scope) {
        synchronized (requestLock) {
            if (active != null && (scope == null || scope.equals(active.scope))) active.cancel();
        }
    }

    private int availability() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M || closed) return 1;
        int result = BiometricManager.from(context).canAuthenticate(
                BiometricManager.Authenticators.BIOMETRIC_STRONG);
        if (result == BiometricManager.BIOMETRIC_SUCCESS) return 0;
        if (result == BiometricManager.BIOMETRIC_ERROR_NONE_ENROLLED) return 2;
        if (result == BiometricManager.BIOMETRIC_ERROR_NO_HARDWARE
                || result == BiometricManager.BIOMETRIC_ERROR_UNSUPPORTED) return 1;
        return 3;
    }

    private byte[] status(String scope, String config) throws GeneralSecurityException, IOException {
        int reason = availability();
        boolean enrolled = false;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            synchronized (storeLock) {
                try {
                    BiometricVaultRecord record = read(scope);
                    if (record != null && config.equals(record.configHash())) {
                        enrolled = true;
                        SecretKey key = key(scope, record.keyID());
                        // Initializing an auth-per-use operation does not unlock
                        // it; it detects keys invalidated by enrollment changes.
                        cipher(Cipher.DECRYPT_MODE, key, record.iv());
                    } else if (record != null) {
                        enrolled = true;
                        reason = 4;
                    }
                } catch (KeyPermanentlyInvalidatedException | InvalidRecord invalidated) {
                    // Status is read-only: an old caller's config must not
                    // remove a newer valid enrollment with the same scope ID.
                    enrolled = true;
                    reason = 4;
                }
            }
        }
        return new byte[] {OK, (byte) (availability() == 0 ? 1 : 0),
                (byte) (enrolled ? 1 : 0), (byte) reason};
    }

    @RequiresApi(Build.VERSION_CODES.M)
    private byte[] authenticate(long id, int operation, String scope, String config, byte[] secret)
            throws GeneralSecurityException, IOException {
        if (availability() != 0 || !foreground) return error(UNAVAILABLE);
        MainActivity host = activity.get();
        if (host == null || host.isFinishing() || host.isDestroyed()) return error(UNAVAILABLE);
        Request request = new Request(id, scope, host, this);
        synchronized (requestLock) {
            if (closed || active != null) return error(UNAVAILABLE);
            active = request;
            if (isCanceled(id)) request.cancel();
        }
        String temporaryAlias = null;
        byte[] plaintext = null;
        try {
            BiometricVaultRecord previous;
            byte[] keyID = null;
            Cipher prepared;
            synchronized (storeLock) {
                if (request.canceled.get() || closed || !foreground) return error(CANCELED);
                previous = read(scope);
                if (operation == 2) {
                    keyID = new byte[16];
                    new SecureRandom().nextBytes(keyID);
                    temporaryAlias = alias(scope, BiometricVaultRecord.hex(keyID));
                    SecretKey key = createKey(temporaryAlias);
                    prepared = cipher(Cipher.ENCRYPT_MODE, key, null);
                } else {
                    if (previous == null) return error(NOT_ENROLLED);
                    if (!config.equals(previous.configHash())) return error(INVALIDATED);
                    prepared = cipher(Cipher.DECRYPT_MODE,
                            key(scope, previous.keyID()), previous.iv());
                }
                prepared.updateAAD((scope + ":" + config).getBytes(StandardCharsets.US_ASCII));
            }
            request.prompt(prepared, operation == 2);
            int code = request.await();
            if (code != OK) return error(code);
            synchronized (storeLock) {
                if (request.canceled.get() || closed || !foreground || current != this) return error(CANCELED);
                if (operation == 2) {
                    byte[] ciphertext = prepared.doFinal(secret);
                    BiometricVaultRecord record = new BiometricVaultRecord(
                            BiometricVaultRecord.unhex(config), keyID, prepared.getIV(), ciphertext);
                    write(scope, record);
                    if (request.canceled.get() || closed || !foreground) {
                        deleteScope(scope);
                        return error(CANCELED);
                    }
                    temporaryAlias = null; // Committed key now belongs to its record.
                    if (previous != null && !previous.keyID().equals(record.keyID())) {
                        keyStore().deleteEntry(alias(scope, previous.keyID()));
                    }
                    return error(OK);
                }
                BiometricVaultRecord latest = read(scope);
                if (latest == null || !latest.keyID().equals(previous.keyID())
                        || !latest.configHash().equals(config)) return error(INVALIDATED);
                plaintext = prepared.doFinal(previous.ciphertext());
                if (plaintext.length != 64) return error(INVALIDATED);
                byte[] result = new byte[65];
                System.arraycopy(plaintext, 0, result, 1, 64);
                if (request.canceled.get() || closed || !foreground) {
                    Arrays.fill(result, (byte) 0);
                    return error(CANCELED);
                }
                return result;
            }
        } catch (KeyPermanentlyInvalidatedException | InvalidRecord | AEADBadTagException invalidated) {
            synchronized (storeLock) { deleteScope(scope); }
            return error(INVALIDATED);
        } finally {
            if (plaintext != null) Arrays.fill(plaintext, (byte) 0);
            try {
                if (temporaryAlias != null) {
                    synchronized (storeLock) { keyStore().deleteEntry(temporaryAlias); }
                }
            } finally {
                request.cancel();
                synchronized (requestLock) { if (active == request) active = null; }
            }
        }
    }

    private KeyStore keyStore() throws GeneralSecurityException, IOException {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        return store;
    }

    @RequiresApi(Build.VERSION_CODES.M)
    private SecretKey createKey(String alias) throws GeneralSecurityException {
        KeyGenParameterSpec.Builder spec = new KeyGenParameterSpec.Builder(alias,
                KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setKeySize(256).setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true).setUserAuthenticationRequired(true);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            spec.setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG);
        } else {
            spec.setUserAuthenticationValidityDurationSeconds(-1);
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            spec.setInvalidatedByBiometricEnrollment(true);
        }
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(spec.build());
        return generator.generateKey();
    }

    @RequiresApi(Build.VERSION_CODES.M)
    private SecretKey key(String scope, String keyID) throws GeneralSecurityException, IOException {
        java.security.Key key = keyStore().getKey(alias(scope, keyID), null);
        if (!(key instanceof SecretKey)) throw new InvalidRecord();
        KeyInfo info = (KeyInfo) SecretKeyFactory.getInstance(KeyProperties.KEY_ALGORITHM_AES,
                "AndroidKeyStore").getKeySpec((SecretKey) key, KeyInfo.class);
        boolean biometricOnly = Build.VERSION.SDK_INT < Build.VERSION_CODES.R
                || info.getUserAuthenticationType() == KeyProperties.AUTH_BIOMETRIC_STRONG;
        boolean enrollmentBound = Build.VERSION.SDK_INT < Build.VERSION_CODES.N
                || info.isInvalidatedByBiometricEnrollment();
        if (!BiometricVaultKeyPolicy.accepts(info.isUserAuthenticationRequired(),
                info.getUserAuthenticationValidityDurationSeconds(), info.getKeySize(),
                biometricOnly, enrollmentBound)) {
            throw new InvalidRecord();
        }
        return (SecretKey) key;
    }

    private Cipher cipher(int mode, SecretKey key, byte[] iv) throws GeneralSecurityException {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        if (mode == Cipher.ENCRYPT_MODE) cipher.init(mode, key);
        else cipher.init(mode, key, new GCMParameterSpec(128, iv));
        return cipher;
    }

    private AtomicFile file(String scope) throws IOException {
        File directory = new File(context.getNoBackupFilesDir(), "biometric-vault");
        if (!directory.isDirectory() && !directory.mkdirs()) throw new IOException("Vault storage unavailable");
        return new AtomicFile(new File(directory, scope + ".vault"));
    }

    private BiometricVaultRecord read(String scope) throws IOException {
        AtomicFile file = file(scope);
        try (DataInputStream stream = new DataInputStream(file.openRead())) {
            byte[] bytes = new byte[BiometricVaultRecord.SIZE];
            stream.readFully(bytes);
            if (stream.read() != -1) throw new InvalidRecord();
            try { return BiometricVaultRecord.decode(bytes); }
            catch (IOException malformed) { throw new InvalidRecord(); }
        } catch (FileNotFoundException absent) {
            if (file.getBaseFile().exists()) throw absent;
            return null;
        } catch (java.io.EOFException truncated) {
            throw new InvalidRecord();
        }
    }

    private void write(String scope, BiometricVaultRecord record) throws IOException {
        AtomicFile file = file(scope);
        FileOutputStream output = null;
        try {
            output = file.startWrite();
            output.write(record.encode());
            output.getFD().sync();
            file.finishWrite(output);
            output = null;
            BiometricVaultRecord durable = read(scope);
            if (durable == null || !Arrays.equals(durable.encode(), record.encode())) {
                throw new IOException("Biometric vault enrollment was not saved");
            }
        } catch (IOException failure) {
            if (output != null) file.failWrite(output);
            throw failure;
        }
    }

    private void deleteScope(String scope) throws IOException, GeneralSecurityException {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            KeyStore store = keyStore();
            String prefix = PREFIX + scope + ".";
            Enumeration<String> names = store.aliases();
            while (names.hasMoreElements()) {
                String name = names.nextElement();
                if (name.startsWith(prefix)) {
                    store.deleteEntry(name);
                    if (store.containsAlias(name)) {
                        throw new GeneralSecurityException("Could not remove biometric vault protection");
                    }
                }
            }
        }
        AtomicFile record = file(scope);
        record.delete();
        BiometricVaultFiles.verifyDeletion(record.getBaseFile());
    }

    private static String alias(String scope, String keyID) { return PREFIX + scope + "." + keyID; }
    private static byte[] error(int code) { return new byte[] {(byte) code}; }
    private static final class InvalidRecord extends IOException { }

    private static final class Request {
        final long id;
        final String scope;
        final WeakReference<MainActivity> host;
        final BiometricVault owner;
        final AtomicBoolean canceled = new AtomicBoolean();
        final CountDownLatch done = new CountDownLatch(1);
        volatile int code = CANCELED;
        volatile BiometricPrompt biometric;

        Request(long id, String scope, MainActivity host, BiometricVault owner) {
            this.id = id;
            this.scope = scope;
            this.host = new WeakReference<>(host);
            this.owner = owner;
        }

        void prompt(Cipher cipher, boolean enroll) {
            MainActivity activity = host.get();
            if (activity == null) { cancel(); return; }
            activity.runOnUiThread(() -> {
                if (canceled.get() || owner.closed || !owner.foreground
                        || activity.isFinishing() || activity.isDestroyed()) { cancel(); return; }
                try {
                    biometric = new BiometricPrompt(activity, ContextCompat.getMainExecutor(activity),
                            new BiometricPrompt.AuthenticationCallback() {
                        @Override public void onAuthenticationSucceeded(BiometricPrompt.AuthenticationResult result) {
                            BiometricPrompt.CryptoObject object = result.getCryptoObject();
                            complete(object != null && object.getCipher() == cipher ? OK : FAILED);
                        }
                        @Override public void onAuthenticationError(int errorCode, CharSequence message) {
                            if (errorCode == BiometricPrompt.ERROR_CANCELED
                                    || errorCode == BiometricPrompt.ERROR_USER_CANCELED
                                    || errorCode == BiometricPrompt.ERROR_NEGATIVE_BUTTON) complete(CANCELED);
                            else complete(UNAVAILABLE);
                        }
                    });
                    BiometricPrompt.PromptInfo info = new BiometricPrompt.PromptInfo.Builder()
                            .setTitle(enroll ? "Enable biometric vault unlock" : "Unlock encrypted files")
                            .setSubtitle("Unlock TDrive for this session")
                            .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
                            .setNegativeButtonText("Use password")
                            .build();
                    biometric.authenticate(info, new BiometricPrompt.CryptoObject(cipher));
                    if (canceled.get()) biometric.cancelAuthentication();
                } catch (RuntimeException unavailable) { complete(UNAVAILABLE); }
            });
        }

        synchronized void complete(int result) {
            if (done.getCount() == 0 || canceled.get()) return;
            code = result;
            done.countDown();
        }

        int await() {
            try {
                if (!done.await(120, TimeUnit.SECONDS)) cancel();
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                cancel();
            }
            return canceled.get() ? CANCELED : code;
        }

        void cancel() {
            canceled.set(true);
            done.countDown();
            MainActivity activity = host.get();
            if (activity != null) activity.runOnUiThread(() -> {
                BiometricPrompt prompt = biometric;
                if (prompt != null) prompt.cancelAuthentication();
            });
        }
    }
}
