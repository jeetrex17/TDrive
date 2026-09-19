package com.wails.app;

import android.annotation.SuppressLint;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.res.Configuration;
import android.database.Cursor;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.Uri;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Bundle;
import android.os.PowerManager;
import android.os.StatFs;
import android.content.pm.PackageManager;
import android.content.ContentUris;
import android.content.ContentResolver;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Color;
import android.provider.MediaStore;
import android.provider.DocumentsContract;
import android.provider.OpenableColumns;
import android.util.Base64;
import android.util.Log;
import android.view.WindowManager;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

import androidx.activity.OnBackPressedCallback;
import androidx.annotation.Nullable;
import androidx.appcompat.app.AppCompatActivity;
import androidx.core.content.FileProvider;
import androidx.core.view.WindowCompat;
import androidx.webkit.WebViewAssetLoader;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.HashMap;
import java.util.concurrent.Callable;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * MainActivity hosts the WebView and manages the Wails application lifecycle.
 * It uses WebViewAssetLoader to serve assets from the Go library without
 * requiring a network server.
 */
public class MainActivity extends AppCompatActivity {
    private static final String TAG = "WailsActivity";
    private static final boolean DEBUG = BuildConfig.DEBUG;
    private static final String WAILS_SCHEME = "https";
    private static final String WAILS_HOST = "wails.localhost";
    private static final int FILE_PICKER_REQUEST = 7001;
    /**
     * How many picked documents are copied out of the picker at once. Four
     * matches ANDROID_UPLOAD_WINDOW on the other side of this flow: enough to
     * keep a provider busy through its per-file latency, few enough that a
     * large selection cannot fill the cache with in-flight copies.
     */
    private static final int PICKER_COPY_CONCURRENCY = 4;
    /** Floor between progress ticks. Roughly three frames: visibly live, cheap. */
    private static final long PICKER_PROGRESS_INTERVAL_MS = 50;
    private static final float MIN_TEXT_SCALE = 0.85f;
    private static final float MAX_TEXT_SCALE = 2.50f;

    private WebView webView;
    private WailsBridge bridge;
    // Battery: system-event receivers are registered only while the activity is
    // in the foreground (onStart) and torn down in onStop, so background battery/
    // network/screen broadcasts don't wake the app.
    private boolean systemReceiversRegistered = false;
    private WebViewAssetLoader assetLoader;

    // The Go-side dialog ID of the in-flight file picker (-1 when idle)
    private int pendingFilePickerCallbackID = -1;
    private String pendingFolderCallbackId = null;
    // The tree the last folder pick returned, what it holds keyed by the id the
    // manifest handed out, and where copies of it go. Written on the picker's
    // thread and read on whichever thread the uploader asks from.
    private volatile Uri pickedTree;
    private volatile File pickedCache;
    private final Map<String, PickedFile> pickedFiles = new ConcurrentHashMap<>();
    private WailsJSBridge jsBridge;
    private static final int FOLDER_PICKER_REQUEST = 7004;
    // Its own request code, and its own pending callback: the import picker
    // above holds one document tree and releases the previous grant whenever it
    // runs, so a backup pick must never arrive on that path.
    private static final int PHOTO_BACKUP_FOLDER_REQUEST = 7005;
    private static final int PHOTO_CAPTURE_REQUEST = 7002;
    private static final int VIDEO_CAPTURE_REQUEST = 7003;
    private static final int CAMERA_PERMISSION_REQUEST = 7010;
    private static final int SAVE_PERMISSION_REQUEST = 7011;
    private static final int PHOTO_BACKUP_PERMISSION_REQUEST = 7012;
    private static final int NOTIFICATION_PERMISSION_REQUEST = 7013;
    private static final int PHOTO_BACKUP_PAGE_LIMIT = 128;
    // One resource is staged at a time, which permits normal phone videos while
    // still putting a firm bound on a malicious or corrupt MediaStore row.
    private static final long PHOTO_BACKUP_STAGE_MAX_BYTES = 4L * 1024L * 1024L * 1024L;
    // Never consume the last space the app and OS need for normal operation.
    private static final long PHOTO_BACKUP_STAGE_FREE_HEADROOM_BYTES = 512L * 1024L * 1024L;
    private String pendingSaveCallbackId;
    private String pendingSavePath;
    private File pendingCaptureFile;
    private boolean pendingCaptureIsVideo;
    private String pendingPhotoBackupPermissionCallbackId;
    private String pendingPhotoBackupFolderCallbackId;
    // Whether a transfer has held the process open since the last time the user
    // was here, and whether this run of the app has already asked about it.
    // Written from the JS thread by noteBackgroundTransferRunning, read on the
    // main thread in onResume.
    private volatile boolean backgroundTransferRan;
    private boolean askedForTransferNotifications;
    // Staging is intentionally independent of discovery lifetime: durable
    // queues keep MediaStore IDs, and every materialization revalidates them.
    private final Map<String, File> stagedPhotoBackupAssets = new ConcurrentHashMap<>();
    private final Map<String, AtomicBoolean> stagingPhotoBackupAssets = new ConcurrentHashMap<>();
    private final Object photoBackupStageLock = new Object();
    // A global slot is intentional: a 4 GiB resource must never be multiplied
    // by several simultaneous native requests.
    private String activePhotoBackupStageKey;

    // System-event sources (battery/power, screen lock, network). Registered in
    // onCreate, torn down in onDestroy. Each forwards a "system:*" event to JS
    // via the bridge.
    private BroadcastReceiver batteryReceiver;
    private BroadcastReceiver screenReceiver;
    private BroadcastReceiver powerSaveReceiver;
    private ConnectivityManager connectivityManager;
    private ConnectivityManager.NetworkCallback networkCallback;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        takeWholeWindow();
        setContentView(R.layout.activity_main);
        cleanupPhotoBackupStage();

        // Initialize the native Go library
        bridge = new WailsBridge(this);
        WailsForegroundService.attachRuntimeBridge(bridge);
        GalleryImage.nativeInit();
        GalleryVideo.nativeInit();
        bridge.initialize();

        // Set up WebView
        setupWebView();

        // Load the application
        loadApplication();
    }

    /**
     * The page keeps itself clear of the status bar and the gesture handle from
     * the insets WailsBridge reports, so the window has to hand it the whole
     * screen. Fitted, the decor lays the WebView out between the system bars
     * instead and the window background shows through above and below the page
     * as a hard-edged band. Android 15 gives the window over on its own; older
     * releases need asking, and need the bars left uncoloured so the decor does
     * not paint the same band back on top of the content.
     */
    private void takeWholeWindow() {
        WindowCompat.setDecorFitsSystemWindows(getWindow(), false);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            // A turned phone keeps the window off the short edge it reserves for
            // a cutout, which is the edge the status bar moves to in landscape,
            // and that is the whole width of the video player.
            WindowManager.LayoutParams attrs = getWindow().getAttributes();
            attrs.layoutInDisplayCutoutMode =
                    WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
            getWindow().setAttributes(attrs);
        }
        getWindow().setStatusBarColor(Color.TRANSPARENT);
        getWindow().setNavigationBarColor(Color.TRANSPARENT);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            // Without this the system draws its own scrim behind a transparent
            // navigation bar, which is the band again in a different colour.
            getWindow().setNavigationBarContrastEnforced(false);
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void setupWebView() {
        webView = findViewById(R.id.webview);
        bridge.setWebView(webView);

        // Configure WebView settings
        WebSettings settings = webView.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setDatabaseEnabled(true);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        settings.setMediaPlaybackRequiresUserGesture(false);
        applySystemTextScale(settings);
        // Page origin is https://wails.localhost; TDrive streams media from its own http://127.0.0.1 server (see res/xml/network_security_config.xml).
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);

        // Enable debugging in debug builds
        if (DEBUG) {
            WebView.setWebContentsDebuggingEnabled(true);
        }

        // Set up asset loader for serving local assets
        assetLoader = new WebViewAssetLoader.Builder()
                .setDomain(WAILS_HOST)
                .addPathHandler("/", new WailsPathHandler(bridge))
                .build();

        // Set up WebView client to intercept requests
        webView.setWebViewClient(new WebViewClient() {
            @Nullable
            @Override
            public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                // Handle wails.localhost requests
                if (request.getUrl().getHost() != null &&
                        request.getUrl().getHost().equals(WAILS_HOST)) {

                    // For wails API calls (runtime, capabilities, etc.) pass the
                    // full URL including the query string, because
                    // WebViewAssetLoader.PathHandler strips query params
                    String path = request.getUrl().getPath();
                    if (path != null && path.startsWith("/wails/")) {
                        String fullPath = path;
                        String query = request.getUrl().getQuery();
                        if (query != null && !query.isEmpty()) {
                            fullPath = path + "?" + query;
                        }
                        if (DEBUG) Log.d(TAG, "Wails API call: " + fullPath);

                        byte[] data = bridge.serveAsset(fullPath, request.getMethod(), "{}");
                        if (data != null && data.length > 0) {
                            java.io.InputStream inputStream = new java.io.ByteArrayInputStream(data);
                            java.util.Map<String, String> headers = new java.util.HashMap<>();
                            headers.put("Access-Control-Allow-Origin", "*");
                            headers.put("Cache-Control", "no-cache");
                            headers.put("Content-Type", "application/json");

                            return new WebResourceResponse(
                                "application/json",
                                "UTF-8",
                                200,
                                "OK",
                                headers,
                                inputStream
                            );
                        }
                        // Return error response if data is null
                        return new WebResourceResponse(
                            "application/json",
                            "UTF-8",
                            500,
                            "Internal Error",
                            new java.util.HashMap<>(),
                            new java.io.ByteArrayInputStream("{}".getBytes())
                        );
                    }

                    // Stream captured photos/videos from the cache with HTTP Range
                    // support so <video> can seek/stream a clip of any length.
                    if (path != null && path.startsWith("/__capture__/")) {
                        return serveCaptureFile(path.substring("/__capture__/".length()), request);
                    }

                    // For regular assets, use the asset loader
                    return assetLoader.shouldInterceptRequest(request.getUrl());
                }

                return super.shouldInterceptRequest(view, request);
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                super.onPageFinished(view, url);
                if (DEBUG) Log.d(TAG, "Page loaded: " + url);
                bridge.onPageFinished(url);
                publishSystemTextScale();
                // Now that JS listeners are mounted, push a snapshot of the
                // current battery / network / theme so the UI starts populated.
                emitSystemSnapshot();
            }
        });

        // Add JavaScript interface for Go communication
        jsBridge = new WailsJSBridge(bridge, webView);
        webView.addJavascriptInterface(jsBridge, "wails");

        registerBackHandler();
    }

    /**
     * Android's font-size accessibility preference is exposed as
     * Configuration.fontScale, but a WebView does not reliably consume it for
     * CSS text. Text zoom is the supported WebSettings mapping: it affects text
     * rather than the whole page, so 48dp targets and reserved gesture edges
     * keep their physical size. Re-read it on resume as Settings can change
     * while TDrive is backgrounded.
     */
    private void applySystemTextScale(WebSettings settings) {
        settings.setTextZoom(Math.round(systemTextScale() * 100f));
    }

    private float systemTextScale() {
        float scale = getResources().getConfiguration().fontScale;
        if (Float.isNaN(scale) || Float.isInfinite(scale) || scale <= 0f) {
            scale = 1f;
        }
        return Math.max(MIN_TEXT_SCALE, Math.min(MAX_TEXT_SCALE, scale));
    }

    /**
     * TextZoom does the visual scaling on Android. The page also needs the
     * category to choose its large-text wrapping rules, matching the Dynamic
     * Type bridge on iOS without setting the CSS root a second time.
     */
    private void publishSystemTextScale() {
        if (webView == null) return;
        String scale = String.format(Locale.US, "%.3f", systemTextScale());
        String script = "window.__tdriveSystemTextScale=" + scale
                + ";window.dispatchEvent(new CustomEvent('tdrive:system-text-scale',{detail:{scale:window.__tdriveSystemTextScale}}));";
        webView.evaluateJavascript(script, null);
    }

    private void loadApplication() {
        String url = WAILS_SCHEME + "://" + WAILS_HOST + "/";
        if (DEBUG) Log.d(TAG, "Loading URL: " + url);
        webView.loadUrl(url);
    }

    /**
     * Launch the system camera to capture a photo (video=false) or a video
     * (video=true). The capture is written to a FileProvider URI in the cache and
     * the result is delivered to JS as a "common:capture" event.
     */
    public void launchCameraCapture(boolean video) {
        if (checkSelfPermission("android.permission.CAMERA") != PackageManager.PERMISSION_GRANTED) {
            pendingCaptureIsVideo = video;
            requestPermissions(new String[]{"android.permission.CAMERA"}, CAMERA_PERMISSION_REQUEST);
            return;
        }
        try {
            File dir = new File(getCacheDir(), "captures");
            if (!dir.exists()) dir.mkdirs();
            pendingCaptureFile = new File(dir, "capture_" + System.currentTimeMillis() + (video ? ".mp4" : ".jpg"));
            pendingCaptureIsVideo = video;
            Uri uri = FileProvider.getUriForFile(this, getPackageName() + ".fileprovider", pendingCaptureFile);
            Intent intent = new Intent(video ? MediaStore.ACTION_VIDEO_CAPTURE : MediaStore.ACTION_IMAGE_CAPTURE);
            intent.putExtra(MediaStore.EXTRA_OUTPUT, uri);
            intent.addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
            // Don't pre-check with resolveActivity(): Android 11+ package visibility
            // hides other apps' intents unless declared in <queries>, so it can
            // return null even when a camera app exists. Just launch and handle a miss.
            startActivityForResult(intent, video ? VIDEO_CAPTURE_REQUEST : PHOTO_CAPTURE_REQUEST);
        } catch (android.content.ActivityNotFoundException e) {
            bridge.emitEvent("common:capture", "{\"error\":\"no camera app available\"}");
        } catch (Exception e) {
            Log.e(TAG, "launchCameraCapture failed", e);
            bridge.emitEvent("common:capture", "{\"error\":\"capture failed\"}");
        }
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == CAMERA_PERMISSION_REQUEST) {
            if (grantResults.length > 0 && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
                launchCameraCapture(pendingCaptureIsVideo);
            } else {
                bridge.emitEvent("common:capture", "{\"error\":\"camera permission denied\"}");
            }
            return;
        }
        if (requestCode == SAVE_PERMISSION_REQUEST) {
            String callbackId = pendingSaveCallbackId;
            String path = pendingSavePath;
            pendingSaveCallbackId = null;
            pendingSavePath = null;
            if (callbackId == null) {
                return;
            }
            if (grantResults.length > 0 && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
                runSaveToDownloads(callbackId, path);
            } else {
                jsBridge.sendCallback(callbackId, null, "TDrive needs permission to write to Downloads");
            }
            return;
        }
        // Nothing to resume: this path only primes the permission ahead of a
        // transfer. A held notification is replayed by WailsBridge's own
        // request code; a refusal leaves transfers running with nothing to say.
        if (requestCode == NOTIFICATION_PERMISSION_REQUEST) {
            return;
        }
        if (requestCode == PHOTO_BACKUP_PERMISSION_REQUEST) {
            String callbackId = pendingPhotoBackupPermissionCallbackId;
            pendingPhotoBackupPermissionCallbackId = null;
            if (callbackId != null) {
                jsBridge.sendCallback(callbackId, photoBackupAccess().toString(), null);
            }
            return;
        }
        if (bridge != null) {
            bridge.onRequestPermissionsResult(requestCode, grantResults);
        }
    }

    private void handleCaptureResult(int resultCode, @Nullable Intent data) {
        File file = pendingCaptureFile;
        final boolean video = pendingCaptureIsVideo;
        pendingCaptureFile = null;
        if (resultCode != RESULT_OK) {
            bridge.emitEvent("common:capture", "{\"cancelled\":true}");
            return;
        }
        // Some camera apps (commonly for video) ignore EXTRA_OUTPUT and instead
        // return a content URI in the result data; copy that into our cache.
        if ((file == null || !file.exists() || file.length() == 0)
                && data != null && data.getData() != null) {
            String copied = copyUriToCache(data.getData());
            if (copied != null) file = new File(copied);
        }
        final File f = file;
        if (f == null || !f.exists() || f.length() == 0) {
            bridge.emitEvent("common:capture", "{\"cancelled\":true}");
            return;
        }
        new Thread(() -> {
            try {
                JSONObject o = new JSONObject();
                o.put("type", video ? "video" : "photo");
                o.put("path", f.getAbsolutePath());
                o.put("size", f.length());
                if (!video) {
                    String thumb = makePhotoThumbnail(f);
                    if (thumb != null) o.put("thumb", thumb);
                }
                // Stream URL works for both: <video>/<img> load it from the cache
                // via shouldInterceptRequest (Range-enabled), no size limit.
                o.put("streamUrl", captureStreamUrl(f));
                bridge.emitEvent("common:capture", o.toString());
            } catch (Exception e) {
                Log.e(TAG, "handleCaptureResult failed", e);
                bridge.emitEvent("common:capture", "{\"error\":\"result processing failed\"}");
            }
        }).start();
    }

    /** Downscale a captured photo into a base64 JPEG data URL for display in the webview. */
    @Nullable
    private String makePhotoThumbnail(File file) {
        try {
            BitmapFactory.Options bounds = new BitmapFactory.Options();
            bounds.inJustDecodeBounds = true;
            BitmapFactory.decodeFile(file.getAbsolutePath(), bounds);
            int sample = 1;
            while (Math.max(bounds.outWidth, bounds.outHeight) / sample > 640) sample *= 2;
            BitmapFactory.Options opts = new BitmapFactory.Options();
            opts.inSampleSize = sample;
            Bitmap bmp = BitmapFactory.decodeFile(file.getAbsolutePath(), opts);
            if (bmp == null) return null;
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            bmp.compress(Bitmap.CompressFormat.JPEG, 70, baos);
            bmp.recycle();
            return "data:image/jpeg;base64," + Base64.encodeToString(baos.toByteArray(), Base64.NO_WRAP);
        } catch (Exception e) {
            return null;
        }
    }

    /**
     * Build a same-origin URL the webview can stream a capture from. Served by
     * serveCaptureFile (via shouldInterceptRequest); the path is relative to the
     * cache dir so both camera files (captures/) and copied content URIs
     * (wails-picker/) resolve.
     */
    private String captureStreamUrl(File file) {
        String base = getCacheDir().getAbsolutePath() + File.separator;
        String abs = file.getAbsolutePath();
        String rel = abs.startsWith(base) ? abs.substring(base.length()) : file.getName();
        return "/__capture__/" + Uri.encode(rel, "/");
    }

    /**
     * Serve a captured file (under the app cache) to the webview with HTTP Range
     * support, so &lt;video&gt; can stream and seek a clip of any length without
     * inlining it as a data URL.
     */
    private WebResourceResponse serveCaptureFile(String relPath, WebResourceRequest request) {
        try {
            File cache = getCacheDir();
            File file = new File(cache, Uri.decode(relPath));
            // Path-traversal guard: only ever serve files under the cache dir.
            if (!file.getCanonicalPath().startsWith(cache.getCanonicalPath() + File.separator)
                    || !file.exists() || !file.isFile()) {
                return new WebResourceResponse("text/plain", "UTF-8", 404, "Not Found",
                        new java.util.HashMap<>(), new java.io.ByteArrayInputStream(new byte[0]));
            }
            String name = file.getName().toLowerCase();
            String mime = name.endsWith(".mp4") ? "video/mp4"
                    : name.endsWith(".mov") ? "video/quicktime"
                    : name.endsWith(".jpg") || name.endsWith(".jpeg") ? "image/jpeg"
                    : name.endsWith(".png") ? "image/png" : "application/octet-stream";
            long length = file.length();
            java.util.Map<String, String> reqHeaders = request.getRequestHeaders();
            String range = reqHeaders != null ? reqHeaders.get("Range") : null;
            if (range == null && reqHeaders != null) range = reqHeaders.get("range");

            java.util.Map<String, String> headers = new java.util.HashMap<>();
            headers.put("Accept-Ranges", "bytes");
            headers.put("Cache-Control", "no-store");

            if (range != null && range.startsWith("bytes=")) {
                long start = 0, end = length - 1;
                String spec = range.substring(6).trim();
                int dash = spec.indexOf('-');
                if (dash >= 0) {
                    try {
                        if (dash > 0) start = Long.parseLong(spec.substring(0, dash).trim());
                        String e = spec.substring(dash + 1).trim();
                        if (!e.isEmpty()) end = Long.parseLong(e);
                    } catch (NumberFormatException ignored) { }
                }
                if (start < 0) start = 0;
                if (end >= length) end = length - 1;
                if (start > end) { start = 0; end = length - 1; }
                long count = end - start + 1;
                java.io.InputStream in = new java.io.FileInputStream(file);
                long toSkip = start;
                while (toSkip > 0) {
                    long s = in.skip(toSkip);
                    if (s <= 0) break;
                    toSkip -= s;
                }
                headers.put("Content-Range", "bytes " + start + "-" + end + "/" + length);
                headers.put("Content-Length", String.valueOf(count));
                return new WebResourceResponse(mime, null, 206, "Partial Content",
                        headers, new LimitedInputStream(in, count));
            }
            headers.put("Content-Length", String.valueOf(length));
            return new WebResourceResponse(mime, null, 200, "OK", headers,
                    new java.io.FileInputStream(file));
        } catch (Exception e) {
            Log.e(TAG, "serveCaptureFile failed", e);
            return new WebResourceResponse("text/plain", "UTF-8", 500, "Error",
                    new java.util.HashMap<>(), new java.io.ByteArrayInputStream(new byte[0]));
        }
    }

    /** Wraps a stream to yield at most a fixed number of bytes (for Range responses). */
    private static final class LimitedInputStream extends java.io.FilterInputStream {
        private long remaining;
        LimitedInputStream(java.io.InputStream in, long limit) {
            super(in);
            this.remaining = limit;
        }
        @Override public int read() throws java.io.IOException {
            if (remaining <= 0) return -1;
            int b = super.read();
            if (b >= 0) remaining--;
            return b;
        }
        @Override public int read(byte[] b, int off, int len) throws java.io.IOException {
            if (remaining <= 0) return -1;
            int n = super.read(b, off, (int) Math.min(len, remaining));
            if (n > 0) remaining -= n;
            return n;
        }
    }

    /**
     * Launch the system folder picker and answer with a manifest of what the
     * chosen tree holds, without copying a byte.
     *
     * The Storage Access Framework gives a document-tree URI, which nothing
     * above this understands. Mirroring the whole tree into the cache first was
     * the obvious answer and the wrong one: it wrote a second copy of the folder
     * before a single byte uploaded. The uploader asks for its files a batch at
     * a time instead, through materializeFiles.
     */
    // ---- Photo/video automatic backup -----------------------------------
    // These APIs use MediaStore IDs, never filesystem paths.  A queue may keep
    // an ID across restarts, but the OS grant is always checked again when the
    // bytes are requested; no picker or prior grant is used as a bypass.

    public JSONObject photoBackupAccess() {
        JSONObject result = new JSONObject();
        try {
            boolean images;
            boolean videos;
            boolean selected = false;
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                images = checkSelfPermission("android.permission.READ_MEDIA_IMAGES") == PackageManager.PERMISSION_GRANTED;
                videos = checkSelfPermission("android.permission.READ_MEDIA_VIDEO") == PackageManager.PERMISSION_GRANTED;
                if (Build.VERSION.SDK_INT >= 34) {
                    selected = checkSelfPermission("android.permission.READ_MEDIA_VISUAL_USER_SELECTED") == PackageManager.PERMISSION_GRANTED;
                }
            } else {
                images = videos = Build.VERSION.SDK_INT < Build.VERSION_CODES.M
                        || checkSelfPermission("android.permission.READ_EXTERNAL_STORAGE") == PackageManager.PERMISSION_GRANTED;
            }
            boolean canRead = images || videos || selected;
            // Full access decides first. Android 14 may report
            // READ_MEDIA_VISUAL_USER_SELECTED as granted alongside a full
            // grant, and asking about it first called such a device "limited"
            // -- the one state where the app must not nag the user about
            // access it already has.
            boolean full = images && videos;
            String status = !canRead ? "denied" : full ? "granted" : "limited";
            // "limited" is the state the user can do something about, so its
            // words are the user's, not the log's: under partial access
            // MediaStore answers only with the handful of items they picked,
            // which is why the folder list looks nearly empty.
            result.put("supported", true).put("status", status)
                    .put("detail", status.equals("granted") ? "full media access"
                            : status.equals("limited") ? "TDrive can only see the photos you picked. Choose sources again to let it see more."
                            : "Allow TDrive to access your photos and videos in Settings.")
                    .put("canRead", canRead)
                    .put("images", images).put("videos", videos).put("selectedOnly", selected)
                    .put("full", full);
        } catch (Exception e) {
            try { result.put("supported", false).put("status", "unavailable").put("canRead", false); } catch (Exception ignored) { }
        }
        return result;
    }

    /**
     * Ask for media access, and ask again when only some of it was given.
     *
     * The test is full access, not "can read anything at all". Under Android
     * 14's Select photos the app can read the few items the user picked, so a
     * canRead test returned early and there was no way back: every later
     * attempt short-circuited, the folder list stayed collapsed to whatever
     * those items happened to be in, and nothing in the app could change it.
     * Asking again puts the system's own "Select more photos" dialog in front
     * of the user, which is the only thing that can widen the grant.
     */
    public void requestPhotoBackupAccess(String callbackId) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M || photoBackupAccess().optBoolean("full")) {
            jsBridge.sendCallback(callbackId, photoBackupAccess().toString(), null);
            return;
        }
        synchronized (this) {
            if (pendingPhotoBackupPermissionCallbackId != null) {
                jsBridge.sendCallback(callbackId, null, "a media permission request is already open");
                return;
            }
            pendingPhotoBackupPermissionCallbackId = callbackId;
        }
        // READ_MEDIA_VISUAL_USER_SELECTED rides along from Android 14, where the
        // manifest declares it: without it in the request the system has no
        // reselection dialog to show a user who already chose Select photos.
        String[] permissions = Build.VERSION.SDK_INT >= 34
                ? new String[]{"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO", "android.permission.READ_MEDIA_VISUAL_USER_SELECTED"}
                : Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU
                ? new String[]{"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO"}
                : new String[]{"android.permission.READ_EXTERNAL_STORAGE"};
        runOnUiThread(() -> requestPermissions(permissions, PHOTO_BACKUP_PERMISSION_REQUEST));
    }

    public JSONObject photoBackupPolicyStatus() {
        JSONObject out = new JSONObject();
        try {
            Intent battery = registerSticky(Intent.ACTION_BATTERY_CHANGED);
            int level = battery == null ? -1 : battery.getIntExtra(BatteryManager.EXTRA_LEVEL, -1);
            int scale = battery == null ? -1 : battery.getIntExtra(BatteryManager.EXTRA_SCALE, -1);
            PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
            Network network = connectivityManager == null ? null : connectivityManager.getActiveNetwork();
            NetworkCapabilities caps = connectivityManager == null || network == null ? null : connectivityManager.getNetworkCapabilities(network);
            boolean connected = caps != null && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET);
            String type = caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) ? "wifi"
                    : caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) ? "cellular"
                    : connected ? "other" : "none";
            out.put("supported", true).put("status", connected ? "ready" : "offline").put("detail", connected ? "network available" : "no active network")
                    .put("batteryLevel", scale > 0 ? level / (double) scale : -1)
                    .put("lowPowerMode", pm != null && pm.isPowerSaveMode())
                    .put("wifi", "wifi".equals(type))
                    .put("network", new JSONObject().put("connected", connected).put("type", type)
                            .put("metered", caps != null && !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)));
        } catch (Exception e) {
            try { out.put("supported", false).put("status", "unavailable").put("detail", "system status unavailable"); } catch (Exception ignored) { }
        }
        return out;
    }

    public void listPhotoBackupAssets(String callbackId, String requestJson) {
        new Thread(() -> {
            try {
                if (!photoBackupAccess().optBoolean("canRead")) { jsBridge.sendCallback(callbackId, null, "media permission denied"); return; }
                JSONObject request = new JSONObject(requestJson);
                // A watched folder is the only kind of source there is: albums
                // and the whole-library source went with the picker that used
                // to offer them.
                String sourceId = request.optString("sourceId", "");
                TreeSource tree = TreeSource.parse(sourceId);
                if (tree == null) throw new IOException("unknown media source");
                int limit = Math.max(1, Math.min(PHOTO_BACKUP_PAGE_LIMIT, request.optInt("limit", PHOTO_BACKUP_PAGE_LIMIT)));
                JSONObject cursor = request.optJSONObject("cursor");
                long modified = cursor == null ? Long.MAX_VALUE : cursor.optLong("modified", Long.MAX_VALUE);
                long id = cursor == null ? Long.MAX_VALUE : cursor.optLong("id", Long.MAX_VALUE);
                String selection = MediaStore.Files.FileColumns.MEDIA_TYPE + " IN (?,?)";
                List<String> args = new ArrayList<>();
                args.add(String.valueOf(MediaStore.Files.FileColumns.MEDIA_TYPE_IMAGE)); args.add(String.valueOf(MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO));
                // A watched folder is a place, not a bucket: the folder itself
                // and everything under it, which is what the desktop walker
                // does with a chosen directory. The equality term is the
                // folder's own files, and the one an index can answer; the
                // pattern reaches its subfolders. A folder named "100%" is
                // escaped, or it would match half the volume.
                String column = treePathColumn();
                String prefix = tree.pathPrefix(this);
                if (prefix == null) throw new IOException("that folder is not available on this device");
                selection += " AND (" + column + "=? OR " + column + " LIKE ? ESCAPE '\\')";
                args.add(prefix); args.add(escapeLike(prefix) + "%");
                if (cursor != null) { selection += " AND (" + MediaStore.MediaColumns.DATE_MODIFIED + "<? OR (" + MediaStore.MediaColumns.DATE_MODIFIED + "=? AND " + MediaStore.MediaColumns._ID + "<?))"; args.add(String.valueOf(modified)); args.add(String.valueOf(modified)); args.add(String.valueOf(id)); }
                // DATE_TAKEN is the camera's clock and the only column that survives
                // an edit; DATE_ADDED is when MediaStore first saw the row, which is
                // the closest thing to it for media without EXIF.
                String[] base = {MediaStore.MediaColumns._ID, MediaStore.Files.FileColumns.MEDIA_TYPE, MediaStore.MediaColumns.DISPLAY_NAME, MediaStore.MediaColumns.MIME_TYPE, MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_MODIFIED, MediaStore.MediaColumns.DATE_ADDED, MediaStore.Images.ImageColumns.DATE_TAKEN};
                // RELATIVE_PATH does not exist before Android 10, and a
                // projection naming a column the provider does not know fails
                // the whole query rather than returning null for it, so the
                // location column is added rather than assumed.
                String[] projection = new String[base.length + 1];
                System.arraycopy(base, 0, projection, 0, base.length);
                projection[base.length] = treePathColumn();
                JSONArray assets = new JSONArray(); JSONObject next = null; long lastModified = 0; long lastId = 0;
                Uri collection = tree.collection();
                try (Cursor c = queryMedia(collection, projection, selection, args.toArray(new String[0]), limit + 1)) {
                    while (c != null && c.moveToNext()) {
                        if (assets.length() >= limit) { next = new JSONObject().put("modified", lastModified).put("id", lastId); break; }
                        long mediaId = c.getLong(0); boolean video = c.getInt(1) == MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO; long size = c.getLong(4); long changed = c.getLong(5);
                        long taken = c.isNull(7) ? 0L : c.getLong(7);
                        long createdAt = taken > 0 ? taken : c.getLong(6) * 1000L;
                        // The id is MediaStore's, the same one an album source
                        // reports for the same file, so a photo that is in
                        // both a watched folder and a chosen album is one
                        // upload rather than two. relDir is the only thing
                        // that differs, and only a folder source has one.
                        assets.put(new JSONObject().put("id", "media:" + (video ? "video:" : "image:") + mediaId)
                                .put("version", changed + ":" + size).put("name", c.isNull(2) ? "media" : c.getString(2))
                                .put("mediaType", video ? "video" : "image").put("mimeType", c.isNull(3) ? "" : c.getString(3))
                                .put("resourceID", "media:" + (video ? "video:" : "image:") + mediaId)
                                .put("relDir", tree.relativeDir(c.isNull(base.length) ? "" : c.getString(base.length)))
                                .put("size", size).put("modifiedAt", changed * 1000L).put("createdAt", createdAt).put("sourceId", sourceId));
                        lastModified = changed; lastId = mediaId;
                    }
                }
                JSONObject answer = new JSONObject().put("assets", assets).put("nextCursor", next == null ? JSONObject.NULL : next);
                jsBridge.sendCallback(callbackId, answer.toString(), null);
            } catch (Exception e) { Log.e(TAG, "Media asset listing failed", e); jsBridge.sendCallback(callbackId, null, "could not list media assets"); }
        }).start();
    }

    /** Uses provider-supported query arguments on modern Android. Never embed
     * LIMIT in a SQL sort string, which scoped-storage providers may reject. */
    @Nullable
    private Cursor queryMedia(Uri uri, String[] projection, String selection, String[] selectionArgs, int limit) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Bundle args = new Bundle();
            args.putString(ContentResolver.QUERY_ARG_SQL_SELECTION, selection);
            args.putStringArray(ContentResolver.QUERY_ARG_SQL_SELECTION_ARGS, selectionArgs);
            args.putStringArray(ContentResolver.QUERY_ARG_SORT_COLUMNS,
                    new String[]{MediaStore.MediaColumns.DATE_MODIFIED, MediaStore.MediaColumns._ID});
            args.putInt(ContentResolver.QUERY_ARG_SORT_DIRECTION, ContentResolver.QUERY_SORT_DIRECTION_DESCENDING);
            if (limit > 0) args.putInt(ContentResolver.QUERY_ARG_LIMIT, limit);
            return getContentResolver().query(uri, projection, args, null);
        }
        return getContentResolver().query(uri, projection, selection, selectionArgs,
                MediaStore.MediaColumns.DATE_MODIFIED + " DESC, " + MediaStore.MediaColumns._ID + " DESC");
    }

    public void materializePhotoBackupAsset(String callbackId, String requestJson) {
        new Thread(() -> {
            MediaRef ref = null;
            AtomicBoolean cancelled = null;
            File temporary = null;
            boolean staged = false;
            try {
                JSONObject request = new JSONObject(requestJson); ref = MediaRef.parse(request.optString("id", ""));
                JSONObject access = photoBackupAccess();
                if (ref == null || !(access.optBoolean(ref.video ? "videos" : "images") || access.optBoolean("selectedOnly"))) throw new IOException("media asset is unavailable");
                cancelled = new AtomicBoolean(false);
                synchronized (photoBackupStageLock) {
                    if (activePhotoBackupStageKey != null) throw new IOException("another media asset is staged or staging; release it first");
                    activePhotoBackupStageKey = ref.key();
                    stagingPhotoBackupAssets.put(ref.key(), cancelled);
                }
                String expected = request.optString("version", ""); Uri uri = ContentUris.withAppendedId(MediaStore.Files.getContentUri("external"), ref.id);
                String[] p = {MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_MODIFIED, MediaStore.MediaColumns.DISPLAY_NAME, MediaStore.Files.FileColumns.MEDIA_TYPE};
                try (Cursor c = getContentResolver().query(uri, p, null, null, null)) {
                    if (c == null || !c.moveToFirst() || c.getInt(3) != (ref.video ? MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO : MediaStore.Files.FileColumns.MEDIA_TYPE_IMAGE)) throw new IOException("media asset no longer exists");
                    long size = c.getLong(0); String version = c.getLong(1) + ":" + size;
                    if (!expected.equals(version)) throw new IOException("media asset changed; rediscover it before upload");
                    if (size < 0 || size > PHOTO_BACKUP_STAGE_MAX_BYTES) throw new IOException("media asset exceeds the 4 GB staging limit");
                    String displayName = safeName(c.isNull(2) ? "media" : c.getString(2));
                    File root = photoBackupStageRoot(); if (!root.isDirectory() && !root.mkdirs()) throw new IOException("could not create staging area");
                    File dir = new File(root, ref.directoryName()); if (!dir.isDirectory() && !dir.mkdirs()) throw new IOException("could not create staging area");
                    long existingBytes = stageBytes(root);
                    if (existingBytes > PHOTO_BACKUP_STAGE_MAX_BYTES || size > PHOTO_BACKUP_STAGE_MAX_BYTES - existingBytes) throw new IOException("media staging would exceed the 4 GB cache limit");
                    requireStagingSpace(root, size);
                    File out = new File(dir, displayName); temporary = new File(dir, "." + displayName + ".partial");
                    // Only the stale file, never discard(): this runs *before*
                    // the copy, and discard() also removes the parent once it is
                    // empty -- which a directory just created for this asset
                    // always is. That deleted the staging directory out from
                    // under the stream below, and every first attempt at an
                    // asset failed with ENOENT on its own .partial file.
                    temporary.delete();
                    copyCapped(uri, temporary, PHOTO_BACKUP_STAGE_MAX_BYTES - existingBytes, cancelled, root);
                    if (cancelled.get()) throw new IOException("media staging cancelled");
                    String after = mediaVersion(uri, ref.video);
                    if (!expected.equals(after)) throw new IOException("media asset changed during staging; rediscover it before upload");
                    File previous = stagedPhotoBackupAssets.remove(ref.key()); if (previous != null) discard(previous);
                    if (!temporary.renameTo(out)) throw new IOException("could not finalize staged media asset");
                    if (cancelled.get()) { discard(out); throw new IOException("media staging cancelled"); }
                    stagedPhotoBackupAssets.put(ref.key(), out);
                    staged = true;
                    jsBridge.sendCallback(callbackId, new JSONObject().put("id", ref.key()).put("version", version).put("path", out.getAbsolutePath()).toString(), null);
                }
            } catch (Exception e) { Log.e(TAG, "Media staging failed", e); jsBridge.sendCallback(callbackId, null, e.getMessage() == null ? "could not stage media asset" : e.getMessage()); }
            finally {
                if (!staged && temporary != null) discard(temporary);
                if (ref != null && cancelled != null) {
                    stagingPhotoBackupAssets.remove(ref.key(), cancelled);
                    synchronized (photoBackupStageLock) {
                        if (ref.key().equals(activePhotoBackupStageKey) && !staged) activePhotoBackupStageKey = null;
                    }
                }
            }
        }).start();
    }

    public void releasePhotoBackupAsset(String callbackId, String requestJson) {
        new Thread(() -> { try { MediaRef ref = MediaRef.parse(new JSONObject(requestJson).optString("id", "")); if (ref != null) { AtomicBoolean cancel = stagingPhotoBackupAssets.get(ref.key()); if (cancel != null) { cancel.set(true); discardTree(new File(photoBackupStageRoot(), ref.directoryName())); } else { File f = stagedPhotoBackupAssets.remove(ref.key()); if (f != null) discard(f); discardTree(new File(photoBackupStageRoot(), ref.directoryName())); synchronized (photoBackupStageLock) { if (ref.key().equals(activePhotoBackupStageKey)) activePhotoBackupStageKey = null; } } } } catch (Exception ignored) { } jsBridge.sendCallback(callbackId, "", null); }).start();
    }

    private void requireStagingSpace(File directory, long expectedBytes) throws IOException {
        long available = new StatFs(directory.getAbsolutePath()).getAvailableBytes();
        if (available < PHOTO_BACKUP_STAGE_FREE_HEADROOM_BYTES || expectedBytes > available - PHOTO_BACKUP_STAGE_FREE_HEADROOM_BYTES) {
            throw new IOException("not enough storage to stage this media asset; keep at least 512 MB free");
        }
    }

    // Wails Android StoragePath is Context.getFilesDir(); Go's datadir.CacheDir
    // is StoragePath/TDrive. Keeping this under that exact root makes the
    // resolved path verifiable by Go without exposing Context cache paths.
    private File photoBackupStageRoot() { return new File(new File(getFilesDir(), "TDrive"), "photo-backup-stage"); }

    private long stageBytes(File root) {
        if (!root.isDirectory()) return 0;
        long total = 0;
        File[] entries = root.listFiles();
        if (entries == null) return 0;
        for (File entry : entries) {
            File[] files = entry.isDirectory() ? entry.listFiles() : null;
            if (files == null) { total += entry.length(); continue; }
            for (File file : files) total += Math.max(0, file.length());
        }
        return total;
    }

    /** The directory contains only one level of per-asset safe names. */
    private void discardTree(File directory) {
        File[] files = directory.listFiles();
        if (files != null) for (File file : files) file.delete();
        directory.delete();
    }

    /** Recover after a killed process without recursively touching arbitrary paths. */
    private void cleanupPhotoBackupStage() {
        File root = photoBackupStageRoot();
        File[] entries = root.listFiles();
        if (entries == null) return;
        int count = 0;
        for (File entry : entries) {
            if (count++ >= 256) break;
            if (entry.isDirectory()) discardTree(entry); else entry.delete();
        }
        root.delete();
    }

    private String mediaVersion(Uri uri, boolean video) throws IOException {
        String[] projection = {MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_MODIFIED, MediaStore.Files.FileColumns.MEDIA_TYPE};
        try (Cursor c = getContentResolver().query(uri, projection, null, null, null)) {
            if (c == null || !c.moveToFirst() || c.getInt(2) != (video ? MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO : MediaStore.Files.FileColumns.MEDIA_TYPE_IMAGE)) throw new IOException("media asset no longer exists");
            return c.getLong(1) + ":" + c.getLong(0);
        }
    }

    private void copyCapped(Uri uri, File out, long max, AtomicBoolean cancelled, File directory) throws IOException {
        long total = 0;
        try (InputStream in = getContentResolver().openInputStream(uri); OutputStream os = new FileOutputStream(out)) {
            if (in == null) throw new IOException("media asset cannot be opened");
            byte[] buffer = new byte[64 * 1024];
            for (int read; (read = in.read(buffer)) > 0;) {
                if (cancelled.get() || Thread.currentThread().isInterrupted()) throw new IOException("media staging cancelled");
                total += read;
                if (total > max) throw new IOException("media asset exceeds the 4 GB staging limit");
                // Querying storage every 4 MiB catches other app writes without
                // making a multi-gigabyte copy spend its time in StatFs.
                if ((total & ((4L * 1024L * 1024L) - 1)) < read) requireStagingSpace(directory, 0);
                os.write(buffer, 0, read);
            }
            requireStagingSpace(directory, 0);
        } catch (IOException e) { discard(out); throw e; }
    }

    private static final class MediaRef {
        final boolean video; final long id; MediaRef(boolean video, long id) { this.video = video; this.id = id; }
        static MediaRef parse(String value) { try { String[] parts = value.split(":"); if (parts.length != 3 || !"media".equals(parts[0]) || (!"image".equals(parts[1]) && !"video".equals(parts[1]))) return null; long id = Long.parseLong(parts[2]); return id > 0 ? new MediaRef("video".equals(parts[1]), id) : null; } catch (Exception e) { return null; } }
        String key() { return "media:" + (video ? "video:" : "image:") + id; } String directoryName() { return (video ? "video-" : "image-") + id; }
    }

    // ---- Watched folders -------------------------------------------------
    //
    // A folder the user picks for backup is read through MediaStore, not
    // through the document tree the picker returns.
    //
    // The Storage Access Framework would work -- its grant can even be made
    // persistable -- but it answers with documents, and a document has no
    // MediaStore id. The same photo reached through an album and through a
    // picked folder would then carry two different identities, and the ledger,
    // which dedupes on identity, would upload it twice. Converting the tree to
    // the place it denotes and reading that place the way every other source
    // is read keeps one photo one photo. It also leaves nothing to persist and
    // no grant to lose: the source is a string, and the media permission the
    // app already holds is what reads it.
    //
    // The cost is honest and bounded: this backs up the photos and videos
    // MediaStore knows about, not every file in the folder, and only folders
    // that live on the device. A folder from a cloud provider is refused where
    // it is picked rather than accepted and quietly skipped.

    /** Which column names a row's location, by what the OS offers. */
    private static String treePathColumn() {
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
                ? MediaStore.MediaColumns.RELATIVE_PATH : "_data";
    }

    /** Escapes a LIKE prefix so a folder named "100%" is not a wildcard. */
    private static String escapeLike(String value) {
        return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_");
    }

    /**
     * A folder chosen for backup: the media volume it sits on and its place on
     * that volume. Its source id, "tree:<volume>:<path>/", is derived from the
     * folder itself, so picking the same folder twice names the same source
     * instead of opening a second one over the same files.
     */
    private static final class TreeSource {
        final String volume;   // "external_primary", or a card's lowercased UUID
        final String relative; // "DCIM/Camera/", always with a trailing slash

        private TreeSource(String volume, String relative) { this.volume = volume; this.relative = relative; }

        @Nullable
        static TreeSource parse(String sourceId) {
            if (sourceId == null || !sourceId.startsWith("tree:")) return null;
            int split = sourceId.indexOf(':', 5);
            if (split < 0) return null;
            String volume = sourceId.substring(5, split);
            String relative = sourceId.substring(split + 1);
            if (volume.isEmpty() || relative.isEmpty() || !relative.endsWith("/") || relative.contains("..")) return null;
            return new TreeSource(volume, relative);
        }

        static String sourceId(String volume, String relative) { return "tree:" + volume + ":" + relative; }

        Uri collection() {
            return Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
                    ? MediaStore.Files.getContentUri(volume) : MediaStore.Files.getContentUri("external");
        }

        /**
         * What the location column holds for files directly in this folder, or
         * null when this device cannot express it -- a card before Android 10,
         * where only the primary volume has a knowable path.
         */
        @Nullable
        String pathPrefix(Context context) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) return relative;
            if (!MediaStore.VOLUME_EXTERNAL_PRIMARY.equals(volume)) return null;
            File external = android.os.Environment.getExternalStorageDirectory();
            return external == null ? null : external.getAbsolutePath() + "/" + relative;
        }

        /**
         * The folders between this source and one of its rows, "/" separated
         * and empty for a file sitting directly in it. Before Android 10 the
         * column is the file's own path, so its name comes off first.
         */
        String relativeDir(String located) {
            if (located == null || located.isEmpty()) return "";
            String directory = located;
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
                int slash = directory.lastIndexOf('/');
                directory = slash < 0 ? "" : directory.substring(0, slash + 1);
            }
            if (!directory.endsWith("/")) directory = directory + "/";
            String prefix = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q ? relative : null;
            if (prefix == null) {
                // Pre-Q absolute path: everything up to and including the
                // source's own relative part is the prefix.
                int at = directory.indexOf("/" + relative);
                if (at < 0) return "";
                directory = directory.substring(at + 1);
                prefix = relative;
            }
            if (!directory.startsWith(prefix)) return "";
            String rest = directory.substring(prefix.length());
            while (rest.endsWith("/")) rest = rest.substring(0, rest.length() - 1);
            return rest;
        }
    }

    /**
     * Ask for a folder to back up, and answer with the source it becomes:
     * {"id","name","root","kind"}, or "" when the picker was dismissed.
     *
     * Deliberately not launchFolderPicker: that one holds a single document
     * tree for the import flow and releases the previous grant every time it
     * runs, so sharing it would revoke a folder import in mid-upload. Nothing
     * is held here at all -- the chosen tree is converted to a place and let
     * go of before this returns.
     */
    public void pickPhotoBackupFolder(String callbackId) {
        synchronized (this) {
            if (pendingPhotoBackupFolderCallbackId != null) {
                jsBridge.sendCallback(callbackId, null, "a folder picker is already open");
                return;
            }
            pendingPhotoBackupFolderCallbackId = callbackId;
        }
        try {
            startActivityForResult(new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE), PHOTO_BACKUP_FOLDER_REQUEST);
        } catch (Exception e) {
            Log.e(TAG, "Failed to launch the backup folder picker", e);
            pendingPhotoBackupFolderCallbackId = null;
            jsBridge.sendCallback(callbackId, null, "no folder picker on this device");
        }
    }

    private void handlePhotoBackupFolderResult(int resultCode, @Nullable Intent data) {
        String callbackId;
        synchronized (this) { callbackId = pendingPhotoBackupFolderCallbackId; pendingPhotoBackupFolderCallbackId = null; }
        if (callbackId == null) return;
        Uri tree = resultCode == RESULT_OK && data != null ? data.getData() : null;
        if (tree == null) { jsBridge.sendCallback(callbackId, "", null); return; }
        try {
            jsBridge.sendCallback(callbackId, describePickedBackupFolder(tree).toString(), null);
        } catch (IOException e) {
            jsBridge.sendCallback(callbackId, null, e.getMessage());
        } catch (Exception e) {
            Log.e(TAG, "Could not use the chosen backup folder", e);
            jsBridge.sendCallback(callbackId, null, "could not use that folder");
        }
    }

    /**
     * Turns a picked document tree into a backup source, or explains why it
     * cannot be one. Only the device's own storage provider denotes a place
     * MediaStore indexes; a tree from Drive or another app is a set of
     * documents that exist nowhere on this device.
     */
    private JSONObject describePickedBackupFolder(Uri tree) throws IOException, JSONException {
        if (!"com.android.externalstorage.documents".equals(tree.getAuthority())) {
            throw new IOException("TDrive backs up folders stored on this device. Choose one in internal storage or on the SD card.");
        }
        String docId = DocumentsContract.getTreeDocumentId(tree);
        int split = docId == null ? -1 : docId.indexOf(':');
        if (split < 0) throw new IOException("could not read that folder");
        String root = docId.substring(0, split);
        String path = docId.substring(split + 1);
        while (path.startsWith("/")) path = path.substring(1);
        while (path.endsWith("/")) path = path.substring(0, path.length() - 1);
        if (path.isEmpty()) {
            throw new IOException("Choose a folder inside your storage, or use All photos and videos.");
        }
        String volume = "primary".equals(root)
                ? MediaStore.VOLUME_EXTERNAL_PRIMARY : root.toLowerCase(Locale.US);
        TreeSource source = new TreeSource(volume, path + "/");
        if (source.pathPrefix(this) == null) {
            throw new IOException("This version of Android can only back up folders in internal storage.");
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
                && !MediaStore.getExternalVolumeNames(this).contains(volume)) {
            throw new IOException("That storage is not available. Reinsert the card and try again.");
        }
        String name = path.substring(path.lastIndexOf('/') + 1);
        return new JSONObject()
                .put("id", TreeSource.sourceId(volume, source.relative))
                .put("root", volume + ":" + source.relative)
                .put("name", name)
                .put("kind", "device-folder");
    }

    public void launchFolderPicker(String callbackId) {
        synchronized (this) {
            if (pendingFolderCallbackId != null) {
                // One at a time: a second tree arriving for the first one's
                // callback would replace the manifest the uploader is working
                // through.
                jsBridge.sendCallback(callbackId, null, "a folder picker is already open");
                return;
            }
            pendingFolderCallbackId = callbackId;
        }
        try {
            startActivityForResult(new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE), FOLDER_PICKER_REQUEST);
        } catch (Exception e) {
            Log.e(TAG, "Failed to launch folder picker", e);
            pendingFolderCallbackId = null;
            jsBridge.sendCallback(callbackId, null, "no folder picker on this device");
        }
    }

    /**
     * Launch the system document picker. Results are copied into the app's
     * cache directory so Go receives real filesystem paths. Called by
     * WailsBridge on the main thread.
     */
    public void launchFilePicker(int callbackID, boolean multiple) {
        synchronized (this) {
            if (pendingFilePickerCallbackID != -1) {
                // Only one picker can be in flight
                bridge.filePickerDone(callbackID);
                return;
            }
            pendingFilePickerCallbackID = callbackID;
        }

        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.setType("*/*");
        intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, multiple);
        try {
            startActivityForResult(intent, FILE_PICKER_REQUEST);
        } catch (Exception e) {
            Log.e(TAG, "Failed to launch file picker", e);
            pendingFilePickerCallbackID = -1;
            bridge.filePickerDone(callbackID);
        }
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, @Nullable Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == PHOTO_CAPTURE_REQUEST || requestCode == VIDEO_CAPTURE_REQUEST) {
            handleCaptureResult(resultCode, data);
            return;
        }
        if (requestCode == FOLDER_PICKER_REQUEST) {
            handleFolderPickerResult(resultCode, data);
            return;
        }
        if (requestCode == PHOTO_BACKUP_FOLDER_REQUEST) {
            handlePhotoBackupFolderResult(resultCode, data);
            return;
        }
        if (requestCode != FILE_PICKER_REQUEST) {
            return;
        }
        final int callbackID = pendingFilePickerCallbackID;
        pendingFilePickerCallbackID = -1;
        if (callbackID == -1) {
            return;
        }

        final List<Uri> uris = new ArrayList<>();
        if (resultCode == RESULT_OK && data != null) {
            if (data.getClipData() != null) {
                for (int i = 0; i < data.getClipData().getItemCount(); i++) {
                    uris.add(data.getClipData().getItemAt(i).getUri());
                }
            } else if (data.getData() != null) {
                uris.add(data.getData());
            }
        }

        copyPickedDocuments(callbackID, uris);
    }

    /**
     * Copies the picked documents into the cache and hands the resulting paths
     * to Go, which cannot see a selection until every one of them has landed
     * (Wails drains the results channel until it closes).
     *
     * Two things matter here, and both are about the seconds this costs. The
     * picker activity is already gone by the time this runs, so unlike iOS --
     * where UIKit copies behind its own document picker -- every millisecond is
     * spent on TDrive's own screen. So it reports progress, and the app turns
     * that into a transfer row rather than leaving the screen looking frozen.
     *
     * And it copies several at once. Serially, a selection cost the sum of its
     * files; the work is almost entirely waiting on a ContentProvider, which is
     * a network fetch when the document lives in a cloud provider, so overlapping
     * them turns that sum into roughly its longest member.
     */
    private void copyPickedDocuments(final int callbackID, final List<Uri> uris) {
        final int total = uris.size();
        if (total == 0) {
            bridge.filePickerDone(callbackID);
            return;
        }

        // Announce the size of the job before any of it is done, so the app can
        // say "0 of 7" immediately rather than after the first file lands.
        emitPickerProgress(0, total, false);

        new Thread(() -> {
            // Indexed rather than appended: the copies finish out of order, but
            // the user picked these in an order and the uploads should follow it.
            final String[] copied = new String[total];
            final AtomicInteger done = new AtomicInteger();
            final AtomicLong lastReport = new AtomicLong();
            final ExecutorService pool =
                    Executors.newFixedThreadPool(Math.min(PICKER_COPY_CONCURRENCY, total));
            try {
                final List<Callable<Void>> jobs = new ArrayList<>(total);
                for (int i = 0; i < total; i++) {
                    final int index = i;
                    jobs.add(() -> {
                        try {
                            copied[index] = copyUriToCache(uris.get(index));
                        } finally {
                            // Counted even when the copy failed: this reports how
                            // much of the wait is left, not how much succeeded.
                            reportPickerProgress(done.incrementAndGet(), total, lastReport);
                        }
                        return null;
                    });
                }
                pool.invokeAll(jobs); // returns once every copy has finished
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            } finally {
                pool.shutdownNow();
            }

            emitPickerProgress(done.get(), total, true);
            for (String path : copied) {
                if (path != null) {
                    bridge.filePickerResult(callbackID, path);
                }
            }
            bridge.filePickerDone(callbackID);
        }).start();
    }

    /**
     * Rate-limits progress so a large selection cannot spend its time crossing
     * JNI. The final tick is always delivered by the caller, so dropping ticks
     * here can only cost intermediate frames, never the end of the row.
     */
    private void reportPickerProgress(int done, int total, AtomicLong lastReport) {
        final long now = System.currentTimeMillis();
        final long previous = lastReport.get();
        if (now - previous < PICKER_PROGRESS_INTERVAL_MS && done < total) {
            return;
        }
        if (!lastReport.compareAndSet(previous, now)) {
            return; // another copy just reported; one tick per interval is enough
        }
        emitPickerProgress(done, total, false);
    }

    private void emitPickerProgress(int done, int total, boolean finished) {
        try {
            bridge.emitEvent("common:filepicker", new JSONObject()
                    .put("phase", finished ? "done" : "copying")
                    .put("done", done)
                    .put("total", total)
                    .toString());
        } catch (JSONException e) {
            Log.w(TAG, "Could not report file picker progress", e);
        }
    }

    private void handleFolderPickerResult(int resultCode, @Nullable Intent data) {
        final String callbackId = pendingFolderCallbackId;
        pendingFolderCallbackId = null;
        if (callbackId == null) {
            return;
        }
        final Uri tree = (resultCode == RESULT_OK && data != null) ? data.getData() : null;
        if (tree == null) {
            // Dismissed. An empty answer is the answer, not an error: the reader
            // changed their mind, and nothing went wrong. An empty folder is a
            // manifest with no files, which is a different thing entirely.
            jsBridge.sendCallback(callbackId, "", null);
            return;
        }
        // Off the main thread: the walk costs two round trips to the provider per
        // directory, which on a deep folder is long enough to freeze the screen.
        new Thread(() -> {
            try {
                jsBridge.sendCallback(callbackId, readTree(tree).toString(), null);
            } catch (Exception e) {
                Log.e(TAG, "Failed to read the chosen folder", e);
                jsBridge.sendCallback(callbackId, null, "could not read that folder");
            }
        }).start();
    }

    /**
     * Lists every file in a chosen tree as {"root","files":[{"id","rel","size"}]}.
     * The ids are this pick's own, and stay answerable until the next one.
     */
    private JSONObject readTree(Uri tree) throws JSONException {
        holdTree(tree);
        String rootDocId = DocumentsContract.getTreeDocumentId(tree);
        JSONArray files = new JSONArray();
        collectTree(tree, rootDocId, "", files);
        JSONObject manifest = new JSONObject();
        manifest.put("root", treeDisplayName(tree, rootDocId));
        manifest.put("files", files);
        return manifest;
    }

    /**
     * Takes the tree for keeps and drops the one before it. The grant the picker
     * returns dies with the activity, and a large folder is uploaded over far
     * longer than that, so every id in the manifest would stop opening partway
     * through.
     */
    private void holdTree(Uri tree) {
        if (pickedTree != null) {
            try {
                getContentResolver().releasePersistableUriPermission(
                        pickedTree, Intent.FLAG_GRANT_READ_URI_PERMISSION);
            } catch (SecurityException ignored) {
            }
        }
        try {
            getContentResolver().takePersistableUriPermission(
                    tree, Intent.FLAG_GRANT_READ_URI_PERMISSION);
        } catch (SecurityException e) {
            // Not every provider offers one. The transient grant still covers a
            // folder that finishes uploading while the app is up.
            Log.w(TAG, "No persistable permission for the chosen folder", e);
        }
        pickedTree = tree;
        pickedFiles.clear();
        pickedCache = new File(getCacheDir(), "wails-folder/" + System.nanoTime());
    }

    private void collectTree(Uri tree, String parentDocId, String prefix, JSONArray files)
            throws JSONException {
        Uri children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, parentDocId);
        try (Cursor cursor = getContentResolver().query(children, new String[]{
                DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                DocumentsContract.Document.COLUMN_MIME_TYPE,
                DocumentsContract.Document.COLUMN_SIZE,
        }, null, null, null)) {
            if (cursor == null) {
                return;
            }
            while (cursor.moveToNext()) {
                String docId = cursor.getString(0);
                String rawName = cursor.getString(1);
                if (docId == null || rawName == null || rawName.isEmpty()) {
                    continue;
                }
                String rel = prefix + safeName(rawName);
                if (DocumentsContract.Document.MIME_TYPE_DIR.equals(cursor.getString(2))) {
                    collectTree(tree, docId, rel + "/", files);
                    continue;
                }
                String id = String.valueOf(files.length());
                pickedFiles.put(id, new PickedFile(docId, rel));
                files.put(new JSONObject()
                        .put("id", id)
                        .put("rel", rel)
                        .put("size", cursor.isNull(3) ? 0 : cursor.getLong(3)));
            }
        }
    }

    /**
     * Copies the named documents into the cache and answers with where they
     * landed, as {"paths":{"<id>":"/abs/path"}}. An id that will not open is left
     * out rather than failing the batch, so one unreadable file does not stop the
     * rest of the folder.
     */
    public void materializeFiles(String callbackId, String idsJson) {
        new Thread(() -> {
            JSONObject paths = new JSONObject();
            try {
                JSONArray ids = new JSONArray(idsJson);
                for (int i = 0; i < ids.length(); i++) {
                    String id = ids.getString(i);
                    File copy = materialize(id);
                    if (copy != null) {
                        paths.put(id, copy.getAbsolutePath());
                    }
                }
                jsBridge.sendCallback(callbackId, new JSONObject().put("paths", paths).toString(), null);
            } catch (JSONException e) {
                Log.e(TAG, "Malformed file list", e);
                jsBridge.sendCallback(callbackId, null, "malformed file list");
            }
        }).start();
    }

    /** Deletes the cached copies of the named documents. Unknown ids are fine. */
    public void releaseFiles(String callbackId, String idsJson) {
        new Thread(() -> {
            try {
                JSONArray ids = new JSONArray(idsJson);
                for (int i = 0; i < ids.length(); i++) {
                    String id = ids.getString(i);
                    PickedFile file = pickedFiles.get(id);
                    if (file != null) {
                        discard(cacheSlot(id, file));
                    }
                }
            } catch (JSONException e) {
                // A list that will not parse names no cached file, so there is
                // nothing to delete and nothing to report.
                Log.w(TAG, "Malformed release list", e);
            }
            jsBridge.sendCallback(callbackId, "", null);
        }).start();
    }

    /**
     * Moves a finished download out of the sandbox and into the phone's public
     * Downloads folder, and answers {"location":"Download/plan.pdf"} with the
     * name it really got.
     *
     * The Go side writes downloads under the app's own files directory, which
     * since Android 11 no file manager will browse, so until this runs a
     * finished download is somewhere the reader cannot reach.
     */
    public void saveToDownloads(String callbackId, String json) {
        String path;
        try {
            path = new JSONObject(json).optString("path", "");
        } catch (JSONException e) {
            jsBridge.sendCallback(callbackId, null, "malformed save request");
            return;
        }
        if (path.isEmpty()) {
            jsBridge.sendCallback(callbackId, null, "nothing to save");
            return;
        }
        // Before Android 10 the Downloads folder was an ordinary folder and
        // writing to it needed asking. From 10 on, MediaStore owns it and no
        // permission applies.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M
                && Build.VERSION.SDK_INT < Build.VERSION_CODES.Q
                && checkSelfPermission("android.permission.WRITE_EXTERNAL_STORAGE")
                != PackageManager.PERMISSION_GRANTED) {
            final String asked = path;
            runOnUiThread(() -> {
                pendingSaveCallbackId = callbackId;
                pendingSavePath = asked;
                requestPermissions(
                        new String[]{"android.permission.WRITE_EXTERNAL_STORAGE"}, SAVE_PERMISSION_REQUEST);
            });
            return;
        }
        runSaveToDownloads(callbackId, path);
    }

    /** A folder download can be gigabytes, so none of this happens on the main thread. */
    private void runSaveToDownloads(String callbackId, String path) {
        new Thread(() -> {
            try {
                String location = DownloadExport.save(this, new File(path));
                jsBridge.sendCallback(
                        callbackId, new JSONObject().put("location", location).toString(), null);
            } catch (Exception e) {
                Log.e(TAG, "Failed to save " + path + " to Downloads", e);
                String why = e.getMessage();
                jsBridge.sendCallback(
                        callbackId, null, why == null || why.isEmpty() ? "could not save to Downloads" : why);
            }
        }).start();
    }

    @Nullable
    private File materialize(String id) {
        PickedFile file = pickedFiles.get(id);
        if (file == null || pickedTree == null) {
            return null;
        }
        File out = cacheSlot(id, file);
        File slot = out.getParentFile();
        try {
            if (!slot.isDirectory() && !slot.mkdirs()) {
                return null;
            }
            copyDocument(DocumentsContract.buildDocumentUriUsingTree(pickedTree, file.docId), out);
        } catch (Exception e) {
            Log.e(TAG, "Failed to copy " + file.rel, e);
            discard(out);
            return null;
        }
        return out;
    }

    /**
     * Where one document's copy lives. Each id gets a directory of its own
     * because the copy has to keep the name the manifest reported, which is what
     * the drive ends up calling it, and two folders in one batch can easily hold
     * the same filename.
     */
    private File cacheSlot(String id, PickedFile file) {
        return new File(pickedCache, id + "/" + file.rel.substring(file.rel.lastIndexOf('/') + 1));
    }

    /**
     * Removes a staged copy and the slot directory it was the last occupant of.
     *
     * Cleanup only, once nothing more will be written to that directory: the
     * parent delete is what keeps the picker's one-directory-per-file cache from
     * accumulating empty slots, and File.delete() on a directory is a no-op
     * while anything else is still in it. Clearing a stale file that is about to
     * be rewritten is not this -- use File.delete() for that, or the directory
     * goes with it.
     */
    private void discard(File copy) {
        copy.delete();
        File slot = copy.getParentFile();
        if (slot != null) {
            slot.delete();
        }
    }

    /** One file in the picked tree: where to read it, and what to call the copy. */
    private static final class PickedFile {
        final String docId;
        final String rel;

        PickedFile(String docId, String rel) {
            this.docId = docId;
            this.rel = rel;
        }
    }

    private String treeDisplayName(Uri tree, String docId) {
        Uri self = DocumentsContract.buildDocumentUriUsingTree(tree, docId);
        try (Cursor cursor = getContentResolver().query(
                self, new String[]{DocumentsContract.Document.COLUMN_DISPLAY_NAME}, null, null, null)) {
            if (cursor != null && cursor.moveToFirst()) {
                String name = cursor.getString(0);
                if (name != null && !name.isEmpty()) {
                    return safeName(name);
                }
            }
        } catch (Exception ignored) {
        }
        return "folder";
    }

    /**
     * A display name is whatever the provider chose to call the document, so it
     * is reduced to a bare filename before it is ever joined to a path. Left
     * alone, a name carrying separators would write outside the folder it
     * belongs to.
     */
    private String safeName(String name) {
        String bare = new File(name).getName().trim();
        if (bare.isEmpty() || bare.equals(".") || bare.equals("..")) {
            return "item";
        }
        return bare;
    }

    /** Streams one document out of the provider and into the cache. */
    private void copyDocument(Uri uri, File out) throws IOException {
        try (InputStream in = getContentResolver().openInputStream(uri);
             OutputStream os = new FileOutputStream(out)) {
            if (in == null) {
                throw new IOException("nothing to read from " + uri);
            }
            byte[] buf = new byte[64 * 1024];
            int n;
            while ((n = in.read(buf)) > 0) {
                os.write(buf, 0, n);
            }
        }
    }

    /**
     * Copy a content URI into the app cache and return its filesystem path.
     */
    @Nullable
    private String copyUriToCache(Uri uri) {
        String name = "document";
        try (Cursor cursor = getContentResolver().query(uri, null, null, null, null)) {
            if (cursor != null && cursor.moveToFirst()) {
                int idx = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (idx >= 0 && cursor.getString(idx) != null) {
                    name = new File(cursor.getString(idx)).getName();
                }
            }
        } catch (Exception ignored) {
        }

        try {
            File dir = new File(getCacheDir(), "wails-picker/" + System.nanoTime());
            if (!dir.mkdirs()) {
                return null;
            }
            File out = new File(dir, name);
            try (InputStream in = getContentResolver().openInputStream(uri);
                 OutputStream os = new FileOutputStream(out)) {
                if (in == null) {
                    return null;
                }
                byte[] buf = new byte[64 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) {
                    os.write(buf, 0, n);
                }
            }
            return out.getAbsolutePath();
        } catch (Exception e) {
            Log.e(TAG, "Failed to copy picked document", e);
            return null;
        }
    }

    /**
     * Execute JavaScript in the WebView from the Go side
     */
    public void executeJavaScript(final String js) {
        runOnUiThread(() -> {
            if (webView != null) {
                webView.evaluateJavascript(js, null);
            }
        });
    }

    // ---- System events ---------------------------------------------------
    // Battery/power, screen lock and network connectivity are surfaced to JS as
    // "system:*" events. The OS broadcasts used here (ACTION_BATTERY_CHANGED,
    // SCREEN_OFF, USER_PRESENT, POWER_SAVE_MODE_CHANGED) are protected system
    // broadcasts, so dynamic registration needs no RECEIVER_* export flag.

    private void registerSystemEventReceivers() {
        // Battery + charging state (sticky broadcast: the current value is
        // delivered to the receiver immediately on registration).
        batteryReceiver = new BroadcastReceiver() {
            @Override public void onReceive(Context context, Intent intent) {
                emitBattery(intent);
            }
        };
        registerReceiver(batteryReceiver, new IntentFilter(Intent.ACTION_BATTERY_CHANGED));

        // Low-power (battery saver) mode toggles → re-emit battery with the flag.
        powerSaveReceiver = new BroadcastReceiver() {
            @Override public void onReceive(Context context, Intent intent) {
                emitBattery(registerSticky(Intent.ACTION_BATTERY_CHANGED));
            }
        };
        registerReceiver(powerSaveReceiver,
                new IntentFilter(PowerManager.ACTION_POWER_SAVE_MODE_CHANGED));

        // Screen lock / unlock. SCREEN_OFF ≈ locked; USER_PRESENT = unlocked.
        screenReceiver = new BroadcastReceiver() {
            @Override public void onReceive(Context context, Intent intent) {
                String action = intent.getAction();
                if (Intent.ACTION_SCREEN_OFF.equals(action)) {
                    emitLock(true);
                } else if (Intent.ACTION_USER_PRESENT.equals(action)) {
                    emitLock(false);
                }
            }
        };
        IntentFilter screenFilter = new IntentFilter();
        screenFilter.addAction(Intent.ACTION_SCREEN_OFF);
        screenFilter.addAction(Intent.ACTION_USER_PRESENT);
        registerReceiver(screenReceiver, screenFilter);

        // Network connectivity / transport type / cellular signal strength.
        connectivityManager = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        if (connectivityManager != null) {
            networkCallback = new ConnectivityManager.NetworkCallback() {
                @Override public void onAvailable(Network network) { emitNetwork(network); }
                @Override public void onLost(Network network) { emitNetworkDisconnected(); }
                @Override public void onCapabilitiesChanged(Network network, NetworkCapabilities caps) {
                    emitNetwork(network);
                }
            };
            try {
                connectivityManager.registerDefaultNetworkCallback(networkCallback);
            } catch (Exception e) {
                Log.e(TAG, "registerDefaultNetworkCallback failed", e);
            }
        }
    }

    private void unregisterSystemEventReceivers() {
        safeUnregister(batteryReceiver);
        batteryReceiver = null;
        safeUnregister(powerSaveReceiver);
        powerSaveReceiver = null;
        safeUnregister(screenReceiver);
        screenReceiver = null;
        if (connectivityManager != null && networkCallback != null) {
            try {
                connectivityManager.unregisterNetworkCallback(networkCallback);
            } catch (Exception ignored) {
            }
            networkCallback = null;
        }
    }

    private void safeUnregister(BroadcastReceiver r) {
        if (r != null) {
            try {
                unregisterReceiver(r);
            } catch (Exception ignored) {
            }
        }
    }

    /** Read the current sticky value for an action without a standing receiver. */
    @Nullable
    private Intent registerSticky(String action) {
        return registerReceiver(null, new IntentFilter(action));
    }

    /** Push current battery / network / theme so a freshly-loaded UI is populated. */
    private void emitSystemSnapshot() {
        emitBattery(registerSticky(Intent.ACTION_BATTERY_CHANGED));
        if (connectivityManager != null) {
            Network active = connectivityManager.getActiveNetwork();
            if (active != null) {
                emitNetwork(active);
            } else {
                emitNetworkDisconnected();
            }
        }
        emitTheme();
    }

    private void emitBattery(@Nullable Intent batteryStatus) {
        try {
            float level = -1f;
            String state = "unknown";
            if (batteryStatus != null) {
                int lvl = batteryStatus.getIntExtra(BatteryManager.EXTRA_LEVEL, -1);
                int scale = batteryStatus.getIntExtra(BatteryManager.EXTRA_SCALE, -1);
                if (lvl >= 0 && scale > 0) {
                    level = lvl / (float) scale;
                }
                switch (batteryStatus.getIntExtra(BatteryManager.EXTRA_STATUS, -1)) {
                    case BatteryManager.BATTERY_STATUS_CHARGING: state = "charging"; break;
                    case BatteryManager.BATTERY_STATUS_FULL: state = "full"; break;
                    case BatteryManager.BATTERY_STATUS_DISCHARGING:
                    case BatteryManager.BATTERY_STATUS_NOT_CHARGING: state = "unplugged"; break;
                    default: state = "unknown"; break;
                }
            }
            boolean lowPower = false;
            PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
            if (pm != null) {
                lowPower = pm.isPowerSaveMode();
            }
            JSONObject o = new JSONObject();
            o.put("level", (double) level);
            o.put("state", state);
            o.put("lowPowerMode", lowPower);
            if (bridge != null) bridge.emitSystemEvent("android:BatteryChanged", o.toString());
        } catch (Exception e) {
            Log.e(TAG, "emitBattery failed", e);
        }
    }

    private void emitNetwork(@Nullable Network network) {
        try {
            boolean connected = false;
            String type = "none";
            boolean metered = false;
            Integer signal = null;
            if (connectivityManager != null && network != null) {
                NetworkCapabilities caps = connectivityManager.getNetworkCapabilities(network);
                if (caps != null) {
                    connected = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET);
                    if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                        type = "wifi";
                    } else if (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) {
                        type = "cellular";
                    } else if (caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) {
                        type = "wired";
                    } else {
                        type = "other";
                    }
                    metered = !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED);
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                        int s = caps.getSignalStrength();
                        if (s != Integer.MIN_VALUE) {
                            signal = s; // dBm; closer to 0 is a stronger signal
                        }
                    }
                }
            }
            JSONObject o = new JSONObject();
            o.put("connected", connected);
            o.put("type", type);
            o.put("metered", metered);
            if (signal != null) {
                o.put("signal", (int) signal);
            }
            if (bridge != null) bridge.emitSystemEvent("android:NetworkChanged", o.toString());
        } catch (Exception e) {
            Log.e(TAG, "emitNetwork failed", e);
        }
    }

    private void emitNetworkDisconnected() {
        try {
            JSONObject o = new JSONObject();
            o.put("connected", false);
            o.put("type", "none");
            o.put("metered", false);
            if (bridge != null) bridge.emitSystemEvent("android:NetworkChanged", o.toString());
        } catch (Exception ignored) {
        }
    }

    private void emitLock(boolean locked) {
        // Lock/unlock are signals (no payload); name carries the state.
        if (bridge != null) {
            bridge.emitSystemEvent(locked ? "android:ScreenLocked" : "android:ScreenUnlocked", "{}");
        }
    }

    private void emitTheme() {
        try {
            int mode = getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK;
            JSONObject o = new JSONObject();
            // "isDarkMode" matches the context key the desktop platforms use.
            o.put("isDarkMode", mode == Configuration.UI_MODE_NIGHT_YES);
            if (bridge != null) bridge.emitSystemEvent("android:ThemeChanged", o.toString());
        } catch (Exception ignored) {
        }
    }

    @Override
    public void onConfigurationChanged(Configuration newConfig) {
        super.onConfigurationChanged(newConfig);
        // Fires for light/dark switches because the manifest lists uiMode in
        // android:configChanges (otherwise the activity would be recreated).
        emitTheme();
    }

    @Override
    protected void onStart() {
        super.onStart();
        // Battery: only monitor system events while the app is visible.
        if (!systemReceiversRegistered) {
            registerSystemEventReceivers();
            systemReceiversRegistered = true;
        }
        if (bridge != null) {
            bridge.onStart();
        }
    }

    /**
     * Records that transfers are being kept alive behind the app, so the next
     * return to the foreground can be the moment TDrive asks to show them.
     */
    void noteBackgroundTransferRunning() {
        backgroundTransferRan = true;
    }

    /**
     * Asks for POST_NOTIFICATIONS at the only moment it explains itself.
     *
     * Asked when Upload is tapped, the dialog is one more thing between the
     * user and the thing they just asked for, and it gets dismissed; on Android
     * 13+ the second dismissal is permanent, so a badly-timed ask is not a
     * retry, it is the end of the matter. Asked here -- the user has left, a
     * transfer has been running without them, and they have just come back --
     * the question is about something that has already happened to them.
     *
     * Once per run of the app, and only after a transfer has actually earned
     * it. No preference is stored: a refusal is remembered by Android itself,
     * which from then on answers the request immediately and without a dialog,
     * so there is no way for this to become a prompt the user sees twice.
     */
    private void askForTransferNotificationsIfEarned() {
        if (!backgroundTransferRan || askedForTransferNotifications) return;
        backgroundTransferRan = false;
        askedForTransferNotifications = true;
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return;
        if (checkSelfPermission("android.permission.POST_NOTIFICATIONS")
                == PackageManager.PERMISSION_GRANTED) {
            return;
        }
        requestPermissions(
                new String[]{"android.permission.POST_NOTIFICATIONS"}, NOTIFICATION_PERMISSION_REQUEST);
    }

    @Override
    protected void onResume() {
        super.onResume();
        askForTransferNotificationsIfEarned();
        if (webView != null) {
            applySystemTextScale(webView.getSettings());
            publishSystemTextScale();
        }
        if (bridge != null) {
            bridge.onResume();
            // MediaStore access may have changed while the app was backgrounded
            // (notably Android 14's selected-photo grant), so the page can
            // resume its bounded discovery without polling a stale grant.
            bridge.emitSystemEvent("android:PhotoBackupMediaChanged", photoBackupAccess().toString());
        }
    }

    @Override
    protected void onPause() {
        super.onPause();
        if (bridge != null) {
            bridge.onPause();
        }
    }

    @Override
    protected void onStop() {
        super.onStop();
        if (systemReceiversRegistered) {
            unregisterSystemEventReceivers();
            systemReceiversRegistered = false;
        }
        if (bridge != null) {
            bridge.onStop();
        }
    }

    @Override
    public void onLowMemory() {
        super.onLowMemory();
        if (bridge != null) {
            bridge.onLowMemory();
        }
    }

    @Override
    public void onTrimMemory(int level) {
        super.onTrimMemory(level);
        // Modern Android commonly trims without dispatching onLowMemory.
        // UI-hidden also drops disposable decoded images before backgrounding.
        if (bridge != null && (level == android.content.ComponentCallbacks2.TRIM_MEMORY_UI_HIDDEN
                || level >= android.content.ComponentCallbacks2.TRIM_MEMORY_RUNNING_LOW)) {
            bridge.onLowMemory();
        }
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        unregisterSystemEventReceivers();
        WailsForegroundService.attachRuntimeBridge(null);
        if (bridge != null) {
            bridge.shutdown();
        }
        if (webView != null) {
            webView.destroy();
        }
    }

    /**
     * TDrive: the page owns in-app back (open sheet, drive switcher, gallery,
     * folder). WebView.canGoBack() cannot decide it, because the history a
     * single-page app pushes is same-document and invisible to that call, so
     * relying on it exits the app on the first press. Ask the page instead and
     * only leave when it reports the press unhandled. Going through the
     * dispatcher rather than overriding onBackPressed() keeps this working
     * under the predictive back gesture.
     */
    private void registerBackHandler() {
        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                if (webView == null) {
                    finish();
                    return;
                }
                webView.evaluateJavascript(
                        "(function(){try{return !!(window.__tdriveHandleBack && window.__tdriveHandleBack());}"
                                + "catch(e){return false;}})()",
                        value -> {
                            if (!"true".equals(value)) finish();
                        });
            }
        });
    }
}
