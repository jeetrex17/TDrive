package com.wails.app;

import static org.junit.Assert.*;

import java.io.IOException;
import java.util.Arrays;
import org.junit.Test;

public final class BiometricVaultRecordTest {
    @Test public void decodesIndependentRecordAndOwnsBuffers() throws Exception {
        byte[] encoded = new byte[144];
        encoded[0] = 'T'; encoded[1] = 'B'; encoded[2] = 'V'; encoded[3] = '1';
        Arrays.fill(encoded, 4, 36, (byte) 0x11);
        Arrays.fill(encoded, 36, 52, (byte) 0x22);
        Arrays.fill(encoded, 52, 64, (byte) 0x33);
        Arrays.fill(encoded, 64, 144, (byte) 0x44);
        BiometricVaultRecord record = BiometricVaultRecord.decode(encoded);
        encoded[4] = 0;
        assertEquals("1111111111111111111111111111111111111111111111111111111111111111", record.configHash());
        assertEquals("22222222222222222222222222222222", record.keyID());
        assertEquals(12, record.iv().length);
        assertEquals(80, record.ciphertext().length);
        byte[] iv = record.iv();
        iv[0] = 0;
        assertEquals(0x33, record.iv()[0]);
    }

    @Test public void rejectsTruncationTrailingBytesAndUnknownVersion() throws Exception {
        byte[] valid = new byte[144];
        valid[0] = 'T'; valid[1] = 'B'; valid[2] = 'V'; valid[3] = '1';
        for (int length : new int[] {0, 4, 143, 145, 10000}) {
            try {
                BiometricVaultRecord.decode(Arrays.copyOf(valid, length));
                fail("Accepted record of length " + length);
            } catch (IOException expected) { }
        }
        valid[3] = '2';
        try {
            BiometricVaultRecord.decode(valid);
            fail("Accepted an unknown record version");
        } catch (IOException expected) { }
    }

    @Test public void encodesOnlyExactCryptoLengths() throws Exception {
        byte[] config = new byte[32];
        byte[] keyID = new byte[16];
        byte[] iv = new byte[12];
        byte[] ciphertext = new byte[80];
        config[0] = 0x12; keyID[0] = 0x34; iv[0] = 0x56; ciphertext[0] = 0x78;
        BiometricVaultRecord record = new BiometricVaultRecord(config, keyID, iv, ciphertext);
        config[0] = 0;
        byte[] encoded = record.encode();
        assertEquals(144, encoded.length);
        assertEquals('T', encoded[0]);
        assertEquals(0x12, encoded[4]);
        assertEquals(0x34, encoded[36]);
        assertEquals(0x56, encoded[52]);
        assertEquals(0x78, encoded[64]);
        try {
            new BiometricVaultRecord(new byte[31], keyID, iv, ciphertext);
            fail("Accepted a malformed configuration digest");
        } catch (IOException expected) { }
        try {
            new BiometricVaultRecord(new byte[32], keyID, new byte[16], ciphertext);
            fail("Accepted an unexpected GCM nonce length");
        } catch (IOException expected) { }
        try {
            new BiometricVaultRecord(new byte[32], keyID, iv, new byte[64]);
            fail("Accepted ciphertext missing its authentication tag");
        } catch (IOException expected) { }
    }

    @Test public void rejectsPathLikeAndNonCanonicalIDs() throws Exception {
        assertTrue(BiometricVaultRecord.validID("a".repeat(64), 64));
        assertFalse(BiometricVaultRecord.validID("A".repeat(64), 64));
        assertFalse(BiometricVaultRecord.validID("../" + "a".repeat(61), 64));
        assertFalse(BiometricVaultRecord.validID("a".repeat(63), 64));
        assertFalse(BiometricVaultRecord.validID(null, 64));
    }

    @Test public void configurationDigestUsesBytesAndRejectsInvalidHex() throws Exception {
        String hash = "0123456789abcdef".repeat(4);
        byte[] digest = BiometricVaultRecord.unhex(hash);
        assertEquals(32, digest.length);
        assertEquals(0x01, digest[0]);
        assertEquals((byte) 0xef, digest[7]);
        assertEquals(hash, BiometricVaultRecord.hex(digest));
        for (String invalid : new String[] {"a".repeat(63), "g".repeat(64), "A".repeat(64)}) {
            try {
                BiometricVaultRecord.unhex(invalid);
                fail("Accepted an invalid digest");
            } catch (IOException expected) { }
        }
    }
}
