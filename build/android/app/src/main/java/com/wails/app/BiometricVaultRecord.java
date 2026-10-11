package com.wails.app;

import java.io.IOException;
import java.util.Arrays;

/** Fixed-size ciphertext envelope. It never holds an unwrapped vault key. */
final class BiometricVaultRecord {
    static final int SIZE = 144;
    private final byte[] config;
    private final byte[] keyID;
    private final byte[] iv;
    private final byte[] ciphertext;

    BiometricVaultRecord(byte[] config, byte[] keyID, byte[] iv, byte[] ciphertext) throws IOException {
        if (config.length != 32 || keyID.length != 16 || iv.length != 12 || ciphertext.length != 80) {
            throw new IOException("Invalid biometric vault record");
        }
        this.config = config.clone();
        this.keyID = keyID.clone();
        this.iv = iv.clone();
        this.ciphertext = ciphertext.clone();
    }

    static BiometricVaultRecord decode(byte[] bytes) throws IOException {
        if (bytes.length != SIZE || bytes[0] != 'T' || bytes[1] != 'B'
                || bytes[2] != 'V' || bytes[3] != '1') {
            throw new IOException("Invalid biometric vault record");
        }
        return new BiometricVaultRecord(Arrays.copyOfRange(bytes, 4, 36),
                Arrays.copyOfRange(bytes, 36, 52), Arrays.copyOfRange(bytes, 52, 64),
                Arrays.copyOfRange(bytes, 64, SIZE));
    }

    byte[] encode() {
        byte[] bytes = new byte[SIZE];
        bytes[0] = 'T'; bytes[1] = 'B'; bytes[2] = 'V'; bytes[3] = '1';
        System.arraycopy(config, 0, bytes, 4, 32);
        System.arraycopy(keyID, 0, bytes, 36, 16);
        System.arraycopy(iv, 0, bytes, 52, 12);
        System.arraycopy(ciphertext, 0, bytes, 64, 80);
        return bytes;
    }

    String configHash() { return hex(config); }
    String keyID() { return hex(keyID); }
    byte[] iv() { return iv.clone(); }
    byte[] ciphertext() { return ciphertext.clone(); }

    static boolean validID(String value, int length) {
        if (value == null || value.length() != length) return false;
        for (int i = 0; i < length; i++) {
            char c = value.charAt(i);
            if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) return false;
        }
        return true;
    }

    static byte[] unhex(String value) throws IOException {
        if (!validID(value, 64)) throw new IOException("Invalid vault configuration identity");
        byte[] result = new byte[32];
        for (int i = 0; i < result.length; i++) {
            result[i] = (byte) ((Character.digit(value.charAt(i * 2), 16) << 4)
                    | Character.digit(value.charAt(i * 2 + 1), 16));
        }
        return result;
    }

    static String hex(byte[] value) {
        char[] chars = new char[value.length * 2];
        char[] alphabet = "0123456789abcdef".toCharArray();
        for (int i = 0; i < value.length; i++) {
            chars[i * 2] = alphabet[(value[i] & 0xff) >>> 4];
            chars[i * 2 + 1] = alphabet[value[i] & 15];
        }
        return new String(chars);
    }
}
