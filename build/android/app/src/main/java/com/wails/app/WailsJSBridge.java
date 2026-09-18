package com.wails.app;

import android.util.Log;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import android.content.Context;
import android.content.Intent;
import android.webkit.JavascriptInterface;
import android.webkit.WebView;
import androidx.core.content.ContextCompat;
import com.wails.app.BuildConfig;
import org.json.JSONObject;

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

    /** Read the current photo/video library grant without opening a dialog. */
    @JavascriptInterface public void photoBackupAccess(final String callbackId) {
        MainActivity activity = activity();
        if (activity == null) { sendCallback(callbackId, null, "media library unavailable"); return; }
        sendCallback(callbackId, activity.photoBackupAccess().toString(), null);
    }

    /** Ask only for the system media permissions appropriate for this Android version. */
    @JavascriptInterface public void requestPhotoBackupAccess(final String callbackId) {
        MainActivity activity = activity();
        if (activity == null) { sendCallback(callbackId, null, "media library unavailable"); return; }
        activity.requestPhotoBackupAccess(callbackId);
    }

    /** Current network and battery constraints; this does not schedule work. */
    @JavascriptInterface public void photoBackupPolicyStatus(final String callbackId) {
        MainActivity activity = activity();
        if (activity == null) { sendCallback(callbackId, null, "media policy unavailable"); return; }
        sendCallback(callbackId, activity.photoBackupPolicyStatus().toString(), null);
    }

    /** List MediaStore albums as backup sources. */
    @JavascriptInterface public void listPhotoBackupSources(final String callbackId) {
        MainActivity activity = activity();
        if (activity == null) { sendCallback(callbackId, null, "media library unavailable"); return; }
        activity.listPhotoBackupSources(callbackId);
    }

    /** Return one bounded page of photo/video assets; paths are deliberately omitted. */
    @JavascriptInterface public void listPhotoBackupAssets(final String callbackId, final String requestJson) {
        MainActivity activity = activity();
        if (activity == null) { sendCallback(callbackId, null, "media library unavailable"); return; }
        activity.listPhotoBackupAssets(callbackId, requestJson);
    }

    /** Stage exactly one durable MediaStore asset for an upload. */
    @JavascriptInterface public void materializePhotoBackupAsset(final String callbackId, final String requestJson) {
        MainActivity activity = activity();
        if (activity == null) { sendCallback(callbackId, null, "media library unavailable"); return; }
        activity.materializePhotoBackupAsset(callbackId, requestJson);
    }

    /** Delete the single staged copy after its upload completes. */
    @JavascriptInterface public void releasePhotoBackupAsset(final String callbackId, final String requestJson) {
        MainActivity activity = activity();
        if (activity == null) { sendCallback(callbackId, "", null); return; }
        activity.releasePhotoBackupAsset(callbackId, requestJson);
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

    /**
     * Keep the process alive while transfers run, and say what they are doing.
     *
     * Called from JavaScript: wails.foregroundService(callbackId, json) with
     * json {"running":true,"title":"Uploading 3 files","text":"2 min left",
     * "progress":42,"detail":"IMG_0042.HEIC","filesDone":3,"filesTotal":12},
     * or {"running":false} to let the process be killable again. Progress is a
     * percentage, or negative where nothing is known yet.
     *
     * {"running":false,"outcome":"complete","title":"...","text":"..."} also
     * leaves a dismissible line behind saying how the work ended; "stopped" is
     * the same thing for work that did not finish. Every key past "running" is
     * optional and read with a default, so a page that sends a field this host
     * has never heard of is not an error and a host that predates a field
     * behaves exactly as it did before the field existed. That is the whole
     * extension mechanism: the payload grows, the method never does.
     *
     * Sending the same shape for the first call and every update is deliberate:
     * whether the service is already up is Android's business, not the page's,
     * and startForegroundService on a running service is simply how its
     * notification is replaced. One method, one state machine, no way for the
     * two sides to disagree about what is running.
     *
     * This deliberately does not ask for POST_NOTIFICATIONS here. The service
     * runs and the process survives whether or not the notification is visible
     * -- and where it is denied, Android still lists the app under the Task
     * Manager's running apps. A permission dialog thrown up the instant someone
     * taps Upload is the kind that gets dismissed, and on Android 13+ a second
     * dismissal is permanent, so the one good chance to ask is not here. It is
     * the next time the user comes back to the app, having just had a transfer
     * run behind their back with nothing to show for it; the activity owns that
     * moment, and all this does is tell it the moment has been earned. Once the
     * permission is granted the notification simply appears, because every
     * coalesced update re-posts it.
     */
    @JavascriptInterface
    public void foregroundService(final String callbackId, final String json) {
        final MainActivity activity = activity();
        if (activity == null) {
            sendCallback(callbackId, null, "background service unavailable");
            return;
        }
        try {
            JSONObject options = new JSONObject(json);
            if (!options.optBoolean("running", false)) {
                String outcome = options.optString("outcome", "");
                // Posted before the service goes, so the summary is already in
                // the shade as the ongoing row leaves it, rather than after a
                // gap in which the user is told nothing at all.
                if (!outcome.isEmpty()) {
                    WailsForegroundService.postSummary(activity,
                            options.optString("title", "Transfers finished"),
                            options.optString("text", ""),
                            !"complete".equals(outcome));
                }
                activity.stopService(new Intent(activity, WailsForegroundService.class));
                sendCallback(callbackId, "", null);
                return;
            }
            Intent intent = new Intent(activity, WailsForegroundService.class)
                    .setAction(WailsForegroundService.ACTION_START)
                    .putExtra(WailsForegroundService.EXTRA_TITLE, options.optString("title", "Transferring files"))
                    .putExtra(WailsForegroundService.EXTRA_TEXT, options.optString("text", ""))
                    .putExtra(WailsForegroundService.EXTRA_PROGRESS, options.optInt("progress", -1))
                    .putExtra(WailsForegroundService.EXTRA_DETAIL, options.optString("detail", ""))
                    .putExtra(WailsForegroundService.EXTRA_FILES_DONE, options.optInt("filesDone", 0))
                    .putExtra(WailsForegroundService.EXTRA_FILES_TOTAL, options.optInt("filesTotal", 0));
            ContextCompat.startForegroundService(activity, intent);
            activity.noteBackgroundTransferRunning();
            sendCallback(callbackId, "", null);
        } catch (Exception e) {
            // Android 12+ throws ForegroundServiceStartNotAllowedException when
            // the app is no longer in the foreground. Nothing above can recover
            // from that, but the transfer itself is still running and must not
            // be taken down with it, so the failure is reported and dropped.
            Log.e(TAG, "foregroundService failed", e);
            sendCallback(callbackId, null, "could not keep transfers running in the background");
        }
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
