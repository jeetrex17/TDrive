package com.wails.app;

import android.util.Log;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import android.content.Context;
import android.webkit.JavascriptInterface;
import android.webkit.WebView;
import com.wails.app.BuildConfig;

/**
 * WailsJSBridge provides the JavaScript interface that allows the web frontend
 * to communicate with the Go backend. This is exposed to JavaScript as the
 * `window.wails` object.
 *
 * Similar to iOS's WKScriptMessageHandler but using Android's addJavascriptInterface.
 */
public class WailsJSBridge {
    private static final String TAG = "WailsJSBridge";
    private static final boolean DEBUG = BuildConfig.DEBUG;
    // Pooled threads avoid unbounded thread creation under high call volume.
    private static final ExecutorService executor = Executors.newCachedThreadPool();

    private final WailsBridge bridge;
    private final WebView webView;

    public WailsJSBridge(WailsBridge bridge, WebView webView) {
        this.bridge = bridge;
        this.webView = webView;
    }

    /**
     * Send a message to Go and return the response synchronously.
     * Called from JavaScript: wails.invoke(message)
     *
     * @param message The message to send (JSON string)
     * @return The response from Go (JSON string)
     */
    @JavascriptInterface
    public String invoke(String message) {
        if (DEBUG) Log.d(TAG, "Invoke called: " + message);
        return bridge.handleMessage(message);
    }

    /**
     * Send a message to Go asynchronously.
     * The response will be sent back via a callback.
     * Called from JavaScript: wails.invokeAsync(callbackId, message)
     *
     * @param callbackId The callback ID to use for the response
     * @param message The message to send (JSON string)
     */
    @JavascriptInterface
    public void invokeAsync(final String callbackId, final String payload) {
        if (DEBUG) Log.d(TAG, "InvokeAsync called: " + payload);

        // Handle off the JS thread so we don't block the WebView.
        executor.execute(() -> {
            try {
                String response = bridge.handleRuntimeCall(payload);
                sendCallback(callbackId, response, null);
            } catch (Exception e) {
                Log.e(TAG, "Error in async invoke", e);
                sendCallback(callbackId, null, e.getMessage());
            }
        });
    }

    /**
     * Ask the system for a folder and answer with what it holds.
     *
     * Called from JavaScript: wails.pickFolder(callbackId)
     *
     * This exists because Wails refuses directory selection on Android: the
     * Storage Access Framework hands back document-tree URIs rather than
     * filesystem paths, and its dialog API has nowhere to put one. The answer is
     * a manifest, {"root":"Holiday","files":[{"id","rel","size"}]}, so a folder
     * of any size costs a walk rather than a second copy of itself on disk. The
     * uploader then asks for the bytes a batch at a time, through
     * materializeFiles, and gives them back through releaseFiles.
     *
     * An empty string means the picker was dismissed, and only that: an empty
     * folder answers with a manifest holding no files.
     */
    @JavascriptInterface
    public void pickFolder(final String callbackId) {
        final MainActivity activity = activity();
        if (activity == null) {
            sendCallback(callbackId, null, "folder picker unavailable");
            return;
        }
        activity.runOnUiThread(() -> activity.launchFolderPicker(callbackId));
    }

    /**
     * Copy a batch of the picked folder's files into the cache and answer with
     * {"paths":{"<id>":"/abs/path"}}, so only the files about to be uploaded sit
     * on disk. An id that will not open is left out of the map.
     *
     * Called from JavaScript: wails.materializeFiles(callbackId, idsJson)
     */
    @JavascriptInterface
    public void materializeFiles(final String callbackId, final String idsJson) {
        final MainActivity activity = activity();
        if (activity == null) {
            sendCallback(callbackId, null, "folder picker unavailable");
            return;
        }
        activity.materializeFiles(callbackId, idsJson);
    }

    /**
     * Drop the cached copies of a batch once it has uploaded. Answers with an
     * empty string, and is happy with ids that were never copied.
     *
     * Called from JavaScript: wails.releaseFiles(callbackId, idsJson)
     */
    @JavascriptInterface
    public void releaseFiles(final String callbackId, final String idsJson) {
        final MainActivity activity = activity();
        if (activity == null) {
            sendCallback(callbackId, "", null);
            return;
        }
        activity.releaseFiles(callbackId, idsJson);
    }

    /**
     * Move a finished download into the phone's public Downloads folder and
     * answer {"location":"Download/plan.pdf"} with the name it really got,
     * which is not always the name it asked for. The path may be a file or a
     * whole folder, and the sandbox copy is gone once every byte has landed.
     *
     * Called from JavaScript: wails.saveToDownloads(callbackId, json)
     * with json {"path":"/data/.../files/TDrive/Downloads/plan.pdf"}.
     */
    @JavascriptInterface
    public void saveToDownloads(final String callbackId, final String json) {
        final MainActivity activity = activity();
        if (activity == null) {
            sendCallback(callbackId, null, "cannot save to Downloads right now");
            return;
        }
        activity.saveToDownloads(callbackId, json);
    }

    private MainActivity activity() {
        Context context = webView.getContext();
        return context instanceof MainActivity ? (MainActivity) context : null;
    }

    /**
     * Log a message from JavaScript to Android's logcat
     * Called from JavaScript: wails.log(level, message)
     *
     * @param level The log level (debug, info, warn, error)
     * @param message The message to log
     */
    @JavascriptInterface
    public void log(String level, String message) {
        switch (level.toLowerCase()) {
            case "debug":
                Log.d(TAG + "/JS", message);
                break;
            case "info":
                Log.i(TAG + "/JS", message);
                break;
            case "warn":
                Log.w(TAG + "/JS", message);
                break;
            case "error":
                Log.e(TAG + "/JS", message);
                break;
            default:
                Log.v(TAG + "/JS", message);
                break;
        }
    }

    /**
     * Get the platform name
     * Called from JavaScript: wails.platform()
     *
     * @return "android"
     */
    @JavascriptInterface
    public String platform() {
        return "android";
    }

    /**
     * Check if we're running in debug mode
     * Called from JavaScript: wails.isDebug()
     *
     * @return true if debug build, false otherwise
     */
    @JavascriptInterface
    public boolean isDebug() {
        return BuildConfig.DEBUG;
    }

    /**
     * Send a callback response to JavaScript
     */
    /** Package visible so the activity can answer a picker it launched. */
    void sendCallback(String callbackId, String result, String error) {
        final String js;
        if (error != null) {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', null, '%s');",
                    escapeJsString(callbackId),
                    escapeJsString(error)
            );
        } else {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', '%s', null);",
                    escapeJsString(callbackId),
                    escapeJsString(result != null ? result : "")
            );
        }

        webView.post(() -> webView.evaluateJavascript(js, null));
    }

    private String escapeJsString(String str) {
        if (str == null) return "";
        return str.replace("\\", "\\\\")
                .replace("'", "\\'")
                .replace("\n", "\\n")
                .replace("\r", "\\r")
                // JS line terminators (U+2028/U+2029) must be escaped too; built via
                // (char) casts so the Java lexer does not reinterpret them as newlines.
                .replace(String.valueOf((char) 0x2028), "\\u2028")
                .replace(String.valueOf((char) 0x2029), "\\u2029");
    }
}
