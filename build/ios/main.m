//go:build ios
// Minimal bootstrap: delegate comes from Go archive (WailsAppDelegate)
#import <UIKit/UIKit.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>
#import <math.h>
#include <stdio.h>

#import "TDrivePhotoBridge.inc"

// Wails lays its WKWebView out inside the safe area. On a notched phone held
// sideways that leaves a 59 point strip of bare, unpainted view down each edge
// and hands the page a 756 point viewport on an 874 point screen, which is both
// a visible band of nothing and a width that trips the layout's own compact
// breakpoint.
//
// The page does not want that. It asks for viewport-fit=cover and reserves the
// edges itself in CSS, per surface: a video fills the glass while its controls
// stay clear of the island and the home indicator. So the web view is put back
// to the full screen here and the insets are left to env(safe-area-inset-*),
// which is what they are for.
//
// A host using Wails' own native tab bar is left alone: that bar is a real view
// occupying real space, and the web view has to stop above it.
@interface WailsViewController : UIViewController
@property (nonatomic, strong) WKWebView *webView;
@property (nonatomic, strong) UITabBar *tabBar;
- (void)tdrive_handleEdgeBack:(UIScreenEdgePanGestureRecognizer *)gesture;
@end

static void (*tdriveInsetLayout)(id, SEL) = NULL;
static void (*tdriveNavigationFinished)(id, SEL, WKWebView *, WKNavigation *) = NULL;
static char tdriveEdgeBackRecognizerKey;
static char tdriveEdgeBackIndicatorKey;
static __weak WailsViewController *tdriveTextScaleController = nil;
static BOOL tdriveTextScaleNotificationInstalled = NO;
static CGFloat tdriveLastTextScale = 0;

// Whether the status bar is currently asked to be hidden. Owned by the Go
// archive, which sets it from application.Mobile.SetStatusBar; Wails already
// answers -prefersStatusBarHidden from it.
extern BOOL mfStatusBarHidden(void);

// The home indicator follows the status bar.
//
// A full-screen player asks for the system bars to go, and on Android that
// takes the navigation bar with them. iOS has no navigation bar, but it does
// draw the home indicator over the picture, and the only way to dim it is
// -prefersHomeIndicatorAutoHidden, which Wails does not implement. Answering it
// with the same state the status bar uses means one call from the page quiets
// both edges, on both platforms.
static BOOL tdriveHomeIndicatorAutoHidden(id self, SEL _cmd) {
    (void)self; (void)_cmd;
    return mfStatusBarHidden();
}

// The last value UIKit was told about, so the update is only requested when the
// answer actually changes: asking for one from inside a layout pass that it can
// itself provoke is how a layout loop starts.
static BOOL tdriveHomeIndicatorState = NO;

// iOS 16.4 stopped letting Safari's Web Inspector attach to a WKWebView unless
// the view asks for it, and Wails never asks. Without it there is no way to
// read the page on a simulator at all -- no console, no computed styles, no
// measuring what actually shipped.
//
// Only the dev bundle opts in. Its identifier carries a .dev suffix that the
// shipping one does not, so a release build stays uninspectable without any
// build-flag plumbing to get wrong.
static void tdriveMakeInspectable(WKWebView *webView) {
    static BOOL done = NO;
    if (done || webView == nil) {
        return;
    }
    done = YES;
    if (![[[NSBundle mainBundle] bundleIdentifier] hasSuffix:@".dev"]) {
        return;
    }
    if (@available(iOS 16.4, *)) {
        webView.inspectable = YES;
    }
}

// WKWebView does not automatically map Dynamic Type into CSS. The host sends a
// bounded UIFontMetrics multiplier to the page, which stores it before the
// shell mounts and then broadcasts live changes. The shell scales its rem
// typography only; hit targets and media keep their physical size.
static void tdriveApplyTextScale(WailsViewController *controller) {
    if (controller == nil || controller.webView == nil) {
        return;
    }
    CGFloat scale = 1.0;
    if (@available(iOS 11.0, *)) {
        UIFontMetrics *metrics = [UIFontMetrics metricsForTextStyle:UIFontTextStyleBody];
        scale = [metrics scaledValueForValue:16.0 compatibleWithTraitCollection:controller.traitCollection] / 16.0;
    }
    // This mirrors the web guard. The upper bound keeps the phone shell usable
    // at Accessibility sizes while still honoring a substantial text increase.
    scale = MIN(2.5, MAX(0.8, scale));
    if (fabs(scale - tdriveLastTextScale) < 0.001) {
        return;
    }
    tdriveLastTextScale = scale;
    NSString *script = [NSString stringWithFormat:
        @"window.__tdriveSystemTextScale=%.3f;window.dispatchEvent(new CustomEvent('tdrive:system-text-scale',{detail:{scale:window.__tdriveSystemTextScale}}));",
        scale];
    [controller.webView evaluateJavaScript:script completionHandler:nil];
}

// There is no UINavigationController around Wails' single WKWebView, so UIKit
// cannot provide its usual interactive-pop gesture. Keep the recognizer native
// and limited to the leading system edge. File and photo swipes begin outside
// that reserved band, while the page's back handler still dismisses sheets and
// selection before it changes a folder or tab. The small native indicator is
// deliberately passive: it confirms drag progress and cancellation without
// moving a web surface whose state cannot be safely made interactive.
static UIView *tdriveEdgeBackIndicator(WailsViewController *controller) {
    return objc_getAssociatedObject(controller, &tdriveEdgeBackIndicatorKey);
}

static void tdriveShowEdgeBackProgress(WailsViewController *controller,
        UIScreenEdgePanGestureRecognizer *gesture) {
    UIView *indicator = tdriveEdgeBackIndicator(controller);
    if (indicator == nil) {
        return;
    }
    CGPoint translation = [gesture translationInView:controller.view];
    CGFloat progress = MIN(1.0, MAX(0.0, translation.x / 72.0));
    CGPoint point = [gesture locationInView:controller.view];
    UIEdgeInsets safe = UIEdgeInsetsZero;
    if (@available(iOS 11.0, *)) {
        safe = controller.view.safeAreaInsets;
    }
    CGFloat height = 48.0;
    CGFloat y = MIN(MAX(safe.top, point.y - height / 2.0),
                    MAX(safe.top, controller.view.bounds.size.height - safe.bottom - height));
    // A second gesture can begin while the previous 160ms fade is finishing.
    // Cancel that animation first; its completion must not hide the new drag.
    [indicator.layer removeAllAnimations];
    indicator.hidden = NO;
    indicator.frame = CGRectMake(0, y, 3.0 + progress * 2.0, height);
    indicator.alpha = 0.18 + progress * 0.62;
}

static void tdriveHideEdgeBackProgress(WailsViewController *controller) {
    UIView *indicator = tdriveEdgeBackIndicator(controller);
    if (indicator == nil || indicator.hidden) {
        return;
    }
    [UIView animateWithDuration:0.16 animations:^{
        indicator.alpha = 0;
    } completion:^(BOOL finished) {
        if (finished) {
            indicator.hidden = YES;
            indicator.alpha = 0;
        }
    }];
}

static void tdriveHandleEdgeBack(id self, SEL _cmd, UIScreenEdgePanGestureRecognizer *gesture) {
    (void)_cmd;
    WailsViewController *controller = (WailsViewController *)self;
    if (gesture.state == UIGestureRecognizerStateBegan
            || gesture.state == UIGestureRecognizerStateChanged) {
        tdriveShowEdgeBackProgress(controller, gesture);
        return;
    }
    tdriveHideEdgeBackProgress(controller);
    if (gesture.state != UIGestureRecognizerStateEnded) {
        return;
    }
    CGPoint translation = [gesture translationInView:controller.view];
    CGPoint velocity = [gesture velocityInView:controller.view];
    if (translation.x < 72.0 || velocity.x <= 0) {
        return;
    }
    [controller.webView evaluateJavaScript:
        @"if(typeof window.__tdriveHandleBack==='function'){window.__tdriveHandleBack();}"
        completionHandler:nil];
}

static void tdriveInstallEdgeBackGesture(WailsViewController *controller) {
    if (controller == nil || controller.webView == nil
            || objc_getAssociatedObject(controller, &tdriveEdgeBackRecognizerKey) != nil) {
        return;
    }
    UIScreenEdgePanGestureRecognizer *gesture = [[UIScreenEdgePanGestureRecognizer alloc]
        initWithTarget:controller action:@selector(tdrive_handleEdgeBack:)];
    gesture.edges = UIRectEdgeLeft;
    gesture.cancelsTouchesInView = NO;
    gesture.delaysTouchesBegan = NO;
    gesture.delaysTouchesEnded = NO;
    [controller.view addGestureRecognizer:gesture];
    UIView *indicator = [[UIView alloc] initWithFrame:CGRectZero];
    indicator.backgroundColor = [UIColor colorWithRed:0.0 green:0.478 blue:1.0 alpha:1.0];
    indicator.layer.cornerRadius = 2.5;
    indicator.userInteractionEnabled = NO;
    indicator.accessibilityElementsHidden = YES;
    indicator.hidden = YES;
    [controller.view addSubview:indicator];
    objc_setAssociatedObject(controller, &tdriveEdgeBackRecognizerKey, gesture,
                             OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    objc_setAssociatedObject(controller, &tdriveEdgeBackIndicatorKey, indicator,
                             OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}

// A full document reload creates a new JavaScript global. Re-publish the
// current scale after Wails has completed its own navigation bookkeeping so a
// relaunch, recovery reload, or deep-link load cannot silently fall back to
// the browser's fixed 16px root.
static void tdriveDidFinishNavigation(id self, SEL _cmd, WKWebView *webView, WKNavigation *navigation) {
    if (tdriveNavigationFinished != NULL) {
        tdriveNavigationFinished(self, _cmd, webView, navigation);
    }
    tdriveLastTextScale = 0;
    tdriveApplyTextScale((WailsViewController *)self);
}

static void tdriveFullBleedLayout(id self, SEL _cmd) {
    if (tdriveInsetLayout != NULL) {
        tdriveInsetLayout(self, _cmd);
    }
    WailsViewController *controller = (WailsViewController *)self;

    // Hiding the status bar relays out the window, so this pass is where the
    // change arrives; nothing else tells us the page asked for it.
    BOOL wanted = mfStatusBarHidden();
    if (wanted != tdriveHomeIndicatorState) {
        tdriveHomeIndicatorState = wanted;
        if (@available(iOS 11.0, *)) {
            [controller setNeedsUpdateOfHomeIndicatorAutoHidden];
        }
    }

    if (controller.webView != nil) {
        tdriveMakeInspectable(controller.webView);
        TDriveInstallPhotoBridge(controller.webView, controller);
        tdriveTextScaleController = controller;
        tdriveInstallEdgeBackGesture(controller);
        tdriveApplyTextScale(controller);
    }

    if (controller.tabBar != nil && !controller.tabBar.isHidden) {
        return;
    }
    if (controller.webView != nil) {
        controller.webView.frame = controller.view.bounds;
    }
}

// Installed by swizzle rather than by a category method, because a category
// that redefines an existing method has no defined winner. If Wails ever stops
// insetting, or renames the controller, this finds nothing and does nothing.
@implementation WailsViewController (TDriveFullBleed)
+ (void)load {
    Class controller = NSClassFromString(@"WailsViewController");
    if (controller == Nil) {
        return;
    }
    Method layout = class_getInstanceMethod(controller, @selector(viewDidLayoutSubviews));
    if (layout == NULL) {
        return;
    }
    tdriveInsetLayout = (void (*)(id, SEL))method_getImplementation(layout);
    method_setImplementation(layout, (IMP)tdriveFullBleedLayout);

    Method navigationFinished = class_getInstanceMethod(controller,
        @selector(webView:didFinishNavigation:));
    if (navigationFinished != NULL) {
        tdriveNavigationFinished = (void (*)(id, SEL, WKWebView *, WKNavigation *))
            method_getImplementation(navigationFinished);
        method_setImplementation(navigationFinished, (IMP)tdriveDidFinishNavigation);
    }

    // Added rather than swizzled: Wails implements no home-indicator
    // preference, so there is nothing to chain to and nothing to conflict
    // with. class_addMethod leaves any future implementation of its own alone.
    class_addMethod(controller,
                    @selector(prefersHomeIndicatorAutoHidden),
                    (IMP)tdriveHomeIndicatorAutoHidden,
                    "c@:");
    class_addMethod(controller,
                    @selector(tdrive_handleEdgeBack:),
                    (IMP)tdriveHandleEdgeBack,
                    "v@:@");

    if (!tdriveTextScaleNotificationInstalled) {
        tdriveTextScaleNotificationInstalled = YES;
        [[NSNotificationCenter defaultCenter]
            addObserverForName:UIContentSizeCategoryDidChangeNotification
            object:nil
            queue:[NSOperationQueue mainQueue]
            usingBlock:^(__unused NSNotification *notification) {
                tdriveApplyTextScale(tdriveTextScaleController);
            }];
    }
}
@end

int main(int argc, char * argv[]) {
    @autoreleasepool {
        // Disable buffering so stdout/stderr from Go log.Printf flush immediately
        setvbuf(stdout, NULL, _IONBF, 0);
        setvbuf(stderr, NULL, _IONBF, 0);

        // Call UIApplicationMain IMMEDIATELY and start NOTHING else here. Do not
        // start the Go runtime yet: starting it concurrently with UIApplicationMain
        // intermittently corrupts the FrontBoard launch handshake on a physical
        // device, so the app delegate's didFinishLaunchingWithOptions never fires
        // (blank cold launch / 0x8BADF00D). Instead, the WailsAppDelegate (provided
        // by the Go archive) starts the Go runtime itself from
        // didFinishLaunchingWithOptions - i.e. only AFTER UIKit has delivered the
        // launch - so the runtime never races the launch handshake.
        return UIApplicationMain(argc, argv, nil, @"WailsAppDelegate");
    }
}
