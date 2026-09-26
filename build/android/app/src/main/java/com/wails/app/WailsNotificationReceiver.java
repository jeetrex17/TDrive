package com.wails.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

/**
 * The buttons on TDrive's notifications, and the only way a tap on one reaches
 * the code that owns the work.
 *
 * Transfers live in the Go runtime behind the WebView, so an action cannot do
 * anything itself: it can only say which button was pressed and let the page
 * act. That is deliberately the same path the service already uses to report
 * its own deadline -- one weak reference to the runtime bridge, held by the
 * service, never a static handle on a WebView that would outlive the activity
 * that owns it.
 *
 * A broadcast rather than a service start: the process may be in the
 * background when the button is tapped, where Android 8+ refuses
 * startService() and Android 12+ refuses startForegroundService(). A broadcast
 * to the app's own receiver is allowed in either state, which is what makes
 * the button work rather than silently fail.
 *
 * When the page is gone -- the app swiped away, the process reaped -- the tap
 * opens TDrive instead, on the screen the action belonged to. Doing nothing at
 * all would read as a broken button.
 */
public class WailsNotificationReceiver extends BroadcastReceiver {
    static final String ACTION_NOTIFICATION_ACTION = "com.wails.app.NOTIFICATION_ACTION";

    /** Which button: an opaque id the page assigns and the page interprets. */
    static final String EXTRA_ACTION_ID = "com.wails.app.extra.ACTION_ID";
    /** The notification to take down once the action is delivered, or 0 to leave it. */
    static final String EXTRA_DISMISS_ID = "com.wails.app.extra.DISMISS_ID";
    /** Where to open the app if the page is not there to handle the action. */
    static final String EXTRA_FALLBACK_ROUTE = "com.wails.app.extra.FALLBACK_ROUTE";

    @Override
    public void onReceive(Context context, Intent intent) {
        if (intent == null || !ACTION_NOTIFICATION_ACTION.equals(intent.getAction())) return;
        String actionId = intent.getStringExtra(EXTRA_ACTION_ID);
        if (actionId == null || actionId.isEmpty()) return;

        // A button that acts takes its own row with it: a "Retry" still sitting
        // in the shade after the retry started is an invitation to start it
        // twice. The ongoing row is exempt -- the work it describes carries on,
        // and the page takes it down when that work ends.
        int dismissId = intent.getIntExtra(EXTRA_DISMISS_ID, 0);
        if (dismissId != 0) WailsForegroundService.cancelNotification(context, dismissId);

        if (WailsForegroundService.dispatchNotificationAction(actionId)) return;

        // No page to hear it. Open the app where the action lived, so the tap
        // leads somewhere rather than nowhere.
        String route = intent.getStringExtra(EXTRA_FALLBACK_ROUTE);
        Intent launch = MainActivity.routeIntent(context, route);
        if (launch != null) context.startActivity(launch);
    }
}
