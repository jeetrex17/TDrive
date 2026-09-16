//go:build ios
// Minimal bootstrap: delegate comes from Go archive (WailsAppDelegate)
#import <UIKit/UIKit.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>
#include <stdio.h>

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
@end

static void (*tdriveInsetLayout)(id, SEL) = NULL;

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

    // Added rather than swizzled: Wails implements no home-indicator
    // preference, so there is nothing to chain to and nothing to conflict
    // with. class_addMethod leaves any future implementation of its own alone.
    class_addMethod(controller,
                    @selector(prefersHomeIndicatorAutoHidden),
                    (IMP)tdriveHomeIndicatorAutoHidden,
                    "c@:");
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
