//go:build darwin

package nativeplayer

/*
#cgo darwin CFLAGS: -x objective-c -fblocks -DGL_SILENCE_DEPRECATION
#cgo darwin LDFLAGS: -framework Cocoa -framework QuartzCore -framework OpenGL
#cgo darwin pkg-config: mpv

#import <Cocoa/Cocoa.h>
#import <math.h>
#import <OpenGL/gl3.h>
#import <QuartzCore/QuartzCore.h>
#import <dispatch/dispatch.h>
#import <mpv/client.h>
#import <mpv/render.h>
#import <mpv/render_gl.h>
#import <stdint.h>
#import <stdio.h>
#import <string.h>

#define TDRIVE_MAX_TRACKS 32
#define TDRIVE_TRACK_KIND_SIZE 16
#define TDRIVE_TRACK_TITLE_SIZE 128
#define TDRIVE_TRACK_LANGUAGE_SIZE 32
#define TDRIVE_TRACK_CODEC_SIZE 64

static void *tdrive_gl_get_proc_address(void *ctx, const char *name) {
    CFStringRef symbol = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingASCII);
    void *addr = CFBundleGetFunctionPointerForName(CFBundleGetBundleWithIdentifier(CFSTR("com.apple.opengl")), symbol);
    CFRelease(symbol);
    return addr;
}

typedef struct {
	int64_t id;
	int selected;
	int is_default;
	int forced;
	char kind[TDRIVE_TRACK_KIND_SIZE];
	char title[TDRIVE_TRACK_TITLE_SIZE];
	char language[TDRIVE_TRACK_LANGUAGE_SIZE];
	char codec[TDRIVE_TRACK_CODEC_SIZE];
} tdrive_player_track;

typedef struct {
    double time_pos;
    double duration;
    double volume;
    double speed;
    double cache_duration;
    int64_t cache_buffering_state;
    int paused;
    int muted;
    int paused_for_cache;
    int eof_reached;
	int idle_active;
	int file_loaded;
	int load_failed;
	int ended;
    int has_time_pos;
    int has_duration;
    int has_cache_duration;
	int track_count;
	tdrive_player_track tracks[TDRIVE_MAX_TRACKS];
} tdrive_player_state;

static int tdrive_mpv_get_double(mpv_handle *mpv, const char *name, double *out) {
    double value = 0;
    if (mpv_get_property(mpv, name, MPV_FORMAT_DOUBLE, &value) < 0 || !isfinite(value)) {
        return 0;
    }
    *out = value;
    return 1;
}

static int tdrive_mpv_get_flag(mpv_handle *mpv, const char *name) {
    int value = 0;
    if (mpv_get_property(mpv, name, MPV_FORMAT_FLAG, &value) < 0) {
        return 0;
    }
    return value != 0;
}

static int64_t tdrive_mpv_get_int64(mpv_handle *mpv, const char *name) {
    int64_t value = 0;
    if (mpv_get_property(mpv, name, MPV_FORMAT_INT64, &value) < 0) {
        return 0;
    }
    return value;
}

static void tdrive_mpv_copy_string(mpv_handle *mpv, const char *name, char *out, size_t out_size) {
	if (out == NULL || out_size == 0) {
		return;
	}
	out[0] = '\0';
	char *value = mpv_get_property_string(mpv, name);
	if (value == NULL) {
		return;
	}
	strncpy(out, value, out_size - 1);
	out[out_size - 1] = '\0';
	mpv_free(value);
}

@interface TDriveMPVView : NSOpenGLView {
    mpv_handle *_mpv;
    mpv_render_context *_render;
    BOOL _closed;
	BOOL _fileLoaded;
	BOOL _loadFailed;
	BOOL _ended;
}
- (BOOL)startWithURL:(NSString *)url htmlControls:(BOOL)htmlControls;
- (void)shutdown;
- (void)shutdownSynchronously;
- (int)sendCommand:(int)argc argv:(const char **)argv;
- (BOOL)snapshot:(tdrive_player_state *)state;
- (void)drainEvents;
@end

static void tdrive_mpv_render_update(void *ctx) {
    TDriveMPVView *view = [(TDriveMPVView *)ctx retain];
    dispatch_async(dispatch_get_main_queue(), ^{
        [view setNeedsDisplay:YES];
        [view release];
    });
}

@implementation TDriveMPVView

+ (NSOpenGLPixelFormat *)tdrivePixelFormat {
    NSOpenGLPixelFormatAttribute attrs[] = {
        NSOpenGLPFAOpenGLProfile, NSOpenGLProfileVersion3_2Core,
        NSOpenGLPFAColorSize, 24,
        NSOpenGLPFAAlphaSize, 8,
        NSOpenGLPFADoubleBuffer,
        NSOpenGLPFAAccelerated,
        0
    };
    return [[[NSOpenGLPixelFormat alloc] initWithAttributes:attrs] autorelease];
}

- (instancetype)initWithFrame:(NSRect)frameRect {
    self = [super initWithFrame:frameRect pixelFormat:[TDriveMPVView tdrivePixelFormat]];
    if (self) {
        [self setWantsBestResolutionOpenGLSurface:YES];
        [self setWantsLayer:YES];
        [[self layer] setBackgroundColor:[[NSColor blackColor] CGColor]];
    }
    return self;
}

- (BOOL)startWithURL:(NSString *)url htmlControls:(BOOL)htmlControls {
    _mpv = mpv_create();
    if (_mpv == NULL) {
        return NO;
    }

	mpv_set_option_string(_mpv, "config", "no");
	mpv_set_option_string(_mpv, "terminal", "no");
	mpv_set_option_string(_mpv, "msg-level", "all=warn");
	mpv_set_option_string(_mpv, "vo", "libmpv");
	mpv_set_option_string(_mpv, "ytdl", "no");
	mpv_set_option_string(_mpv, "hwdec", "auto-safe");
	mpv_set_option_string(_mpv, "cache", "yes");
	mpv_set_option_string(_mpv, "demuxer-readahead-secs", "20");
	mpv_set_option_string(_mpv, "demuxer-max-bytes", "67108864");
	mpv_set_option_string(_mpv, "demuxer-max-back-bytes", "33554432");
	mpv_set_option_string(_mpv, "osc", htmlControls ? "no" : "yes");
	mpv_set_option_string(_mpv, "osd-bar", htmlControls ? "no" : "yes");

    if (mpv_initialize(_mpv) < 0) {
        return NO;
    }

    [[self openGLContext] makeCurrentContext];
    mpv_opengl_init_params glInit = {
        .get_proc_address = tdrive_gl_get_proc_address,
        .get_proc_address_ctx = NULL,
    };
    mpv_render_param params[] = {
        { MPV_RENDER_PARAM_API_TYPE, (void *)MPV_RENDER_API_TYPE_OPENGL },
        { MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, &glInit },
        { MPV_RENDER_PARAM_INVALID, NULL },
    };
    if (mpv_render_context_create(&_render, _mpv, params) < 0) {
        return NO;
    }
    mpv_render_context_set_update_callback(_render, tdrive_mpv_render_update, self);

    const char *cmd[] = { "loadfile", [url UTF8String], NULL };
    if (mpv_command_async(_mpv, 0, cmd) < 0) {
        return NO;
    }
    return YES;
}

- (void)drainEvents {
	if (_mpv == NULL) {
		return;
	}
	for (;;) {
		mpv_event *event = mpv_wait_event(_mpv, 0);
		if (event == NULL || event->event_id == MPV_EVENT_NONE) {
			return;
		}
		switch (event->event_id) {
			case MPV_EVENT_START_FILE:
				_fileLoaded = NO;
				_loadFailed = NO;
				_ended = NO;
				break;
			case MPV_EVENT_FILE_LOADED:
				_fileLoaded = YES;
				_loadFailed = NO;
				_ended = NO;
				break;
			case MPV_EVENT_END_FILE: {
				mpv_event_end_file *end = (mpv_event_end_file *)event->data;
				if (end != NULL && end->reason == MPV_END_FILE_REASON_EOF) {
					_ended = YES;
				} else if (end != NULL && end->reason == MPV_END_FILE_REASON_ERROR) {
					_loadFailed = YES;
				}
				break;
			}
			case MPV_EVENT_SHUTDOWN:
				if (!_closed) {
					_loadFailed = YES;
				}
				break;
			default:
				break;
		}
	}
}

- (void)drawRect:(NSRect)dirtyRect {
    if (_closed || _render == NULL) {
        [[NSColor blackColor] setFill];
        NSRectFill([self bounds]);
        return;
    }

    [[self openGLContext] makeCurrentContext];
    NSRect backing = [self convertRectToBacking:[self bounds]];
    int width = (int)MAX(1, backing.size.width);
    int height = (int)MAX(1, backing.size.height);
    glViewport(0, 0, width, height);
    glClearColor(0.0, 0.0, 0.0, 1.0);
    glClear(GL_COLOR_BUFFER_BIT);

    mpv_opengl_fbo fbo = { .fbo = 0, .w = width, .h = height, .internal_format = GL_RGBA8 };
    int flipY = 1;
    mpv_render_param params[] = {
        { MPV_RENDER_PARAM_OPENGL_FBO, &fbo },
        { MPV_RENDER_PARAM_FLIP_Y, &flipY },
        { MPV_RENDER_PARAM_INVALID, NULL },
    };
    mpv_render_context_render(_render, params);
    [[self openGLContext] flushBuffer];
}

- (void)reshape {
    [super reshape];
    [self setNeedsDisplay:YES];
}

- (int)sendCommand:(int)argc argv:(const char **)argv {
    if (_closed || _mpv == NULL || argc <= 0 || argv == NULL) {
        return 0;
    }
	if (argv[0] != NULL && strcmp(argv[0], "seek") == 0) {
		_ended = NO;
	}
    return mpv_command_async(_mpv, 0, argv);
}

- (BOOL)snapshot:(tdrive_player_state *)state {
    if (_closed || _mpv == NULL || state == NULL) {
        return NO;
    }

    memset(state, 0, sizeof(tdrive_player_state));
	[self drainEvents];
    state->paused = tdrive_mpv_get_flag(_mpv, "pause");
    state->muted = tdrive_mpv_get_flag(_mpv, "mute");
    state->paused_for_cache = tdrive_mpv_get_flag(_mpv, "paused-for-cache");
    state->eof_reached = tdrive_mpv_get_flag(_mpv, "eof-reached");
	state->idle_active = tdrive_mpv_get_flag(_mpv, "idle-active");
	state->file_loaded = _fileLoaded ? 1 : 0;
	state->load_failed = _loadFailed ? 1 : 0;
	state->ended = _ended ? 1 : 0;
    state->cache_buffering_state = tdrive_mpv_get_int64(_mpv, "cache-buffering-state");
    state->volume = 100.0;
    state->speed = 1.0;
    tdrive_mpv_get_double(_mpv, "volume", &state->volume);
    tdrive_mpv_get_double(_mpv, "speed", &state->speed);
    state->has_time_pos = tdrive_mpv_get_double(_mpv, "time-pos", &state->time_pos);
    state->has_duration = tdrive_mpv_get_double(_mpv, "duration", &state->duration);
    state->has_cache_duration = tdrive_mpv_get_double(_mpv, "demuxer-cache-duration", &state->cache_duration);

	int64_t track_count = tdrive_mpv_get_int64(_mpv, "track-list/count");
	for (int64_t index = 0; index < track_count && state->track_count < TDRIVE_MAX_TRACKS; index++) {
		char property[96];
		char kind[TDRIVE_TRACK_KIND_SIZE];
		snprintf(property, sizeof(property), "track-list/%lld/type", (long long)index);
		tdrive_mpv_copy_string(_mpv, property, kind, sizeof(kind));
		if (strcmp(kind, "audio") != 0 && strcmp(kind, "sub") != 0) {
			continue;
		}

		tdrive_player_track *track = &state->tracks[state->track_count];
		snprintf(property, sizeof(property), "track-list/%lld/id", (long long)index);
		track->id = tdrive_mpv_get_int64(_mpv, property);
		if (track->id <= 0) {
			continue;
		}
		strncpy(track->kind, kind, sizeof(track->kind) - 1);
		snprintf(property, sizeof(property), "track-list/%lld/title", (long long)index);
		tdrive_mpv_copy_string(_mpv, property, track->title, sizeof(track->title));
		snprintf(property, sizeof(property), "track-list/%lld/lang", (long long)index);
		tdrive_mpv_copy_string(_mpv, property, track->language, sizeof(track->language));
		snprintf(property, sizeof(property), "track-list/%lld/codec", (long long)index);
		tdrive_mpv_copy_string(_mpv, property, track->codec, sizeof(track->codec));
		snprintf(property, sizeof(property), "track-list/%lld/selected", (long long)index);
		track->selected = tdrive_mpv_get_flag(_mpv, property);
		snprintf(property, sizeof(property), "track-list/%lld/default", (long long)index);
		track->is_default = tdrive_mpv_get_flag(_mpv, property);
		snprintf(property, sizeof(property), "track-list/%lld/forced", (long long)index);
		track->forced = tdrive_mpv_get_flag(_mpv, property);
		state->track_count++;
	}
    return YES;
}

- (void)shutdownWithAsyncTerminate:(BOOL)asyncTerminate {
    if (_closed) {
        return;
    }
    _closed = YES;
    mpv_handle *mpv = _mpv;
    _mpv = NULL;
    if (_render != NULL) {
        [[self openGLContext] makeCurrentContext];
        mpv_render_context_set_update_callback(_render, NULL, NULL);
        mpv_render_context_free(_render);
        _render = NULL;
        [NSOpenGLContext clearCurrentContext];
    }
    if (mpv != NULL) {
        if (asyncTerminate) {
            TDriveMPVView *view = [self retain];
            dispatch_async(dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT, 0), ^{
                mpv_terminate_destroy(mpv);
                dispatch_async(dispatch_get_main_queue(), ^{
                    [view release];
                });
            });
        } else {
            mpv_terminate_destroy(mpv);
        }
    }
}

- (void)shutdown {
    [self shutdownWithAsyncTerminate:YES];
}

- (void)shutdownSynchronously {
    [self shutdownWithAsyncTerminate:NO];
}

- (void)dealloc {
    [self shutdownSynchronously];
    [super dealloc];
}

@end

static NSWindow* tdrive_player_window(void) {
    NSWindow *window = [NSApp keyWindow];
    if (window != nil) {
        return window;
    }
    for (NSWindow *candidate in [NSApp windows]) {
        if ([candidate isVisible]) {
            return candidate;
        }
    }
    return nil;
}

static NSRect tdrive_player_frame(NSView *content, double x, double y, double w, double h) {
    NSRect bounds = [content bounds];
    double bottomY = bounds.size.height - y - h;
    return NSMakeRect(x, bottomY, w, h);
}

static void* tdrive_player_create_view(const char *rawURL, double x, double y, double w, double h, int htmlControls) {
    if (rawURL == NULL) {
        return nil;
    }
    __block TDriveMPVView *view = nil;
    NSString *url = [NSString stringWithUTF8String:rawURL];
    void (^create)(void) = ^{
        NSWindow *window = tdrive_player_window();
        if (window == nil) {
            return;
        }
        NSView *content = [window contentView];
        if (content == nil) {
            return;
        }
        NSRect frame = tdrive_player_frame(content, x, y, w, h);
        view = [[TDriveMPVView alloc] initWithFrame:frame];
        if (view == nil) {
            return;
        }
        if (![view startWithURL:url htmlControls:(htmlControls != 0)]) {
            [view shutdownSynchronously];
            [view release];
            view = nil;
            return;
        }
        [view setAutoresizingMask:0];
        [content addSubview:view positioned:(htmlControls ? NSWindowBelow : NSWindowAbove) relativeTo:nil];
    };
    if ([NSThread isMainThread]) {
        create();
    } else {
        dispatch_sync(dispatch_get_main_queue(), create);
    }
    return view;
}

static void tdrive_player_set_frame(void *ptr, double x, double y, double w, double h) {
    if (ptr == nil) {
        return;
    }
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    void (^resize)(void) = ^{
        NSView *content = [view superview];
        if (content == nil) {
            return;
        }
        [view setFrame:tdrive_player_frame(content, x, y, w, h)];
        [view setNeedsDisplay:YES];
    };
    if ([NSThread isMainThread]) {
        resize();
    } else {
        dispatch_async(dispatch_get_main_queue(), resize);
    }
}

static int tdrive_player_command(void *ptr, int argc, const char **argv) {
    if (ptr == nil) {
        return 0;
    }
    __block int result = 0;
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    void (^send)(void) = ^{
        result = [view sendCommand:argc argv:argv];
    };
    if ([NSThread isMainThread]) {
        send();
    } else {
        dispatch_sync(dispatch_get_main_queue(), send);
    }
    return result;
}

static int tdrive_player_snapshot(void *ptr, tdrive_player_state *state) {
    if (ptr == nil || state == NULL) {
        return 0;
    }
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    return [view snapshot:state] ? 1 : 0;
}

static void tdrive_player_destroy_view(void *ptr) {
    if (ptr == nil) {
        return;
    }
    TDriveMPVView *view = (TDriveMPVView *)ptr;
    void (^destroy)(void) = ^{
        [view shutdown];
        [view removeFromSuperview];
        [view release];
    };
    if ([NSThread isMainThread]) {
        destroy();
    } else {
        dispatch_sync(dispatch_get_main_queue(), destroy);
    }
}
*/
import "C"

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
	"unsafe"
)

const statePollInterval = 250 * time.Millisecond

type Player struct {
	mu              sync.Mutex
	view            unsafe.Pointer
	closed          bool
	terminal        bool
	cancel          context.CancelFunc
	done            chan struct{}
	onState         StateHandler
	lastState       State
	failureOnce     sync.Once
	closedStateOnce sync.Once
}

func Start(ctx context.Context, url string, rect Rect, opts Options) (*Player, error) {
	if !rect.Valid() {
		return nil, fmt.Errorf("native player: invalid view rect")
	}
	rawURL := C.CString(url)
	defer C.free(unsafe.Pointer(rawURL))
	htmlControls := 0
	if opts.UseHTMLControls {
		htmlControls = 1
	}
	view := C.tdrive_player_create_view(rawURL, C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height), C.int(htmlControls))
	if view == nil {
		return nil, fmt.Errorf("native player: could not start libmpv view")
	}
	runCtx, cancel := context.WithCancel(ctx)
	p := &Player{
		view:    view,
		cancel:  cancel,
		done:    make(chan struct{}),
		onState: opts.OnState,
	}
	if p.onState != nil {
		p.publishState(normalizeState(State{Status: StatusOpening, Paused: true, Loading: true, Volume: 1, Rate: 1}))
		go p.streamState(runCtx)
	} else {
		close(p.done)
	}
	return p, nil
}

func (p *Player) Resize(rect Rect) error {
	if p == nil {
		return nil
	}
	if !rect.Valid() {
		return fmt.Errorf("native player: invalid view rect")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.view == nil {
		return nil
	}
	C.tdrive_player_set_frame(p.view, C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
	return nil
}

// ShowSeekThumbnail and HideSeekThumbnail are no-ops on macOS: the seek preview
// is drawn by the HTML overlay over the transparent webview, not a native
// window, so the player has nothing to render here.
func (p *Player) ShowSeekThumbnail(_ []byte, _ Rect) error { return nil }

func (p *Player) MoveSeekThumbnail(_ Rect) error { return nil }

func (p *Player) HideSeekThumbnail() error { return nil }

func (p *Player) Command(command ...string) error {
	if p == nil || len(command) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.view == nil {
		return nil
	}

	cStrings := make([]*C.char, len(command)+1)
	for i, part := range command {
		cStrings[i] = C.CString(part)
	}
	defer func() {
		for _, cstr := range cStrings {
			if cstr != nil {
				C.free(unsafe.Pointer(cstr))
			}
		}
	}()
	if rc := C.tdrive_player_command(p.view, C.int(len(command)), (**C.char)(unsafe.Pointer(&cStrings[0]))); rc < 0 {
		return fmt.Errorf("native player: libmpv command failed: %d", int(rc))
	}
	return nil
}

func (p *Player) Close() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(time.Second):
		}
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	view := p.view
	p.view = nil
	p.mu.Unlock()
	if view != nil {
		C.tdrive_player_destroy_view(view)
	}
	p.emitTerminal(StatusClosed)
	return nil
}

func (p *Player) streamState(ctx context.Context) {
	defer close(p.done)
	p.emitState()
	ticker := time.NewTicker(statePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.emitState()
		}
	}
}

func (p *Player) emitState() {
	if p == nil || p.onState == nil {
		return
	}
	state, ok := p.State()
	if !ok {
		return
	}
	if state.Status == StatusFailed {
		p.emitTerminal(StatusFailed)
		return
	}
	p.publishState(state)
}

func (p *Player) publishState(state State) {
	state = normalizeState(state)
	p.mu.Lock()
	if p.closed || p.terminal {
		p.mu.Unlock()
		return
	}
	p.lastState = state
	onState := p.onState
	p.mu.Unlock()
	if onState != nil {
		onState(state)
	}
}

func (p *Player) emitTerminal(status PlaybackStatus) {
	once := &p.failureOnce
	if status == StatusClosed {
		once = &p.closedStateOnce
	}
	once.Do(func() {
		state := terminalState(status)
		p.mu.Lock()
		p.terminal = true
		p.lastState = state
		onState := p.onState
		p.mu.Unlock()
		if onState != nil {
			onState(state)
		}
	})
}

func (p *Player) State() (State, bool) {
	if p == nil {
		return State{}, false
	}
	var raw C.tdrive_player_state
	p.mu.Lock()
	if p.closed || p.view == nil {
		p.mu.Unlock()
		return State{}, false
	}
	ok := C.tdrive_player_snapshot(p.view, &raw) != 0
	p.mu.Unlock()
	if !ok {
		return State{}, false
	}

	currentTime := cleanSeconds(float64(raw.time_pos), raw.has_time_pos != 0)
	duration := cleanSeconds(float64(raw.duration), raw.has_duration != 0)
	volume := clampFloat(float64(raw.volume)/100, 0, 1)
	rate := clampFloat(float64(raw.speed), 0.25, 4)
	if rate == 0 {
		rate = 1
	}
	eof := raw.eof_reached != 0 || raw.ended != 0
	paused := raw.paused != 0 || eof
	if eof && duration > 0 {
		currentTime = duration
	}

	state := State{
		EOF:         eof,
		Paused:      paused,
		CurrentTime: clampFloat(currentTime, 0, maxPositive(duration, currentTime)),
		Duration:    duration,
		Volume:      volume,
		Muted:       raw.muted != 0 || volume == 0,
		Rate:        rate,
		Loading:     raw.paused_for_cache != 0 || (!paused && raw.cache_buffering_state > 0 && raw.cache_buffering_state < 100),
		Tracks:      darwinTracks(raw),
	}
	if raw.load_failed != 0 {
		state.Status = StatusFailed
		state.Error = ErrPlayerExited.Error()
	} else if raw.file_loaded == 0 && !eof {
		state.Status = StatusOpening
	} else if raw.idle_active != 0 && !eof {
		state.Status = StatusOpening
	}
	if duration > 0 && raw.has_cache_duration != 0 {
		cacheDuration := cleanSeconds(float64(raw.cache_duration), true)
		if cacheDuration > 0 {
			state.Buffered = []BufferedRange{{
				Start: clampFloat(state.CurrentTime, 0, duration),
				End:   clampFloat(state.CurrentTime+cacheDuration, 0, duration),
			}}
		}
	}
	return normalizeState(state), true
}

func darwinTracks(raw C.tdrive_player_state) []Track {
	count := int(raw.track_count)
	if count < 0 {
		return nil
	}
	if count > len(raw.tracks) {
		count = len(raw.tracks)
	}
	tracks := make([]Track, 0, count)
	for index := 0; index < count; index++ {
		item := &raw.tracks[index]
		var trackType TrackType
		switch C.GoString(&item.kind[0]) {
		case "audio":
			trackType = TrackTypeAudio
		case "sub":
			trackType = TrackTypeSubtitle
		default:
			continue
		}
		tracks = append(tracks, Track{
			ID:       int64(item.id),
			Type:     trackType,
			Title:    boundedTrackText(C.GoString(&item.title[0])),
			Language: boundedTrackText(C.GoString(&item.language[0])),
			Codec:    boundedTrackText(C.GoString(&item.codec[0])),
			Selected: item.selected != 0,
			Default:  item.is_default != 0,
			Forced:   item.forced != 0,
		})
	}
	return tracks
}

func cleanSeconds(value float64, ok bool) float64 {
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func clampFloat(value, min, max float64) float64 {
	if max < min {
		max = min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func maxPositive(values ...float64) float64 {
	var max float64
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}
