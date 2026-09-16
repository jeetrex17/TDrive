package com.wails.app;

import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.media.MediaScannerConnection;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.provider.MediaStore;
import android.webkit.MimeTypeMap;

import androidx.annotation.RequiresApi;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/**
 * Moves a finished download out of the app's private sandbox and into the
 * phone's public Downloads folder.
 *
 * Everything the Go side writes lands under the app's own files directory, and
 * since Android 11 no file manager will browse Android/data, so a download that
 * stays there is a download the reader cannot reach. A per-download location
 * prompt is not the platform's answer either: Chrome, Drive and Photos all put
 * the file in Downloads and say so. This does the same, and reports the name it
 * actually got, which is not always the name it asked for.
 */
final class DownloadExport {
    /** Enough numbered names that giving up says something real about the folder. */
    private static final int MAX_TRIES = 999;

    private DownloadExport() {
    }

    /**
     * Copies a file or a whole folder into public Downloads, deletes the
     * sandbox original, and answers where it landed relative to shared
     * storage: "Download/plan.pdf", or "Download/Holiday" for a folder.
     *
     * A copy that fails part way leaves the original alone and takes its own
     * half-written result back out again.
     */
    static String save(Context ctx, File source) throws IOException {
        File home = ctx.getFilesDir().getCanonicalFile();
        File src = source.getCanonicalFile();
        if (!src.getPath().startsWith(home.getPath() + File.separator)) {
            // The webview asks for these by path, so it could ask for any path.
            // Only what the app wrote is the app's to move.
            throw new IOException("that is not this app's file to move");
        }
        if (!src.exists()) {
            throw new IOException("there is nothing at that path any more");
        }

        String location = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
                ? saveThroughMediaStore(ctx, src)
                : saveIntoPublicFolder(ctx, src);
        deleteTree(src);
        return location;
    }

    // MARK: - Android 10 and up

    /**
     * Hands the bytes to MediaStore, which owns Downloads on Android 10 and up.
     * The row is pending while it is written so nothing else sees a half file.
     */
    @RequiresApi(Build.VERSION_CODES.Q)
    private static String saveThroughMediaStore(Context ctx, File src) throws IOException {
        ContentResolver resolver = ctx.getContentResolver();
        String downloads = Environment.DIRECTORY_DOWNLOADS;

        if (!src.isDirectory()) {
            Uri row = writeRow(resolver, src, downloads, freeName(resolver, downloads, src.getName(), true));
            return locationOf(resolver, row);
        }

        String folder = freeFolder(resolver, downloads, src.getName());
        List<Uri> written = new ArrayList<>();
        try {
            copyTree(resolver, src, downloads + "/" + folder, written);
            if (written.isEmpty()) {
                // MediaStore has no such thing as an empty directory, so a
                // folder holding no files has nowhere to land.
                throw new IOException("that folder has nothing in it to save");
            }
        } catch (IOException e) {
            for (Uri row : written) {
                resolver.delete(row, null, null);
            }
            // Deleting the rows does not take back the directories MediaStore
            // made for them, and an empty folder left in Downloads would make
            // the next attempt call itself "(2)".
            deleteEmptyDirs(new File(
                    Environment.getExternalStoragePublicDirectory(downloads), folder));
            throw e;
        }
        // The folder we asked for is the folder we got, but the answer is read
        // back rather than assumed: it is what the toast will name.
        return folderOf(locationOf(resolver, written.get(0)), downloads + "/" + folder);
    }

    @RequiresApi(Build.VERSION_CODES.Q)
    private static void copyTree(ContentResolver resolver, File dir, String relDir, List<Uri> written)
            throws IOException {
        File[] entries = dir.listFiles();
        if (entries == null) {
            throw new IOException("could not read " + dir.getName());
        }
        for (File entry : entries) {
            String name = safeName(entry.getName());
            if (entry.isDirectory()) {
                copyTree(resolver, entry, relDir + "/" + name, written);
                continue;
            }
            written.add(writeRow(resolver, entry, relDir, name));
        }
    }

    @RequiresApi(Build.VERSION_CODES.Q)
    private static Uri writeRow(ContentResolver resolver, File src, String relDir, String name)
            throws IOException {
        ContentValues values = new ContentValues();
        values.put(MediaStore.MediaColumns.DISPLAY_NAME, name);
        values.put(MediaStore.MediaColumns.MIME_TYPE, mimeOf(name));
        values.put(MediaStore.MediaColumns.RELATIVE_PATH, relDir + "/");
        values.put(MediaStore.MediaColumns.IS_PENDING, 1);

        Uri row = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values);
        if (row == null) {
            throw new IOException("Downloads would not take " + name);
        }
        try (InputStream in = new FileInputStream(src);
             OutputStream out = resolver.openOutputStream(row)) {
            if (out == null) {
                throw new IOException("could not write " + name);
            }
            copy(in, out);
        } catch (IOException e) {
            resolver.delete(row, null, null);
            throw e;
        }
        ContentValues done = new ContentValues();
        done.put(MediaStore.MediaColumns.IS_PENDING, 0);
        resolver.update(row, done, null, null);
        return row;
    }

    /** Where a row really is, read off the row itself: "Download/plan (2).pdf". */
    @RequiresApi(Build.VERSION_CODES.Q)
    private static String locationOf(ContentResolver resolver, Uri row) throws IOException {
        try (Cursor cursor = resolver.query(row, new String[]{
                MediaStore.MediaColumns.RELATIVE_PATH,
                MediaStore.MediaColumns.DISPLAY_NAME,
        }, null, null, null)) {
            if (cursor != null && cursor.moveToFirst()) {
                String rel = cursor.getString(0);
                String name = cursor.getString(1);
                if (rel != null && name != null && !name.isEmpty()) {
                    return trimSlashes(rel) + "/" + name;
                }
            }
        }
        throw new IOException("saved, but Downloads will not say where");
    }

    /** The folder a file's location sits in: "Download/Holiday/a/x" to "Download/Holiday". */
    private static String folderOf(String fileLocation, String fallback) {
        String[] parts = fileLocation.split("/");
        return parts.length >= 2 ? parts[0] + "/" + parts[1] : fallback;
    }

    /**
     * A name nothing in that folder answers to yet. MediaStore will also
     * de-duplicate on its own, and differently, which is why the caller reads
     * the name back rather than trusting this one.
     */
    @RequiresApi(Build.VERSION_CODES.Q)
    private static String freeName(ContentResolver resolver, String relDir, String name, boolean hasExtension)
            throws IOException {
        String bare = safeName(name);
        for (int n = 1; n <= MAX_TRIES; n++) {
            String candidate = numbered(bare, n, hasExtension);
            try (Cursor cursor = resolver.query(MediaStore.Downloads.EXTERNAL_CONTENT_URI,
                    new String[]{MediaStore.MediaColumns._ID},
                    MediaStore.MediaColumns.RELATIVE_PATH + "=? AND "
                            + MediaStore.MediaColumns.DISPLAY_NAME + "=?",
                    new String[]{relDir + "/", candidate}, null)) {
                if (cursor == null || cursor.getCount() == 0) {
                    return candidate;
                }
            }
        }
        throw new IOException("Downloads already holds a thousand of those");
    }

    /** The same, for a folder: taken if Downloads holds anything underneath it. */
    @RequiresApi(Build.VERSION_CODES.Q)
    private static String freeFolder(ContentResolver resolver, String relDir, String name) throws IOException {
        String bare = safeName(name);
        for (int n = 1; n <= MAX_TRIES; n++) {
            String candidate = numbered(bare, n, false);
            try (Cursor cursor = resolver.query(MediaStore.Downloads.EXTERNAL_CONTENT_URI,
                    new String[]{MediaStore.MediaColumns._ID},
                    MediaStore.MediaColumns.RELATIVE_PATH + " LIKE ? ESCAPE '\\'",
                    new String[]{escapeLike(relDir + "/" + candidate + "/") + "%"}, null)) {
                if (cursor == null || cursor.getCount() == 0) {
                    return candidate;
                }
            }
        }
        throw new IOException("Downloads already holds a thousand of those");
    }

    private static String escapeLike(String value) {
        return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_");
    }

    // MARK: - Android 9 and below

    /**
     * Before MediaStore owned Downloads, the folder was just a folder, reachable
     * with WRITE_EXTERNAL_STORAGE. The media scanner is told afterwards so the
     * files show up in the Downloads app rather than only in a file manager.
     */
    private static String saveIntoPublicFolder(Context ctx, File src) throws IOException {
        File downloads = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS);
        if (downloads == null || (!downloads.isDirectory() && !downloads.mkdirs())) {
            throw new IOException("this phone has no Downloads folder");
        }
        File target = freeTarget(downloads, safeName(src.getName()), !src.isDirectory());
        List<String> written = new ArrayList<>();
        try {
            if (src.isDirectory()) {
                if (!target.mkdirs()) {
                    throw new IOException("could not make " + target.getName() + " in Downloads");
                }
                copyTree(src, target, written);
            } else {
                copyFile(src, target);
                written.add(target.getPath());
            }
        } catch (IOException e) {
            deleteTree(target);
            throw e;
        }
        if (!written.isEmpty()) {
            MediaScannerConnection.scanFile(ctx, written.toArray(new String[0]), null, null);
        }
        return Environment.DIRECTORY_DOWNLOADS + "/" + target.getName();
    }

    private static void copyTree(File dir, File target, List<String> written) throws IOException {
        File[] entries = dir.listFiles();
        if (entries == null) {
            throw new IOException("could not read " + dir.getName());
        }
        for (File entry : entries) {
            File out = new File(target, safeName(entry.getName()));
            if (entry.isDirectory()) {
                if (!out.isDirectory() && !out.mkdirs()) {
                    throw new IOException("could not make " + out.getName());
                }
                copyTree(entry, out, written);
                continue;
            }
            copyFile(entry, out);
            written.add(out.getPath());
        }
    }

    private static File freeTarget(File dir, String name, boolean hasExtension) throws IOException {
        for (int n = 1; n <= MAX_TRIES; n++) {
            File candidate = new File(dir, numbered(name, n, hasExtension));
            if (!candidate.exists()) {
                return candidate;
            }
        }
        throw new IOException("Downloads already holds a thousand of those");
    }

    private static void copyFile(File src, File out) throws IOException {
        try (InputStream in = new FileInputStream(src);
             OutputStream os = new FileOutputStream(out)) {
            copy(in, os);
        }
    }

    // MARK: - Shared

    /** "plan.pdf", then "plan (2).pdf", the way Finder and the Files app number a duplicate. */
    private static String numbered(String name, int n, boolean hasExtension) {
        if (n < 2) {
            return name;
        }
        int dot = hasExtension ? name.lastIndexOf('.') : -1;
        if (dot <= 0) {
            return name + " (" + n + ")";
        }
        return name.substring(0, dot) + " (" + n + ")" + name.substring(dot);
    }

    private static String mimeOf(String name) {
        int dot = name.lastIndexOf('.');
        if (dot > 0 && dot < name.length() - 1) {
            String mime = MimeTypeMap.getSingleton()
                    .getMimeTypeFromExtension(name.substring(dot + 1).toLowerCase(Locale.US));
            if (mime != null) {
                return mime;
            }
        }
        return "application/octet-stream";
    }

    /** As elsewhere: a name is reduced to a bare one before it is joined to a path. */
    private static String safeName(String name) {
        String bare = new File(name).getName().trim();
        if (bare.isEmpty() || bare.equals(".") || bare.equals("..")) {
            return "item";
        }
        return bare;
    }

    private static String trimSlashes(String path) {
        int end = path.length();
        while (end > 0 && path.charAt(end - 1) == '/') {
            end--;
        }
        return path.substring(0, end);
    }

    private static void copy(InputStream in, OutputStream out) throws IOException {
        byte[] buf = new byte[64 * 1024];
        int n;
        while ((n = in.read(buf)) > 0) {
            out.write(buf, 0, n);
        }
    }

    /** Bottom up, and only what is empty: delete() will not touch a directory that holds anything. */
    private static void deleteEmptyDirs(File dir) {
        File[] entries = dir.listFiles();
        if (entries != null) {
            for (File entry : entries) {
                if (entry.isDirectory()) {
                    deleteEmptyDirs(entry);
                }
            }
        }
        dir.delete();
    }

    private static void deleteTree(File file) {
        File[] entries = file.listFiles();
        if (entries != null) {
            for (File entry : entries) {
                deleteTree(entry);
            }
        }
        file.delete();
    }
}
