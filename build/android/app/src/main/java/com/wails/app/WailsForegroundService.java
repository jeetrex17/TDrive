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

import org.json.JSONObject;

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
     * The one button the ongoing notification carries, if the work has
     * something the user can do to it: "Pause" for a backup, "Stop" for a
     * transfer they started. Absent means no button, which is what every
     * caller got before this existed.
     *
     * One, not several: the collapsed row shows what it has room for, and a
     * second button is a second decision to read past at the exact moment the
     * user is scanning the shade for something else.
     */
    public static final String EXTRA_ACTION_ID = "actionId";
    public static final String EXTRA_ACTION_LABEL = "actionLabel";

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
    /**
     * Problems get their own id as well as their own channel: a failure that
     * replaced the summary would erase the only record that the rest of the
     * work succeeded, and one that shared an id with the next failure would
     * hide it.
     */
    private static final int PROBLEM_NOTIFICATION_ID = 0x57A3;
    /**
     * The second channel, and the reason there is one.
     *
     * Progress and results belong together -- they are the same subject, one
     * after the other -- but a failure is not. Sharing a channel meant a user
     * who silenced the progress bar (reasonable: it redraws for minutes) also
     * silenced every "could not back this up", which is the one line here
     * worth interrupting for. Two channels is two rows in Settings; it is also
     * the only way either choice can be made.
     */
    private static final String PROBLEM_CHANNEL_ID = "tdrive_problems";
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

    /**
     * Hands a tapped notification button to the page, and reports whether
     * anyone was there to take it.
     *
     * False means the process outlived its WebView -- or never had one this
     * launch -- and the caller falls back to opening the app. The id is opaque
     * here on purpose: what "retry:down:1421" means is the page's business,
     * and a host that had to understand it would need updating every time the
     * page grew a new button.
     */
    static boolean dispatchNotificationAction(String actionId) {
        WailsBridge bridge = runtimeBridge.get();
        if (bridge == null) return false;
        bridge.emitEvent("android:NotificationAction", "{\"id\":" + JSONObject.quote(actionId) + "}");
        return true;
    }

    /** Takes one posted row down, by the id it was posted with. */
    static void cancelNotification(Context context, int notificationId) {
        NotificationManagerCompat.from(context).cancel(notificationId);
    }

    /** Creating the channel is idempotent but not free, and this runs per update. */
    private static volatile boolean channelReady;
    private PendingIntent contentIntent;

    /** The screen a transfer row belongs to, for taps and for action fallbacks. */
    static final String ROUTE_TRANSFERS = "transfers";

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
        String actionId = intent != null ? intent.getStringExtra(EXTRA_ACTION_ID) : null;
        String actionLabel = intent != null ? intent.getStringExtra(EXTRA_ACTION_LABEL) : null;

        ensureChannel(this);
        // Unconditional and synchronous, on every command including the one
        // that arrives with a null intent: the system allows roughly five
        // seconds from startForegroundService() to startForeground() before it
        // kills the app with an ANR, and there is nothing here worth deferring.
        Notification notification = build(title, text, hasBar, progress, detail, filesDone, filesTotal, actionId, actionLabel);
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
                               String detail, int filesDone, int filesTotal,
                               String actionId, String actionLabel) {
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

        // The one thing the user can do to this work without opening the app.
        // It reaches the Go calls that own the transfers the only way it can:
        // a broadcast to this app, then the page, through the same weak bridge
        // the deadline already uses. No static handle on a WebView is created
        // or kept, which was the objection that left this button out before.
        if (actionId != null && !actionId.isEmpty() && actionLabel != null && !actionLabel.isEmpty()) {
            builder.addAction(0, actionLabel, actionIntent(this, actionId, 0, ROUTE_TRANSFERS));
        }
        return builder.build();
    }

    /**
     * Held rather than rebuilt: this runs on every update, and asking for a
     * PendingIntent is a round trip to the system each time.
     */
    private PendingIntent contentIntent() {
        if (contentIntent == null) contentIntent = launchIntent(this, ROUTE_TRANSFERS);
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
        postSummary(context, title, text, interrupted, ROUTE_TRANSFERS);
    }

    /** As above, opening the screen the work belongs to when it is tapped. */
    static void postSummary(Context context, String title, String text, boolean interrupted, String route) {
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
                .setContentIntent(launchIntent(context, route))
                .build();
        manager.notify(SUMMARY_NOTIFICATION_ID, notification);
    }

    /**
     * Something went wrong while the user was elsewhere.
     *
     * Its own channel, so it can still be heard by someone who silenced the
     * progress bar, and its own id, so it neither erases the summary of the
     * work that did succeed nor is erased by it. The button is optional
     * because only some failures have a second chance worth offering: a
     * download can be re-queued from what the row already knows, an upload's
     * source path is long gone.
     */
    static void postProblem(Context context, String title, String text,
                            String actionId, String actionLabel, String route) {
        NotificationManagerCompat manager = NotificationManagerCompat.from(context);
        if (!manager.areNotificationsEnabled()) return;
        ensureProblemChannel(context);
        NotificationCompat.Builder builder = new NotificationCompat.Builder(context, PROBLEM_CHANNEL_ID)
                .setSmallIcon(android.R.drawable.stat_notify_error)
                .setContentTitle(title)
                .setContentText(text)
                // The reason is a sentence, not a label, and the collapsed row
                // has one line: the whole of it belongs in the expanded view.
                .setStyle(new NotificationCompat.BigTextStyle().bigText(text))
                .setAutoCancel(true)
                .setCategory(NotificationCompat.CATEGORY_ERROR)
                .setContentIntent(launchIntent(context, route));
        if (actionId != null && !actionId.isEmpty() && actionLabel != null && !actionLabel.isEmpty()) {
            builder.addAction(0, actionLabel,
                    actionIntent(context, actionId, PROBLEM_NOTIFICATION_ID, route));
        }
        manager.notify(PROBLEM_NOTIFICATION_ID, builder.build());
    }

    /**
     * A failure is worth a sound the first time and noise by the tenth, so the
     * channel is DEFAULT rather than HIGH: it posts to the shade and can be
     * heard, but it does not shove itself over what the user is doing.
     */
    private static volatile boolean problemChannelReady;

    private static void ensureProblemChannel(Context context) {
        if (problemChannelReady || Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            problemChannelReady = true;
            return;
        }
        NotificationChannel channel = new NotificationChannel(
                PROBLEM_CHANNEL_ID, "Transfer problems", NotificationManager.IMPORTANCE_DEFAULT);
        channel.setDescription("Shown when an upload, download, or backup could not finish.");
        NotificationManager nm =
                (NotificationManager) context.getSystemService(NOTIFICATION_SERVICE);
        if (nm != null) nm.createNotificationChannel(channel);
        problemChannelReady = true;
    }

    /**
     * The intent behind a notification button.
     *
     * Keyed by the action id so two buttons never share a PendingIntent: the
     * system matches them by requestCode plus intent equality, and extras are
     * not part of that comparison, so a shared code would silently deliver the
     * first button's extras from the second button.
     */
    private static PendingIntent actionIntent(Context context, String actionId,
                                              int dismissId, String route) {
        Intent intent = new Intent(context, WailsNotificationReceiver.class)
                .setAction(WailsNotificationReceiver.ACTION_NOTIFICATION_ACTION)
                .putExtra(WailsNotificationReceiver.EXTRA_ACTION_ID, actionId)
                .putExtra(WailsNotificationReceiver.EXTRA_DISMISS_ID, dismissId)
                .putExtra(WailsNotificationReceiver.EXTRA_FALLBACK_ROUTE, route);
        return PendingIntent.getBroadcast(context, actionId.hashCode(), intent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    /**
     * Tapping any of this opens the app on the screen the row is about, rather
     * than wherever the user happened to leave it. The activity is singleTop,
     * so a running app receives this through onNewIntent instead of being
     * rebuilt underneath the user.
     */
    private static PendingIntent launchIntent(Context context, String route) {
        Intent launch = MainActivity.routeIntent(context, route);
        if (launch == null) return null;
        return PendingIntent.getActivity(context, route == null ? 0 : route.hashCode(), launch,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
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
