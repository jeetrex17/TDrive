package com.wails.app;

import android.graphics.Bitmap;
import android.media.MediaMetadataRetriever;
import android.os.Build;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;

/** One video frame, bounded, for Go's upload poster pipeline. */
public final class GalleryVideo {
    private GalleryVideo() {}

    public static native void nativeInit();

    /** Matches the quality the sampled photo path encodes at. */
    private static final int JPEG_QUALITY = 82;

    /**
     * Extracts a representative frame and returns it as a JPEG, or null when
     * this device cannot draw one.
     *
     * <p>Null is the ordinary answer for a codec the hardware lacks, a file
     * that is not really a video, or a stream with no video track at all. Go
     * reports every one of them as "no picture" and publishes the video
     * regardless, so nothing here throws upward.
     *
     * <p>Invoked by JNI on a Go worker, never on the activity UI thread: this
     * blocks for as long as the decoder takes to reach the frame.
     *
     * @param pathUtf8      the file path as real UTF-8, not JNI modified UTF-8
     * @param edge          longest side of the returned frame, in pixels
     * @param seekPercent   how far into the video to seek, as a percentage
     * @param fallbackMicros where to seek when no duration is declared
     */
    public static byte[] poster(byte[] pathUtf8, int edge, int seekPercent, long fallbackMicros) {
        if (edge < 1 || edge > 1600 || seekPercent < 0 || seekPercent > 100 || fallbackMicros < 0) return null;
        String path = new String(pathUtf8, StandardCharsets.UTF_8);
        MediaMetadataRetriever retriever = new MediaMetadataRetriever();
        try {
            retriever.setDataSource(path);
            if (!hasVideoTrack(retriever)) return null;
            long offset = frameOffsetMicros(retriever, seekPercent, fallbackMicros);
            byte[] frame = encode(frameAt(retriever, offset, edge));
            // A clip shorter than the offset, or one whose only sync frame is
            // its first, yields nothing at the seek point but plenty at zero.
            return frame != null || offset == 0 ? frame : encode(frameAt(retriever, 0, edge));
        } catch (RuntimeException error) {
            // setDataSource throws IllegalArgumentException for an unreadable
            // file and IllegalStateException for one it cannot parse.
            return null;
        } finally {
            release(retriever);
        }
    }

    /**
     * Reports whether there is a picture to extract at all. An audio file that
     * someone named .mp4 fails here instead of after a pointless seek.
     */
    private static boolean hasVideoTrack(MediaMetadataRetriever retriever) {
        return "yes".equals(retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_HAS_VIDEO));
    }

    /**
     * Where to take the frame from: a share of the declared duration, or the
     * caller's fallback when the container declares none -- a fragmented MP4
     * still being written, or a stream remuxed without a header.
     */
    private static long frameOffsetMicros(MediaMetadataRetriever retriever, int seekPercent, long fallbackMicros) {
        String declared = retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION);
        if (declared == null) return fallbackMicros;
        try {
            long millis = Long.parseLong(declared.trim());
            // Milliseconds to microseconds before the share, so a short clip
            // does not round its whole offset away.
            return millis > 0 ? millis * 1000L / 100L * seekPercent : fallbackMicros;
        } catch (NumberFormatException malformed) {
            return fallbackMicros;
        }
    }

    /**
     * The frame itself. On API 27+ the retriever scales during extraction, so
     * a 4K video never allocates a 4K bitmap; older devices decode full size
     * and Go bounds the result, which is the best they can do.
     */
    private static Bitmap frameAt(MediaMetadataRetriever retriever, long micros, int edge) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O_MR1) {
            Bitmap scaled = retriever.getScaledFrameAtTime(
                    micros, MediaMetadataRetriever.OPTION_CLOSEST_SYNC, edge, edge);
            if (scaled != null) return scaled;
        }
        return retriever.getFrameAtTime(micros, MediaMetadataRetriever.OPTION_CLOSEST_SYNC);
    }

    /** Encodes and recycles in one place, so no path can return without freeing the bitmap. */
    private static byte[] encode(Bitmap frame) {
        if (frame == null) return null;
        try {
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            return frame.compress(Bitmap.CompressFormat.JPEG, JPEG_QUALITY, bytes) ? bytes.toByteArray() : null;
        } catch (RuntimeException error) {
            return null;
        } finally {
            frame.recycle();
        }
    }

    /** The retriever holds a hardware decoder; it is released on every path. */
    private static void release(MediaMetadataRetriever retriever) {
        try {
            retriever.release();
        } catch (Exception ignored) {
            // release() is declared differently across API levels; either way
            // there is nothing useful left to do about a decoder that will not
            // let go, and a poster must not fail an upload.
        }
    }
}
