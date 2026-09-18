package com.wails.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

import androidx.annotation.Nullable;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;

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
    /** The item moving right now -- a file name. Absent where there is nothing to name. */
    public static final String EXTRA_DETAIL = "detail";
    /**
     * Files finished, and files in the batch. Absent, or a total of zero, where
     * the work is not a countable batch; a count is never guessed from one.
     */
    public static final String EXTRA_FILES_DONE = "filesDone";
    public static final String EXTRA_FILES_TOTAL = "filesTotal";

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
    /**
     * The line left behind once the work is over, on an id of its own so it
     * neither replaces a transfer that is still running nor is taken down with
     * the service that posted it.
     */
    private static final int SUMMARY_NOTIFICATION_ID = 0x57A2;
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
    private static volatile boolean channelReady;
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
        // Everything below is optional and defaults to what this notification
        // showed before any of it existed, so a page that sends none of it --
        // an older bundle against a newer host -- is unchanged by its arrival.
        String detail = intent != null ? intent.getStringExtra(EXTRA_DETAIL) : null;
        int filesDone = intent != null ? intent.getIntExtra(EXTRA_FILES_DONE, 0) : 0;
        int filesTotal = intent != null ? intent.getIntExtra(EXTRA_FILES_TOTAL, 0) : 0;

        ensureChannel(this);
        // Unconditional and synchronous, on every command including the one
        // that arrives with a null intent: the system allows roughly five
        // seconds from startForegroundService() to startForeground() before it
        // kills the app with an ANR, and there is nothing here worth deferring.
        Notification notification = build(title, text, hasBar, progress, detail, filesDone, filesTotal);
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

    private static void ensureChannel(Context context) {
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
        NotificationManager nm =
                (NotificationManager) context.getSystemService(NOTIFICATION_SERVICE);
        if (nm != null) nm.createNotificationChannel(channel);
        channelReady = true;
    }

    private Notification build(String title, String text, boolean hasBar, int progress,
                               String detail, int filesDone, int filesTotal) {
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
        // "3 of 12" sits beside the app name, where it costs neither of the two
        // lines anything: a count is what a glance at a collapsed row can use,
        // and bytes and an estimate are what a longer look wants.
        if (filesTotal > 0) {
            builder.setSubText(Math.max(0, Math.min(filesDone, filesTotal)) + " of " + filesTotal);
        }
        // A file name is routinely wider than the collapsed row, which would
        // truncate it to nothing useful, and is exactly what the user came to
        // the shade for. Expanding is where it belongs.
        if (detail != null && !detail.isEmpty()) {
            builder.setStyle(new NotificationCompat.BigTextStyle().bigText(text + "\n" + detail));
        }

        // No Cancel action. Stopping the transfers means reaching the Go calls
        // that own them, which only the WebView can do, and wiring a button
        // back through the activity to JavaScript buys a static reference to a
        // WebView that outlives it. Tapping the notification opens the app,
        // where the Transfers tab cancels any row individually -- honest, and
        // already built.
        return builder.build();
    }

    /**
     * Held rather than rebuilt: this runs on every update, and asking for a
     * PendingIntent is a round trip to the system each time.
     */
    private PendingIntent contentIntent() {
        if (contentIntent == null) contentIntent = launchIntent(this);
        return contentIntent;
    }

    /**
     * The line the user finds afterwards, in place of the ongoing one.
     *
     * A transfer that ran while the phone was in a pocket ends with its
     * notification simply vanishing, which tells the user nothing about whether
     * their photos are safe. This says so, once, and can be swiped away --
     * unlike the ongoing notification, which cannot, and so must never be the
     * thing left behind. Same channel as the progress it replaces: it is the
     * same subject, and a second channel would be a second row in Settings for
     * the user to reason about.
     *
     * Silent and low either way. "Done" is good news, and good news that
     * interrupts is still an interruption.
     */
    static void postSummary(Context context, String title, String text, boolean interrupted) {
        NotificationManagerCompat manager = NotificationManagerCompat.from(context);
        // Denied POST_NOTIFICATIONS makes notify() a no-op rather than an
        // error, but asking first keeps the intent of the code readable.
        if (!manager.areNotificationsEnabled()) return;
        ensureChannel(context);
        Notification notification = new NotificationCompat.Builder(context, CHANNEL_ID)
                // The icon carries the outcome before either line is read.
                .setSmallIcon(interrupted
                        ? android.R.drawable.stat_sys_warning
                        : android.R.drawable.stat_sys_upload_done)
                .setContentTitle(title)
                .setContentText(text)
                .setAutoCancel(true)
                .setSilent(true)
                .setCategory(NotificationCompat.CATEGORY_STATUS)
                .setContentIntent(launchIntent(context))
                .build();
        manager.notify(SUMMARY_NOTIFICATION_ID, notification);
    }

    /** Tapping any of this opens the app, which is where transfers are managed. */
    private static PendingIntent launchIntent(Context context) {
        Intent launch = context.getPackageManager().getLaunchIntentForPackage(context.getPackageName());
        if (launch == null) return null;
        int flags = Build.VERSION.SDK_INT >= Build.VERSION_CODES.M
                ? PendingIntent.FLAG_IMMUTABLE : 0;
        return PendingIntent.getActivity(context, 0, launch, flags);
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
        // Said here rather than left to the page: the deadline is the host's
        // doing, the page may be frozen in a backgrounded WebView when it
        // arrives, and a transfer that stops must never be mistaken for one
        // that finished. The work is resumable, and the line says so.
        postSummary(this, "Transfers stopped",
                "Android reached its background limit. Open TDrive to finish.", true);
        stopSelf(startId);
    }

    @Nullable
    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
