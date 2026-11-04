#import <AVFAudio/AVFAudio.h>
#import <MediaPlayer/MediaPlayer.h>

// Wails' controller queries this mask, so the public scene rotation request
// and the controller's supported orientations agree.
extern void ios_set_orientation(const char *mode);
extern UIInterfaceOrientationMask mfSupportedOrientations(void);

@interface TDrivePlayerBridge : NSObject <WKScriptMessageHandler>
@property(nonatomic, weak) WKWebView *webView;
@property(nonatomic, weak) UIViewController *presenter;
@property(nonatomic, copy) NSString *session;
@property(nonatomic) CGFloat originalBrightness;
@property(nonatomic) UIInterfaceOrientationMask originalOrientations;
@property(nonatomic, strong) UIView *volumePanel;
@property(nonatomic, strong) NSTimer *volumeTimer;
@end

@implementation TDrivePlayerBridge
- (instancetype)init {
    if ((self = [super init])) {
        [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(restore) name:UIApplicationWillResignActiveNotification object:nil];
        [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(becameActive) name:UIApplicationDidBecomeActiveNotification object:nil];
    }
    return self;
}
- (void)dealloc { [[NSNotificationCenter defaultCenter] removeObserver:self]; }
- (void)becameActive {
    [self.webView evaluateJavaScript:@"window.dispatchEvent(new Event('tdrive:player-controls-active'));" completionHandler:nil];
}
- (NSString *)orientation {
    UIInterfaceOrientation value = self.presenter.view.window.windowScene.interfaceOrientation;
    return UIInterfaceOrientationIsLandscape(value) ? @"landscape" : @"portrait";
}
- (NSDictionary *)state {
    return @{ @"brightness": @(UIScreen.mainScreen.brightness),
              @"volume": @(AVAudioSession.sharedInstance.outputVolume),
              @"orientation": [self orientation], @"volumeMode": @"native-slider" };
}
- (void)hideVolume {
    [self.volumeTimer invalidate];
    self.volumeTimer = nil;
    [self.volumePanel removeFromSuperview];
    self.volumePanel = nil;
}
- (void)restore {
    [self hideVolume];
    if (self.session == nil) return;
    NSString *endedSession = self.session;
    self.session = nil;
    NSData *json = [NSJSONSerialization dataWithJSONObject:@{ @"session": endedSession } options:0 error:nil];
    NSString *detail = [[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding];
    [self.webView evaluateJavaScript:[NSString stringWithFormat:@"window.dispatchEvent(new CustomEvent('tdrive:player-controls-ended',{detail:%@}));", detail] completionHandler:nil];
    UIScreen.mainScreen.brightness = self.originalBrightness;
    const char *mode = self.originalOrientations == UIInterfaceOrientationMaskPortrait ? "portrait" :
        self.originalOrientations == UIInterfaceOrientationMaskLandscape ? "landscape" : "auto";
    ios_set_orientation(mode);
}
- (void)showVolume {
    [self hideVolume];
    UIView *host = self.presenter.view;
    UIView *panel = [[UIView alloc] init];
    panel.translatesAutoresizingMaskIntoConstraints = NO;
    panel.backgroundColor = [UIColor colorWithWhite:0.08 alpha:0.92];
    panel.layer.cornerRadius = 16;
    [host addSubview:panel];
    [NSLayoutConstraint activateConstraints:@[
        [panel.trailingAnchor constraintEqualToAnchor:host.safeAreaLayoutGuide.trailingAnchor constant:-12],
        [panel.centerYAnchor constraintEqualToAnchor:host.safeAreaLayoutGuide.centerYAnchor],
        [panel.widthAnchor constraintEqualToConstant:70],
        [panel.heightAnchor constraintEqualToConstant:250]
    ]];
    MPVolumeView *volume = [[MPVolumeView alloc] initWithFrame:CGRectMake(0, 0, 180, 40)];
    volume.showsRouteButton = NO;
    volume.accessibilityLabel = @"Video volume";
    volume.transform = CGAffineTransformMakeRotation(-M_PI_2);
    volume.center = CGPointMake(35, 112);
    [panel addSubview:volume];
    UIButton *close = [UIButton buttonWithType:UIButtonTypeSystem];
    close.frame = CGRectMake(8, 202, 54, 40);
    [close setTitle:@"Done" forState:UIControlStateNormal];
    close.accessibilityLabel = @"Close volume control";
    [close addTarget:self action:@selector(hideVolume) forControlEvents:UIControlEventTouchUpInside];
    [panel addSubview:close];
    self.volumePanel = panel;
    self.volumeTimer = [NSTimer scheduledTimerWithTimeInterval:10 target:self selector:@selector(hideVolume) userInfo:nil repeats:NO];
}
- (void)reply:(NSString *)callback result:(id)result error:(NSString *)error {
    NSDictionary *message = @{ @"callbackId": callback, @"result": result ?: NSNull.null, @"error": error ?: NSNull.null };
    NSData *json = [NSJSONSerialization dataWithJSONObject:message options:0 error:nil];
    if (!json) return;
    NSString *encoded = [json base64EncodedStringWithOptions:0];
    NSString *script = [NSString stringWithFormat:@"window.__tdrivePlayerReply && window.__tdrivePlayerReply(JSON.parse(new TextDecoder().decode(Uint8Array.from(atob('%@'),c=>c.charCodeAt(0)))));", encoded];
    [self.webView evaluateJavaScript:script completionHandler:nil];
}
- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)message {
    WKSecurityOrigin *origin = message.frameInfo.securityOrigin;
    BOOL trustedOrigin = [origin.protocol isEqualToString:@"wails"] && [origin.host isEqualToString:@"localhost"];
    if (!message.frameInfo.isMainFrame || !trustedOrigin || ![message.body isKindOfClass:NSDictionary.class]) return;
    NSDictionary *body = message.body;
    NSString *callback = body[@"callbackId"];
    NSDictionary *request = body[@"request"];
    if (![callback isKindOfClass:NSString.class] || callback.length == 0 || callback.length > 128 || ![request isKindOfClass:NSDictionary.class]) return;
    NSString *session = request[@"session"];
    NSString *operation = request[@"operation"];
    if (![session isKindOfClass:NSString.class] || session.length == 0 || session.length > 128 || ![operation isKindOfClass:NSString.class]) {
        [self reply:callback result:nil error:@"Invalid player request"]; return;
    }
    if (UIApplication.sharedApplication.applicationState != UIApplicationStateActive) {
        [self reply:callback result:nil error:@"Player controls unavailable while inactive"]; return;
    }
    if ([operation isEqualToString:@"begin"]) {
        if (![session isEqualToString:self.session]) {
            [self restore];
            self.originalBrightness = UIScreen.mainScreen.brightness;
            self.originalOrientations = mfSupportedOrientations();
            self.session = session;
        }
    } else if (![session isEqualToString:self.session]) {
        [self reply:callback result:nil error:@"Player session is no longer active"]; return;
    } else if ([operation isEqualToString:@"state"]) {
        // Read only; external hardware controls may have changed the volume.
    } else if ([operation isEqualToString:@"brightness"]) {
        id number = request[@"value"];
        double value = [number isKindOfClass:NSNumber.class] ? [number doubleValue] : NAN;
        if (!isfinite(value) || value < 0 || value > 1) { [self reply:callback result:nil error:@"Brightness must be between zero and one"]; return; }
        UIScreen.mainScreen.brightness = MAX(0.01, value);
    } else if ([operation isEqualToString:@"rotate"]) {
        NSString *mode = request[@"orientation"];
        if (![mode isEqual:@"portrait"] && ![mode isEqual:@"landscape"] && ![mode isEqual:@"auto"]) {
            [self reply:callback result:nil error:@"Invalid player orientation"]; return;
        }
        if (@available(iOS 16.0, *)) {
            ios_set_orientation(mode.UTF8String);
        } else {
            [self reply:callback result:nil error:@"Screen rotation requires iOS 16 or later"]; return;
        }
    } else if ([operation isEqualToString:@"showVolume"]) {
        [self showVolume];
    } else if ([operation isEqualToString:@"hideVolume"]) {
        [self hideVolume];
    } else if ([operation isEqualToString:@"end"]) {
        [self restore];
        [self reply:callback result:@{} error:nil]; return;
    } else {
        [self reply:callback result:nil error:@"Unsupported player control"]; return;
    }
    [self reply:callback result:[self state] error:nil];
}
@end

static char TDrivePlayerBridgeKey;
static void TDriveInstallPlayerBridge(WKWebView *webView, UIViewController *presenter) {
    if (objc_getAssociatedObject(webView, &TDrivePlayerBridgeKey)) return;
    TDrivePlayerBridge *bridge = [[TDrivePlayerBridge alloc] init];
    bridge.webView = webView;
    bridge.presenter = presenter;
    objc_setAssociatedObject(webView, &TDrivePlayerBridgeKey, bridge, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    [webView.configuration.userContentController addScriptMessageHandler:bridge name:@"tdrivePlayer"];
}
