package com.wails.app;

import static org.junit.Assert.*;

import java.io.File;
import java.io.IOException;
import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;

public final class BiometricVaultFilesTest {
    @Rule public final TemporaryFolder temporary = new TemporaryFolder();

    @Test public void acceptsAlreadyRemovedEnrollment() throws Exception {
        BiometricVaultFiles.verifyDeletion(new File(temporary.getRoot(), "scope.vault"));
    }

    @Test public void rejectsAnySurvivingAtomicEnrollmentFile() throws Exception {
        for (String suffix : new String[] {"", ".bak", ".new"}) {
            File directory = temporary.newFolder();
            File base = new File(directory, "scope.vault");
            File surviving = new File(base.getPath() + suffix);
            assertTrue(surviving.createNewFile());
            try {
                BiometricVaultFiles.verifyDeletion(base);
                fail("Accepted surviving enrollment file " + suffix);
            } catch (IOException expected) { }
            assertTrue(surviving.delete());
            BiometricVaultFiles.verifyDeletion(base);
        }
    }
}
