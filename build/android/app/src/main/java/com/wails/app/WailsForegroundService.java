package com.wails.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

import androidx.annotation.Nullable;
import androidx.core.app.NotificationCompat;

import java.lang.ref.WeakReference;

/**
 * The reason Android lets this app keep working after the user leaves it.
 *
 * A plain process is killable the moment it stops being the thing on screen, so
 * an upload left running while the user answers a message is at the system's
 * mercy -- and the only mitigation the app had was forcing the screen to stay
 * on, which does not stop a kill and does cost the battery it was meant to
 * save. A started foreground service with an ongoing notification is the
 * contract Android actually honours: the process stays, and the user can see
 * why.
 *
 * The service does no work itself. It exists so the transfers already running
 * in the Go runtime, in this same process, are allowed to finish.
 *
 * Every command re-posts the notification, so the one line the user is looking
 * at for the next several minutes keeps up with the work. Coalescing lives on
 * the caller's side (frontend/src/ui/mobile/background-transfers.ts), because
 * progress arrives many times a second and a notification that redraws at that
 * rate is jank the user can see.
 */
public class WailsForegroundService extends android.app.Service {
    public static final String ACTION_START = "com.wails.app.FGS_START";
    public static final String EXTRA_TITLE = "title";
    public static final String EXTRA_TEXT = "text";
    /** 0..100 for a real bar, negative for one that only says "something is happening". */
    public static final String EXTRA_PROGRESS = "progress";

    /**
     * Unchanged from the id this service has always used. A channel id is how
     * Android remembers the user's own choices about a channel, so renaming it
     * would silently reset anyone who had already tuned this one and leave the
     * old channel stranded in Settings with nothing posting to it.
     */
    private static final String CHANNEL_ID = "wails_foreground";
    /**
     * One stable id, so the notification is replaced in place. A fresh id per
     * update would stack a new row per second and leave the finished ones
     * behind, because an ongoing notification cannot be swiped away.
     */
    private static final int NOTIFICATION_ID = 0x57A1; // "WAI"
    private static final int PROGRESS_MAX = 100;
    // The service and Wails runtime share a process. Keep only a weak handle so
    // a destroyed Activity/WebView can never be retained by a long transfer.
    private static volatile WeakReference<WailsBridge> runtimeBridge = new WeakReference<>(null);

    public static void attachRuntimeBridge(@Nullable WailsBridge bridge) {
        runtimeBridge = new WeakReference<>(bridge);
    }

    private static void emitDeadline() {
        WailsBridge bridge = runtimeBridge.get();
        if (bridge != null) {
            bridge.emitEvent("android:BackgroundTransferExpired", "{}");
        }
    }

    /** Creating the channel is idempotent but not free, and this runs per update. */
    private boolean channelReady;
    private PendingIntent contentIntent;

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String title = "Wails";
        String text = "Running in the background";
        if (intent != null) {
            if (intent.getStringExtra(EXTRA_TITLE) != null) title = intent.getStringExtra(EXTRA_TITLE);
            if (intent.getStringExtra(EXTRA_TEXT) != null) text = intent.getStringExtra(EXTRA_TEXT);
        }
        // Absent means "no bar at all", which is not the same as a bar with
        // nothing to say yet: a caller that never mentions progress should get
        // the plain notification it has always got.
        boolean hasBar = intent != null && intent.hasExtra(EXTRA_PROGRESS);
        int progress = hasBar ? intent.getIntExtra(EXTRA_PROGRESS, -1) : -1;

        ensureChannel();
        // Unconditional and synchronous, on every command including the one
        // that arrives with a null intent: the system allows roughly five
        // seconds from startForegroundService() to startForeground() before it
        // kills the app with an ANR, and there is nothing here worth deferring.
        Notification notification = build(title, text, hasBar, progress);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }

        // Deliberately not sticky. Restarting alone would not bring back the
        // transfers, which lived in the process the system just killed; it
        // would only re-post an ongoing notification claiming work that no
        // longer exists, which the user cannot dismiss. Better to go quietly.
        return START_NOT_STICKY;
    }

    private void ensureChannel() {
        if (channelReady || Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            channelReady = true;
            return;
        }
        NotificationChannel channel = new NotificationChannel(
                CHANNEL_ID, "File transfers", NotificationManager.IMPORTANCE_LOW);
        channel.setDescription("Shown while uploads and downloads are running.");
        // IMPORTANCE_LOW is what keeps this silent and out of the heads-up
        // strip: a progress line that shoved itself over whatever the user had
        // opened, once a second, would be unusable.
        channel.setShowBadge(false); // Work in progress is not an unread item.
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (nm != null) nm.createNotificationChannel(channel);
        channelReady = true;
    }

    private Notification build(String title, String text, boolean hasBar, int progress) {
        NotificationCompat.Builder builder = new NotificationCompat.Builder(this, CHANNEL_ID)
                // A platform status icon rather than the launcher icon, which is
                // a full-colour bitmap and renders as a white blob up there.
                // This one is bidirectional, and the queue mixes uploads and
                // downloads freely.
                .setSmallIcon(android.R.drawable.ic_popup_sync)
                .setContentTitle(title)
                .setContentText(text)
                .setOngoing(true)
                .setCategory(NotificationCompat.CATEGORY_PROGRESS)
                // The channel is already silent, but the user can raise it, and
                // then every one of the many updates a long transfer posts would
                // buzz. Silence belongs to this notification, not just to the
                // setting it happens to sit under.
                .setSilent(true)
                .setContentIntent(contentIntent())
                // Android 12+ holds a foreground service's first notification
                // back for ten seconds, which is exactly right here: a transfer
                // that finishes inside that window keeps the process safe
                // without ever putting a row in front of the user. The caller
                // starts the service immediately rather than debouncing it,
                // because a foreground service cannot legally be started once
                // the app is in the background -- so this is the only place a
                // short transfer can be kept quiet.
                .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_DEFERRED);

        if (hasBar) builder.setProgress(PROGRESS_MAX, clampProgress(progress), progress < 0);

        // No Cancel action. Stopping the transfers means reaching the Go calls
        // that own them, which only the WebView can do, and wiring a button
        // back through the activity to JavaScript buys a static reference to a
        // WebView that outlives it. Tapping the notification opens the app,
        // where the Transfers tab cancels any row individually -- honest, and
        // already built.
        return builder.build();
    }

    /** Tapping the row opens the app, which is where the transfers can be managed. */
    private PendingIntent contentIntent() {
        if (contentIntent != null) return contentIntent;
        Intent launch = getPackageManager().getLaunchIntentForPackage(getPackageName());
        if (launch == null) return null;
        int flags = Build.VERSION.SDK_INT >= Build.VERSION_CODES.M
                ? PendingIntent.FLAG_IMMUTABLE : 0;
        contentIntent = PendingIntent.getActivity(this, 0, launch, flags);
        return contentIntent;
    }

    private static int clampProgress(int progress) {
        if (progress < 0) return 0;
        return Math.min(progress, PROGRESS_MAX);
    }

    /**
     * Android 15 gives a dataSync service six hours in any twenty-four and then
     * calls this. Stopping is not optional: a service still running a few
     * seconds later is killed with ForegroundServiceDidNotStopInTimeException,
     * which is a crash rather than a lost transfer. Going quietly leaves the
     * transfers running on a process that is merely killable again, which is
     * the same place they were before any of this existed.
     */
    @Override
    public void onTimeout(int startId, int fgsType) {
        // Android 15's dataSync budget is a hard deadline. Tell the durable
        // backup controller before dropping foreground priority so it can
        // cancel staging/upload promptly and leave the queued record resumable.
        emitDeadline();
        stopSelf(startId);
    }

    @Nullable
    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
