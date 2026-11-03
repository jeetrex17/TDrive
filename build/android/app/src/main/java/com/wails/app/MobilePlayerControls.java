package com.wails.app;

import android.app.Activity;
import android.content.Context;
import android.content.pm.ActivityInfo;
import android.content.res.Configuration;
import android.media.AudioManager;
import android.provider.Settings;
import android.view.WindowManager;
import org.json.JSONObject;

/** Main-thread, session-owned player changes; never modifies system brightness. */
final class MobilePlayerControls {
    private final Activity activity;
    private String session;
    private float originalBrightness;
    private int originalOrientation;

    MobilePlayerControls(Activity activity) { this.activity = activity; }

    JSONObject execute(JSONObject request) throws Exception {
        String token = request.optString("session", "");
        if (token.isEmpty() || token.length() > 128) throw new IllegalArgumentException("Invalid player session");
        String operation = request.optString("operation", "");
        if ("begin".equals(operation)) {
            if (!token.equals(session)) {
                restore();
                originalBrightness = activity.getWindow().getAttributes().screenBrightness;
                originalOrientation = activity.getRequestedOrientation();
                session = token;
            }
            return state();
        }
        if (!token.equals(session)) throw new IllegalStateException("Player session is no longer active");
        switch (operation) {
            case "state":
                return state();
            case "brightness": {
                double value = value(request);
                WindowManager.LayoutParams attributes = activity.getWindow().getAttributes();
                attributes.screenBrightness = (float) Math.max(0.01, value);
                activity.getWindow().setAttributes(attributes);
                break;
            }
            case "volume": {
                double value = value(request);
                AudioManager audio = audio();
                if (audio.isVolumeFixed()) throw new IllegalStateException("This output has fixed volume");
                audio.setStreamVolume(AudioManager.STREAM_MUSIC,
                        (int) Math.round(value * audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC)), 0);
                break;
            }
            case "showVolume":
                audio().adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_SAME, AudioManager.FLAG_SHOW_UI);
                break;
            case "hideVolume":
                break;
            case "rotate": {
                String mode = request.optString("orientation", "");
                int orientation;
                if ("portrait".equals(mode)) orientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_PORTRAIT;
                else if ("landscape".equals(mode)) orientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE;
                else if ("auto".equals(mode)) orientation = originalOrientation;
                else throw new IllegalArgumentException("Invalid player orientation");
                activity.setRequestedOrientation(orientation);
                break;
            }
            case "end":
                restore();
                return new JSONObject();
            default:
                throw new IllegalArgumentException("Unsupported player control");
        }
        return state();
    }

    String activeSession() { return session; }

    void restore() {
        if (session == null) return;
        session = null;
        WindowManager.LayoutParams attributes = activity.getWindow().getAttributes();
        attributes.screenBrightness = originalBrightness;
        activity.getWindow().setAttributes(attributes);
        activity.setRequestedOrientation(originalOrientation);
    }

    private AudioManager audio() { return (AudioManager) activity.getSystemService(Context.AUDIO_SERVICE); }

    private static double value(JSONObject request) throws Exception {
        double value = request.getDouble("value");
        if (!Double.isFinite(value) || value < 0 || value > 1) throw new IllegalArgumentException("Player value must be between zero and one");
        return value;
    }

    private JSONObject state() throws Exception {
        float brightness = activity.getWindow().getAttributes().screenBrightness;
        if (brightness < 0) brightness = Settings.System.getInt(activity.getContentResolver(), Settings.System.SCREEN_BRIGHTNESS, 128) / 255f;
        AudioManager audio = audio();
        int maximum = audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC);
        String orientation = activity.getResources().getConfiguration().orientation == Configuration.ORIENTATION_LANDSCAPE ? "landscape" : "portrait";
        return new JSONObject().put("brightness", Math.max(0, Math.min(1, brightness)))
                .put("volume", maximum > 0 ? (double) audio.getStreamVolume(AudioManager.STREAM_MUSIC) / maximum : 0)
                .put("orientation", orientation).put("volumeMode", audio.isVolumeFixed() ? "unavailable" : "system");
    }
}
