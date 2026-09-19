package com.wails.app;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Matrix;
import android.graphics.Paint;
import android.graphics.RectF;
import android.media.ExifInterface;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;

/** Bounded, off-main-thread decoding for Go's local upload rendition pipeline. */
public final class GalleryImage {
    private GalleryImage() {}

    public static native void nativeInit();

    // Invoked by JNI on the Go generation worker, never the activity UI thread.
    public static byte[] downsample(byte[] sourceBytes, int edge, boolean encoded, int exif) {
        try {
            return sample(sourceBytes, edge, encoded, exif);
        } finally {
            // JNI creates an owned Java copy of decrypted data.
            if (encoded) Arrays.fill(sourceBytes, (byte) 0);
        }
    }

    private static byte[] sample(byte[] sourceBytes, int edge, boolean encoded, int exif) {
        if (edge < 1 || edge > 1600) return null;
        String path = encoded ? null : new String(sourceBytes, StandardCharsets.UTF_8);
        BitmapFactory.Options options = new BitmapFactory.Options();
        options.inJustDecodeBounds = true;
        decode(sourceBytes, path, options);
        long pixels = (long) options.outWidth * options.outHeight;
        if (options.outWidth <= 0 || options.outHeight <= 0 || pixels > 200_000_000) return null;
        // Keep unknown codecs below a full-decode budget even if an older
        // vendor decoder does not honour inSampleSize for that format.
        boolean sampled = "image/jpeg".equals(options.outMimeType)
                || "image/heif".equals(options.outMimeType)
                || "image/heic".equals(options.outMimeType)
                || "image/webp".equals(options.outMimeType);
        if (!sampled && pixels > 12_000_000) return null;
        int longest = Math.max(options.outWidth, options.outHeight);
        int sample = 1;
        while (longest / (sample * 2) >= edge) sample *= 2;
        options.inSampleSize = sample;
        options.inJustDecodeBounds = false;
        options.inPreferredConfig = Bitmap.Config.ARGB_8888;
        options.inScaled = false;
        Bitmap source = null;
        Bitmap output = null;
        try {
            source = decode(sourceBytes, path, options);
            if (source == null) return null;
            if (source.getWidth() > edge * 2 || source.getHeight() > edge * 2) return null;
            Matrix transform = orientation(path, exif);
            RectF bounds = new RectF(0, 0, source.getWidth(), source.getHeight());
            transform.mapRect(bounds);
            float scale = Math.min(1f, edge / Math.max(bounds.width(), bounds.height()));
            int width = Math.max(1, (int) (bounds.width() * scale));
            int height = Math.max(1, (int) (bounds.height() * scale));
            output = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
            Canvas canvas = new Canvas(output);
            canvas.drawColor(Color.WHITE);
            transform.postTranslate(-bounds.left, -bounds.top);
            transform.postScale(scale, scale);
            canvas.drawBitmap(source, transform, new Paint(Paint.FILTER_BITMAP_FLAG));
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            if (!output.compress(Bitmap.CompressFormat.JPEG, 82, bytes)) return null;
            return bytes.toByteArray();
        } catch (RuntimeException error) {
            // Unsupported/corrupt inputs remain uploaded originals; their tile
            // reports unavailable rather than crashing the upload operation.
            return null;
        } finally {
            if (output != null) output.recycle();
            if (source != null) source.recycle();
        }
    }

    private static Bitmap decode(byte[] data, String path, BitmapFactory.Options options) {
        return path == null ? BitmapFactory.decodeByteArray(data, 0, data.length, options)
                : BitmapFactory.decodeFile(path, options);
    }

    private static Matrix orientation(String path, int exif) {
        Matrix matrix = new Matrix();
        int value = exif;
        try {
            if (path != null) value = new ExifInterface(path).getAttributeInt(ExifInterface.TAG_ORIENTATION, value);
        } catch (IOException ignored) {
            // Formats without EXIF keep their decoded orientation.
        }
        switch (value) {
            case ExifInterface.ORIENTATION_FLIP_HORIZONTAL: matrix.setScale(-1, 1); break;
            case ExifInterface.ORIENTATION_ROTATE_180: matrix.setRotate(180); break;
            case ExifInterface.ORIENTATION_FLIP_VERTICAL: matrix.setScale(1, -1); break;
            case ExifInterface.ORIENTATION_TRANSPOSE:
                matrix.setRotate(90); matrix.postScale(-1, 1); break;
            case ExifInterface.ORIENTATION_ROTATE_90: matrix.setRotate(90); break;
            case ExifInterface.ORIENTATION_TRANSVERSE:
                matrix.setRotate(270); matrix.postScale(-1, 1); break;
            case ExifInterface.ORIENTATION_ROTATE_270: matrix.setRotate(270); break;
            default: break;
        }
        return matrix;
    }
}
