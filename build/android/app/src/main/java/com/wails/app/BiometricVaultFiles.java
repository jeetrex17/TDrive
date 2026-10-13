package com.wails.app;

import java.io.File;
import java.io.IOException;

/** Verifies AtomicFile's void delete result, including recovery sidecars. */
final class BiometricVaultFiles {
    static void verifyDeletion(File base) throws IOException {
        if (base.exists() || new File(base.getPath() + ".bak").exists()
                || new File(base.getPath() + ".new").exists()) {
            throw new IOException("Could not remove biometric vault enrollment");
        }
    }
}
