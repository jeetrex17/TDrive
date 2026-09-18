#import <Photos/Photos.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>

static NSString *const TDrivePhotoHandlerName = @"tdrivePhotos";
static NSUInteger const TDrivePhotoDefaultPageSize = 50;
static NSUInteger const TDrivePhotoMaximumPageSize = 128;
static unsigned long long const TDrivePhotoDefaultMaxBytes = 2ULL * 1024ULL * 1024ULL * 1024ULL;
static unsigned long long const TDrivePhotoMaximumMaxBytes = 4ULL * 1024ULL * 1024ULL * 1024ULL;
static unsigned long long const TDrivePhotoMinimumFreeBytes = 512ULL * 1024ULL * 1024ULL;

static NSString *TDrivePhotoAuthorizationName(PHAuthorizationStatus status) {
    switch (status) {
        case PHAuthorizationStatusAuthorized: return @"authorized";
        case PHAuthorizationStatusLimited: return @"limited";
        case PHAuthorizationStatusDenied: return @"denied";
        case PHAuthorizationStatusRestricted: return @"restricted";
        case PHAuthorizationStatusNotDetermined: return @"notDetermined";
    }
    return @"unknown";
}

static NSString *TDriveISODate(NSDate *date) {
    if (date == nil) return (NSString *)[NSNull null];
    static NSISO8601DateFormatter *formatter;
    static dispatch_once_t once;
    dispatch_once(&once, ^{ formatter = [[NSISO8601DateFormatter alloc] init]; });
    return [formatter stringFromDate:date];
}

static NSDictionary *TDrivePhotoError(NSString *code, NSString *message) {
    return @{ @"code": code ?: @"nativeError", @"message": message ?: @"Photo library operation failed" };
}

static NSUInteger TDriveBoundedPageSize(id value) {
    NSInteger requested = [value respondsToSelector:@selector(integerValue)] ? [value integerValue] : TDrivePhotoDefaultPageSize;
    return (NSUInteger)MAX(1, MIN((NSInteger)TDrivePhotoMaximumPageSize, requested));
}

static NSUInteger TDriveCursorOffset(id value) {
    if (![value isKindOfClass:[NSString class]] || [value length] == 0) return 0;
    NSScanner *scanner = [NSScanner scannerWithString:value];
    unsigned long long offset = 0;
    if (![scanner scanUnsignedLongLong:&offset] || !scanner.isAtEnd || offset > NSUIntegerMax) return 0;
    return (NSUInteger)offset;
}

static NSString *TDriveResourceID(PHAssetResource *resource, NSUInteger index) {
    return [NSString stringWithFormat:@"%lu:%ld:%@", (unsigned long)index, (long)resource.type,
            resource.originalFilename ?: @""];
}

static NSDictionary *TDriveResourceManifest(PHAssetResource *resource, NSUInteger index) {
    return @{
        @"id": TDriveResourceID(resource, index),
        @"filename": resource.originalFilename ?: @"resource",
        @"uniformTypeIdentifier": resource.uniformTypeIdentifier ?: @"public.data",
        @"type": @(resource.type),
    };
}

static NSUInteger TDriveDefaultResourceIndex(PHAsset *asset, NSArray<PHAssetResource *> *resources) {
    NSArray<NSNumber *> *preferred = asset.mediaType == PHAssetMediaTypeVideo
        ? @[@(PHAssetResourceTypeFullSizeVideo), @(PHAssetResourceTypeVideo)]
        : @[@(PHAssetResourceTypeFullSizePhoto), @(PHAssetResourceTypePhoto)];
    for (NSNumber *type in preferred) {
        for (NSUInteger i = 0; i < resources.count; i++) if (resources[i].type == type.integerValue) return i;
    }
    return resources.count > 0 ? 0 : NSNotFound;
}

static NSArray *TDriveResourcesForAsset(PHAsset *asset) {
    NSArray<PHAssetResource *> *resources = [PHAssetResource assetResourcesForAsset:asset];
    NSMutableArray *result = [NSMutableArray arrayWithCapacity:resources.count];
    [resources enumerateObjectsUsingBlock:^(PHAssetResource *resource, NSUInteger index, BOOL *stop) {
        (void)stop;
        [result addObject:TDriveResourceManifest(resource, index)];
    }];
    return result;
}

// The version is what the ledger keys a resource on, so it must move on any
// edit and stay put otherwise. The resource count is part of it because an
// edit can add or drop an adjustment resource without touching the date.
static NSString *TDriveAssetVersion(PHAsset *asset, NSArray<PHAssetResource *> *resources) {
    return [NSString stringWithFormat:@"%@|%@|%.3f|%lu",
        asset.localIdentifier, TDriveISODate(asset.modificationDate), asset.duration,
        (unsigned long)resources.count];
}

static NSNumber *TDriveMillis(NSDate *date) {
    return date ? @((long long)(date.timeIntervalSince1970 * 1000.0)) : @0;
}

// PhotoKit exposes a resource's byte size only through KVC, and only for
// resources that are local. Anything else reads as unknown rather than wrong.
static long long TDriveResourceFileSize(PHAssetResource *resource) {
    @try {
        id value = [resource valueForKey:@"fileSize"];
        return [value respondsToSelector:@selector(longLongValue)] ? MAX(0LL, [value longLongValue]) : 0;
    } @catch (__unused NSException *exception) {
        return 0;
    }
}

static NSDictionary *TDriveAssetManifest(PHAsset *asset) {
    NSString *mediaType = @"unknown";
    if (asset.mediaType == PHAssetMediaTypeImage) mediaType = @"image";
    if (asset.mediaType == PHAssetMediaTypeVideo) mediaType = @"video";
    NSString *version = TDriveAssetVersion(asset, [PHAssetResource assetResourcesForAsset:asset]);
    NSArray *resources = TDriveResourcesForAsset(asset);
    NSDictionary *primaryResource = resources.firstObject;
    return @{
        @"id": asset.localIdentifier,
        @"version": version,
        @"mediaType": mediaType,
        @"mediaSubtypes": @(asset.mediaSubtypes),
        @"creationDate": TDriveISODate(asset.creationDate),
        @"modificationDate": TDriveISODate(asset.modificationDate),
        @"modifiedAt": TDriveISODate(asset.modificationDate),
        @"name": primaryResource[@"filename"] ?: @"",
        @"size": (id)[NSNull null],
        @"pixelWidth": @(asset.pixelWidth),
        @"pixelHeight": @(asset.pixelHeight),
        @"durationSeconds": @(asset.duration),
        @"favorite": @(asset.favorite),
        @"hidden": @(asset.hidden),
        @"location": asset.location ? @{
            @"latitude": @(asset.location.coordinate.latitude),
            @"longitude": @(asset.location.coordinate.longitude),
        } : (id)[NSNull null],
        @"resources": resources,
    };
}


// A folder the user picked in Files, as a backup source.
//
// iOS has no MediaStore: a folder is a security-scoped URL the picker hands
// over once, so what is kept is a bookmark, and every read opens the scope
// around it. The root written into the ledger is "files:<path>/", normalized
// exactly as the Go side normalizes it (no empty, "." or ".." components,
// always a trailing slash) so the string the ledger stores is the key that
// finds the bookmark again after a relaunch.
static NSString *const TDriveFolderRootPrefix = @"files:";
static NSString *const TDriveFolderSourcePrefix = @"tree:files:";
static NSString *const TDriveFolderBookmarksKey = @"TDriveBackupFolderBookmarks";
static NSUInteger const TDriveFolderMaxEntries = 50000;

static NSString *TDriveNormalizedFolderRoot(NSString *path) {
    NSMutableArray<NSString *> *components = [NSMutableArray array];
    for (NSString *component in [path componentsSeparatedByString:@"/"]) {
        if (component.length == 0 || [component isEqualToString:@"."] || [component isEqualToString:@".."]) continue;
        [components addObject:component];
    }
    if (components.count == 0) return nil;
    return [NSString stringWithFormat:@"%@%@/", TDriveFolderRootPrefix, [components componentsJoinedByString:@"/"]];
}

/** The root a source id names, or nil when the id is not a picked folder. */
static NSString *TDriveFolderRootForSource(NSString *sourceID) {
    if (![sourceID hasPrefix:TDriveFolderSourcePrefix]) return nil;
    return [sourceID substringFromIndex:[@"tree:" length]];
}

// What the engine will actually take. Keeping the list here as well as in Go
// means a folder of PDFs does not cost a page of assets that are enqueued and
// then dropped; both ends agree on what a photo or a video is.
static NSString *TDriveFolderMediaKind(NSString *name) {
    static NSSet<NSString *> *photos, *videos;
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        photos = [NSSet setWithArray:@[@"jpg", @"jpeg", @"png", @"heic", @"heif", @"webp", @"gif", @"tif", @"tiff", @"bmp", @"avif", @"dng", @"raw", @"arw", @"cr2", @"cr3", @"nef", @"orf", @"raf", @"rw2"]];
        videos = [NSSet setWithArray:@[@"mp4", @"mov", @"m4v", @"avi", @"mkv", @"webm", @"3gp"]];
    });
    NSString *ext = name.pathExtension.lowercaseString;
    if ([photos containsObject:ext]) return @"photo";
    if ([videos containsObject:ext]) return @"video";
    return nil;
}

@interface TDrivePhotoBridge : NSObject <WKScriptMessageHandler, PHPhotoLibraryChangeObserver, UIDocumentPickerDelegate>
@property (nonatomic, weak) WKWebView *webView;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSNumber *> *materializations;
@property (nonatomic, strong) NSMutableSet<NSString *> *stagedTokens;
// UIKit grants only a short, system-controlled window to finish work after
// the app leaves the foreground. The page arms this assertion while backup is
// running; it is acquired only after backgrounding and released promptly when
// backup drains or the app returns.
@property (nonatomic, assign) BOOL backgroundBackupWanted;
@property (nonatomic, assign) UIBackgroundTaskIdentifier backgroundBackupTask;
// A backup scan pages over one immutable PHFetchResult per source, held here
// so page N+1 sees the same library page N did. A cursor names the snapshot
// it was cut from; when the snapshot is gone the page asks to start over
// instead of silently skipping or repeating whatever shifted underneath it.
@property (nonatomic, strong) NSMutableDictionary<NSString *, PHFetchResult<PHAsset *> *> *scanSnapshots;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSString *> *scanTokens;
// A picked folder is walked once per scan and the listing held here, for the
// same reason a library scan holds its fetch result: page N+1 has to see what
// page N did, or a file written mid-scan shifts every index after it.
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSArray<NSDictionary *> *> *folderSnapshots;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSString *> *folderTokens;
// The view controller the document picker is presented from, and the one
// request waiting on it. Only one picker can be open at a time.
@property (nonatomic, weak) UIViewController *presenter;
@property (nonatomic, copy) NSString *pendingFolderPick;
- (void)startObserving;
@end

@implementation TDrivePhotoBridge

- (instancetype)init {
    self = [super init];
    if (self) {
        _materializations = [NSMutableDictionary dictionary];
        _stagedTokens = [NSMutableSet set];
        _scanSnapshots = [NSMutableDictionary dictionary];
        _scanTokens = [NSMutableDictionary dictionary];
        _folderSnapshots = [NSMutableDictionary dictionary];
        _folderTokens = [NSMutableDictionary dictionary];
        _backgroundBackupTask = UIBackgroundTaskInvalid;
    }
    return self;
}

- (void)startObserving {
    NSURL *cache = [[[NSFileManager defaultManager] URLsForDirectory:NSCachesDirectory inDomains:NSUserDomainMask] firstObject];
    NSURL *stagingRoot = [cache URLByAppendingPathComponent:@"TDrivePhotoBackup" isDirectory:YES];
    [[NSFileManager defaultManager] removeItemAtURL:stagingRoot error:nil];
    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(applicationDidBecomeActive:) name:UIApplicationDidBecomeActiveNotification object:nil];
    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(applicationWillResignActive:) name:UIApplicationWillResignActiveNotification object:nil];
    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(applicationDidEnterBackground:) name:UIApplicationDidEnterBackgroundNotification object:nil];
    [[PHPhotoLibrary sharedPhotoLibrary] registerChangeObserver:self];
}

- (void)dealloc {
    [self endBackgroundBackupTask];
    [[NSNotificationCenter defaultCenter] removeObserver:self];
    [[PHPhotoLibrary sharedPhotoLibrary] unregisterChangeObserver:self];
}

- (void)emitMediaChanged:(NSString *)reason {
    NSString *script = [NSString stringWithFormat:@"window.dispatchEvent(new CustomEvent('ios:PhotoBackupMediaChanged',{detail:{reason:'%@'}}))", reason];
    dispatch_async(dispatch_get_main_queue(), ^{ [self.webView evaluateJavaScript:script completionHandler:nil]; });
}

- (void)applicationDidBecomeActive:(NSNotification *)notification {
    (void)notification;
    [self endBackgroundBackupTask];
    // Backup may still be running without a state transition in JavaScript.
    // Re-arm while visible so the next background transition has a real UIKit
    // assertion rather than a stale frontend belief that one still exists.
    [self beginBackgroundBackupTaskIfNeeded];
    [self emitMediaChanged:@"resume"];
}

- (void)applicationDidEnterBackground:(NSNotification *)notification {
    (void)notification;
    [self beginBackgroundBackupTaskIfNeeded];
}

- (void)applicationWillResignActive:(NSNotification *)notification {
    (void)notification;
    [self beginBackgroundBackupTaskIfNeeded];
}

- (void)emitBackgroundBackupExpired {
    NSString *script = @"window.dispatchEvent(new CustomEvent('ios:PhotoBackupBackgroundExpired'))";
    dispatch_async(dispatch_get_main_queue(), ^{
        [self.webView evaluateJavaScript:script completionHandler:nil];
    });
}

- (void)beginBackgroundBackupTaskIfNeeded {
    if (!self.backgroundBackupWanted || self.backgroundBackupTask != UIBackgroundTaskInvalid) return;
    __weak TDrivePhotoBridge *weakSelf = self;
    self.backgroundBackupTask = [[UIApplication sharedApplication]
        beginBackgroundTaskWithName:@"TDrive photo backup" expirationHandler:^{
            TDrivePhotoBridge *strongSelf = weakSelf;
            if (strongSelf == nil) return;
            [strongSelf emitBackgroundBackupExpired];
            [strongSelf endBackgroundBackupTask];
        }];
}

- (void)endBackgroundBackupTask {
    UIBackgroundTaskIdentifier task = self.backgroundBackupTask;
    self.backgroundBackupTask = UIBackgroundTaskInvalid;
    if (task != UIBackgroundTaskInvalid) {
        [[UIApplication sharedApplication] endBackgroundTask:task];
    }
}

- (void)setBackgroundBackup:(NSString *)requestID arguments:(NSDictionary *)arguments {
    BOOL active = [arguments[@"active"] boolValue];
    self.backgroundBackupWanted = active;
    if (active) {
        // Acquire while the app is still visible. UIKit explicitly warns that
        // waiting for suspension can be too late to receive the assertion.
        [self beginBackgroundBackupTaskIfNeeded];
    } else if (!active) {
        [self endBackgroundBackupTask];
    }
    if (active && self.backgroundBackupTask == UIBackgroundTaskInvalid) {
        self.backgroundBackupWanted = NO;
        [self reply:requestID result:nil error:TDrivePhotoError(
            @"backgroundUnavailable", @"iOS did not grant background execution time")];
        return;
    }
    [self reply:requestID result:@{
        @"active": @(active),
        // This is deliberately named "limited": iOS chooses the deadline and
        // may suspend or terminate the app. It is never a closed-app promise.
        @"mode": @"limited"
    } error:nil];
}

// Only an addition is worth a rescan. Favouriting, retitling or deleting a
// photo changes the library too, and re-walking a whole camera roll for each
// of those is what made every tap in Photos cost a full scan. Edits are
// picked up on the next resume, which already rescans.
- (void)photoLibraryDidChange:(PHChange *)changeInstance {
    __block BOOL additions = NO;
    NSMutableDictionary<NSString *, PHFetchResult<PHAsset *> *> *advanced = [NSMutableDictionary dictionary];
    @synchronized (self.scanSnapshots) {
        if (self.scanSnapshots.count == 0) additions = YES;
        [self.scanSnapshots enumerateKeysAndObjectsUsingBlock:^(NSString *sourceID, PHFetchResult<PHAsset *> *snapshot, BOOL *stop) {
            (void)stop;
            PHFetchResultChangeDetails *details = [changeInstance changeDetailsForFetchResult:snapshot];
            if (details == nil) return;
            if (!details.hasIncrementalChanges || details.insertedObjects.count > 0) additions = YES;
            advanced[sourceID] = details.fetchResultAfterChanges;
        }];
        // A snapshot that moved under a running scan invalidates that scan's
        // cursor; the next page starts over on the current library.
        [advanced enumerateKeysAndObjectsUsingBlock:^(NSString *sourceID, PHFetchResult<PHAsset *> *after, BOOL *stop) {
            (void)stop;
            self.scanSnapshots[sourceID] = after;
            [self.scanTokens removeObjectForKey:sourceID];
        }];
    }
    if (additions) [self emitMediaChanged:@"libraryChange"];
}

- (PHFetchResult<PHAsset *> *)fetchBackupAssetsForSource:(NSString *)sourceID {
    PHFetchOptions *options = [[PHFetchOptions alloc] init];
    options.sortDescriptors = @[[NSSortDescriptor sortDescriptorWithKey:@"creationDate" ascending:YES]];
    options.predicate = [NSPredicate predicateWithFormat:@"mediaType IN %@", @[@(PHAssetMediaTypeImage), @(PHAssetMediaTypeVideo)]];
    if (sourceID.length > 0 && ![sourceID isEqualToString:@"all"]) {
        PHAssetCollection *collection = [PHAssetCollection fetchAssetCollectionsWithLocalIdentifiers:@[sourceID] options:nil].firstObject;
        return collection ? [PHAsset fetchAssetsInAssetCollection:collection options:options] : nil;
    }
    return [PHAsset fetchAssetsWithOptions:options];
}

- (void)reply:(NSString *)requestID result:(id)result error:(NSDictionary *)error {
    NSDictionary *envelope = @{
        @"id": requestID ?: @"",
        @"result": result ?: (id)[NSNull null],
        @"error": error ?: (id)[NSNull null],
    };
    NSError *jsonError = nil;
    NSData *json = [NSJSONSerialization dataWithJSONObject:envelope options:0 error:&jsonError];
    if (json == nil) return;
    NSString *base64 = [json base64EncodedStringWithOptions:0];
    NSString *script = [NSString stringWithFormat:@"window.__tdriveIOSPhotosReplyBase64('%@')", base64];
    dispatch_async(dispatch_get_main_queue(), ^{ [self.webView evaluateJavaScript:script completionHandler:nil]; });
}

- (void)status:(NSString *)requestID {
    PHAuthorizationStatus status = [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
    [self reply:requestID result:@{ @"status": TDrivePhotoAuthorizationName(status), @"canRead": @(status == PHAuthorizationStatusAuthorized || status == PHAuthorizationStatusLimited), @"limited": @(status == PHAuthorizationStatusLimited) } error:nil];
}

- (void)requestAuthorization:(NSString *)requestID {
    [PHPhotoLibrary requestAuthorizationForAccessLevel:PHAccessLevelReadWrite handler:^(PHAuthorizationStatus status) {
        [self reply:requestID result:@{ @"status": TDrivePhotoAuthorizationName(status), @"canRead": @(status == PHAuthorizationStatusAuthorized || status == PHAuthorizationStatusLimited), @"limited": @(status == PHAuthorizationStatusLimited) } error:nil];
    }];
}

- (BOOL)ensureReadable:(NSString *)requestID {
    PHAuthorizationStatus status = [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
    if (status == PHAuthorizationStatusAuthorized || status == PHAuthorizationStatusLimited) return YES;
    [self reply:requestID result:nil error:TDrivePhotoError(@"photoAccessRequired", @"Photo library access has not been granted")];
    return NO;
}

- (void)listAlbums:(NSString *)requestID arguments:(NSDictionary *)arguments {
    if (![self ensureReadable:requestID]) return;
    NSUInteger pageSize = TDriveBoundedPageSize(arguments[@"pageSize"]);
    NSUInteger offset = TDriveCursorOffset(arguments[@"cursor"]);
    NSArray<PHFetchResult<PHAssetCollection *> *> *fetches = @[
        [PHAssetCollection fetchAssetCollectionsWithType:PHAssetCollectionTypeSmartAlbum subtype:PHAssetCollectionSubtypeAny options:nil],
        [PHAssetCollection fetchAssetCollectionsWithType:PHAssetCollectionTypeAlbum subtype:PHAssetCollectionSubtypeAny options:nil],
    ];
    NSUInteger total = 0;
    for (PHFetchResult *fetch in fetches) total += fetch.count;
    NSUInteger end = MIN(total, offset + pageSize);
    NSMutableArray *items = [NSMutableArray array];
    for (NSUInteger globalIndex = MIN(offset, total); globalIndex < end; globalIndex++) {
        NSUInteger localIndex = globalIndex;
        PHAssetCollection *collection = nil;
        for (PHFetchResult<PHAssetCollection *> *fetch in fetches) {
            if (localIndex < fetch.count) { collection = fetch[localIndex]; break; }
            localIndex -= fetch.count;
        }
        if (collection == nil) continue;
        NSUInteger count = [PHAsset fetchAssetsInAssetCollection:collection options:nil].count;
        [items addObject:@{ @"id": collection.localIdentifier, @"title": collection.localizedTitle ?: @"", @"estimatedAssetCount": @(count), @"type": @(collection.assetCollectionType), @"subtype": @(collection.assetCollectionSubtype) }];
    }
    [self reply:requestID result:@{ @"items": items, @"nextCursor": end < total ? [NSString stringWithFormat:@"%lu", (unsigned long)end] : (id)[NSNull null] } error:nil];
}

- (void)listAssets:(NSString *)requestID arguments:(NSDictionary *)arguments {
    if (![self ensureReadable:requestID]) return;
    NSUInteger pageSize = TDriveBoundedPageSize(arguments[@"pageSize"]);
    NSUInteger offset = TDriveCursorOffset(arguments[@"cursor"]);
    PHFetchOptions *options = [[PHFetchOptions alloc] init];
    options.sortDescriptors = @[[NSSortDescriptor sortDescriptorWithKey:@"creationDate" ascending:YES]];
    NSArray *mediaTypes = [arguments[@"mediaTypes"] isKindOfClass:[NSArray class]] ? arguments[@"mediaTypes"] : nil;
    if (mediaTypes.count > 0) {
        NSMutableArray *types = [NSMutableArray array];
        if ([mediaTypes containsObject:@"image"]) [types addObject:@(PHAssetMediaTypeImage)];
        if ([mediaTypes containsObject:@"video"]) [types addObject:@(PHAssetMediaTypeVideo)];
        if (types.count > 0) options.predicate = [NSPredicate predicateWithFormat:@"mediaType IN %@", types];
    }
    PHFetchResult<PHAsset *> *fetch = nil;
    NSString *albumID = [arguments[@"albumID"] isKindOfClass:[NSString class]] ? arguments[@"albumID"] : nil;
    if (albumID.length > 0) {
        PHFetchResult<PHAssetCollection *> *album = [PHAssetCollection fetchAssetCollectionsWithLocalIdentifiers:@[albumID] options:nil];
        if (album.firstObject == nil) {
            [self reply:requestID result:nil error:TDrivePhotoError(@"albumNotFound", @"The requested album no longer exists")];
            return;
        }
        fetch = [PHAsset fetchAssetsInAssetCollection:album.firstObject options:options];
    } else {
        fetch = [PHAsset fetchAssetsWithOptions:options];
    }
    NSUInteger end = MIN(fetch.count, offset + pageSize);
    NSMutableArray *items = [NSMutableArray arrayWithCapacity:end - MIN(offset, fetch.count)];
    for (NSUInteger i = MIN(offset, fetch.count); i < end; i++) [items addObject:TDriveAssetManifest(fetch[i])];
    [self reply:requestID result:@{ @"items": items, @"nextCursor": end < fetch.count ? [NSString stringWithFormat:@"%lu", (unsigned long)end] : (id)[NSNull null] } error:nil];
}

// One page of a backup scan. The cursor is "token:asset:resource" against the
// snapshot the token names, so a library that changes mid-scan cannot shift
// indexes under the page: the token stops matching and the caller restarts.
- (void)listBackupAssets:(NSString *)requestID arguments:(NSDictionary *)arguments {
    NSString *pickedRoot = TDriveFolderRootForSource([arguments[@"sourceID"] isKindOfClass:[NSString class]] ? arguments[@"sourceID"] : @"");
    // A folder the user handed over is read on its own grant, so it is served
    // before the photo library's permission is consulted at all.
    if (pickedRoot != nil) { [self listFolderAssets:requestID root:pickedRoot arguments:arguments]; return; }
    if (![self ensureReadable:requestID]) return;
    NSUInteger pageSize = TDriveBoundedPageSize(arguments[@"pageSize"]);
    NSString *sourceID = [arguments[@"sourceID"] isKindOfClass:[NSString class]] && [arguments[@"sourceID"] length] > 0 ? arguments[@"sourceID"] : @"all";
    NSString *cursor = [arguments[@"cursor"] isKindOfClass:[NSString class]] ? arguments[@"cursor"] : @"";
    NSUInteger assetOffset = 0, resourceOffset = 0;
    NSString *token = nil;
    PHFetchResult<PHAsset *> *fetch = nil;
    @synchronized (self.scanSnapshots) {
        if (cursor.length > 0) {
            // The live token is the only thing that makes a cursor valid, so a
            // forged or stale one is refused rather than silently read as an
            // offset into whatever snapshot happens to be there.
            NSString *live = self.scanTokens[sourceID];
            NSArray<NSString *> *parts = [cursor componentsSeparatedByString:@":"];
            fetch = live.length > 0 ? self.scanSnapshots[sourceID] : nil;
            if (parts.count != 3 || live.length == 0 || ![parts[0] isEqualToString:live] || fetch == nil) {
                [self reply:requestID result:nil error:TDrivePhotoError(@"cursorExpired", @"The photo library changed; scan again from the start")];
                return;
            }
            token = live;
            assetOffset = TDriveCursorOffset(parts[1]);
            resourceOffset = TDriveCursorOffset(parts[2]);
        } else {
            fetch = [self fetchBackupAssetsForSource:sourceID];
            if (fetch == nil) { [self reply:requestID result:nil error:TDrivePhotoError(@"albumNotFound", @"The requested album no longer exists")]; return; }
            token = [NSUUID.UUID.UUIDString substringToIndex:8];
            self.scanSnapshots[sourceID] = fetch;
            self.scanTokens[sourceID] = token;
        }
    }
    NSMutableArray *items = [NSMutableArray arrayWithCapacity:pageSize];
    NSUInteger nextAsset = assetOffset, nextResource = resourceOffset;
    BOOL hasMore = NO;
    for (NSUInteger assetIndex = MIN(assetOffset, fetch.count); assetIndex < fetch.count; assetIndex++) {
        PHAsset *asset = fetch[assetIndex];
        // One resource lookup per asset: it is the slow call in this loop.
        NSArray<PHAssetResource *> *resources = [PHAssetResource assetResourcesForAsset:asset];
        NSUInteger primaryResource = TDriveDefaultResourceIndex(asset, resources);
        NSString *assetVersion = TDriveAssetVersion(asset, resources);
        NSNumber *modifiedMillis = TDriveMillis(asset.modificationDate);
        NSNumber *createdMillis = TDriveMillis(asset.creationDate);
        NSUInteger start = assetIndex == assetOffset ? resourceOffset : 0;
        for (NSUInteger resourceIndex = start; resourceIndex < resources.count; resourceIndex++) {
            PHAssetResource *resource = resources[resourceIndex];
            if (resourceIndex != primaryResource && resource.type != PHAssetResourceTypePairedVideo) continue;
            if (items.count == pageSize) { nextAsset = assetIndex; nextResource = resourceIndex; hasMore = YES; goto complete; }
            NSString *resourceID = TDriveResourceID(resource, resourceIndex);
            NSString *mediaType = (resource.type == PHAssetResourceTypeVideo || resource.type == PHAssetResourceTypeFullSizeVideo || resource.type == PHAssetResourceTypePairedVideo) ? @"video" : @"image";
            NSString *backupMediaType = [mediaType isEqualToString:@"video"] ? @"video" : @"photo";
            [items addObject:@{ @"id": asset.localIdentifier, @"assetID": asset.localIdentifier, @"groupID": asset.localIdentifier, @"resourceID": resourceID, @"resource_id": resourceID, @"version": assetVersion, @"name": resource.originalFilename ?: @"resource", @"mediaType": mediaType, @"media_type": backupMediaType, @"modifiedAt": modifiedMillis, @"modified_at": modifiedMillis, @"createdAt": createdMillis, @"created_at": createdMillis, @"size": @(TDriveResourceFileSize(resource)), @"resource": TDriveResourceManifest(resource, resourceIndex) }];
        }
        nextAsset = assetIndex + 1;
        nextResource = 0;
    }
complete:
    if (!hasMore) {
        // Keep the finished snapshot: it is what the next library change is
        // diffed against, so a favourite or a delete does not cost a rescan.
        @synchronized (self.scanSnapshots) { [self.scanTokens removeObjectForKey:sourceID]; }
    }
    [self reply:requestID result:@{ @"assets": items, @"nextCursor": hasMore ? [NSString stringWithFormat:@"%@:%lu:%lu", token, (unsigned long)nextAsset, (unsigned long)nextResource] : (id)[NSNull null] } error:nil];
}

#pragma mark - Picked folders

// The bookmarks, keyed by the root the ledger stores. NSUserDefaults is the
// right home for them: they are small, they must survive a relaunch, and the
// ledger they belong to is the app's own.
- (NSDictionary<NSString *, NSData *> *)folderBookmarks {
    NSDictionary *stored = [[NSUserDefaults standardUserDefaults] dictionaryForKey:TDriveFolderBookmarksKey];
    return [stored isKindOfClass:[NSDictionary class]] ? stored : @{};
}

- (void)rememberFolder:(NSData *)bookmark forRoot:(NSString *)root {
    NSMutableDictionary *all = [[self folderBookmarks] mutableCopy];
    all[root] = bookmark;
    [[NSUserDefaults standardUserDefaults] setObject:all forKey:TDriveFolderBookmarksKey];
}

/**
 * The folder a root names, with its security scope open, or nil when the
 * bookmark is gone or no longer resolves -- a folder deleted, or on a volume
 * that is not mounted. The caller stops the scope when it is done.
 */
- (NSURL *)openFolderForRoot:(NSString *)root {
    NSData *bookmark = [self folderBookmarks][root];
    if (![bookmark isKindOfClass:[NSData class]]) return nil;
    BOOL stale = NO;
    NSError *error = nil;
    NSURL *url = [NSURL URLByResolvingBookmarkData:bookmark options:0 relativeToURL:nil bookmarkDataIsStale:&stale error:&error];
    if (url == nil) return nil;
    if (![url startAccessingSecurityScopedResource]) return nil;
    // A stale bookmark still resolved, so it is rewritten while the scope is
    // open rather than left to fail on some later launch.
    if (stale) {
        NSData *refreshed = [url bookmarkDataWithOptions:0 includingResourceValuesForKeys:nil relativeToURL:nil error:nil];
        if (refreshed != nil) [self rememberFolder:refreshed forRoot:root];
    }
    return url;
}

// The system picker, in folder mode -- the same one the app opens to upload a
// folder. What comes back is a place, not a copy: the bookmark is taken before
// anything else, because the grant is only certain while this scope is open.
- (void)pickBackupFolder:(NSString *)requestID {
    if (self.pendingFolderPick.length > 0) { [self reply:requestID result:nil error:TDrivePhotoError(@"pickerBusy", @"A folder picker is already open")]; return; }
    UIViewController *presenter = self.presenter;
    if (presenter == nil) { [self reply:requestID result:nil error:TDrivePhotoError(@"unsupportedOperation", @"This build cannot open a folder picker")]; return; }
    self.pendingFolderPick = requestID;
    dispatch_async(dispatch_get_main_queue(), ^{
        UIDocumentPickerViewController *picker = [[UIDocumentPickerViewController alloc] initForOpeningContentTypes:@[UTTypeFolder] asCopy:NO];
        picker.delegate = self;
        picker.allowsMultipleSelection = NO;
        UIViewController *top = presenter;
        while (top.presentedViewController != nil) top = top.presentedViewController;
        [top presentViewController:picker animated:YES completion:nil];
    });
}

- (void)documentPicker:(UIDocumentPickerViewController *)controller didPickDocumentsAtURLs:(NSArray<NSURL *> *)urls {
    (void)controller;
    NSString *requestID = self.pendingFolderPick;
    self.pendingFolderPick = nil;
    if (requestID.length == 0) return;
    NSURL *url = urls.firstObject;
    if (url == nil) { [self reply:requestID result:@{} error:nil]; return; }
    BOOL scoped = [url startAccessingSecurityScopedResource];
    NSNumber *isDirectory = nil;
    [url getResourceValue:&isDirectory forKey:NSURLIsDirectoryKey error:nil];
    NSString *root = isDirectory.boolValue ? TDriveNormalizedFolderRoot(url.path) : nil;
    NSError *error = nil;
    NSData *bookmark = root == nil ? nil : [url bookmarkDataWithOptions:0 includingResourceValuesForKeys:nil relativeToURL:nil error:&error];
    if (scoped) [url stopAccessingSecurityScopedResource];
    if (root == nil) { [self reply:requestID result:nil error:TDrivePhotoError(@"invalidFolder", @"Choose a folder, not a file")]; return; }
    if (bookmark == nil) { [self reply:requestID result:nil error:TDrivePhotoError(@"invalidFolder", error.localizedDescription ?: @"That folder cannot be kept between launches")]; return; }
    [self rememberFolder:bookmark forRoot:root];
    [self reply:requestID result:@{ @"id": [@"tree:" stringByAppendingString:root], @"root": root, @"kind": @"device-folder", @"name": url.lastPathComponent ?: @"Folder" } error:nil];
}

- (void)documentPickerWasCancelled:(UIDocumentPickerViewController *)controller {
    (void)controller;
    NSString *requestID = self.pendingFolderPick;
    self.pendingFolderPick = nil;
    // A dismissal is an answer, not a failure: the empty reply is what the
    // adapter reads as "nothing was picked".
    if (requestID.length > 0) [self reply:requestID result:@{} error:nil];
}

/** Every photo and video under a picked folder, deepest paths included. */
- (NSArray<NSDictionary *> *)walkFolder:(NSURL *)folder root:(NSURL *)root {
    NSMutableArray<NSDictionary *> *items = [NSMutableArray array];
    NSArray<NSURLResourceKey> *keys = @[NSURLIsDirectoryKey, NSURLNameKey, NSURLFileSizeKey, NSURLContentModificationDateKey, NSURLCreationDateKey];
    NSDirectoryEnumerator<NSURL *> *walk = [[NSFileManager defaultManager] enumeratorAtURL:folder includingPropertiesForKeys:keys options:NSDirectoryEnumerationSkipsHiddenFiles | NSDirectoryEnumerationSkipsPackageDescendants errorHandler:nil];
    NSString *base = root.path;
    for (NSURL *url in walk) {
        if (items.count >= TDriveFolderMaxEntries) break;
        NSNumber *isDirectory = nil;
        [url getResourceValue:&isDirectory forKey:NSURLIsDirectoryKey error:nil];
        if (isDirectory.boolValue) continue;
        NSString *name = url.lastPathComponent ?: @"";
        NSString *kind = TDriveFolderMediaKind(name);
        if (kind == nil) continue;
        NSNumber *size = nil, *ignored = nil;
        NSDate *modified = nil, *created = nil;
        [url getResourceValue:&size forKey:NSURLFileSizeKey error:nil];
        [url getResourceValue:&ignored forKey:NSURLIsDirectoryKey error:nil];
        [url getResourceValue:&modified forKey:NSURLContentModificationDateKey error:nil];
        [url getResourceValue:&created forKey:NSURLCreationDateKey error:nil];
        NSString *path = url.path;
        NSString *relative = [path hasPrefix:base] ? [path substringFromIndex:base.length] : name;
        while ([relative hasPrefix:@"/"]) relative = [relative substringFromIndex:1];
        NSString *relativeDirectory = relative.stringByDeletingLastPathComponent ?: @"";
        // The version moves when the file does: an edit changes one of these
        // two, and the ledger sends it again rather than believing the copy it
        // already has.
        NSString *version = [NSString stringWithFormat:@"%.0f:%llu", modified.timeIntervalSince1970, size.unsignedLongLongValue];
        [items addObject:@{
            @"relative": relative,
            @"path": path,
            @"name": name,
            @"kind": kind,
            @"relDir": relativeDirectory,
            @"version": version,
            @"size": size ?: @0,
            @"modified": TDriveMillis(modified),
            @"created": TDriveMillis(created ?: modified),
        }];
    }
    return items;
}

// One page of a folder scan, against the listing the token names. A folder
// needs no photo permission: the user handed this one over themselves.
- (void)listFolderAssets:(NSString *)requestID root:(NSString *)root arguments:(NSDictionary *)arguments {
    NSUInteger pageSize = TDriveBoundedPageSize(arguments[@"pageSize"]);
    NSString *cursor = [arguments[@"cursor"] isKindOfClass:[NSString class]] ? arguments[@"cursor"] : @"";
    NSArray<NSDictionary *> *listing = nil;
    NSUInteger offset = 0;
    NSString *token = nil;
    @synchronized (self.folderSnapshots) {
        if (cursor.length > 0) {
            NSString *live = self.folderTokens[root];
            NSArray<NSString *> *parts = [cursor componentsSeparatedByString:@":"];
            listing = live.length > 0 ? self.folderSnapshots[root] : nil;
            if (parts.count != 2 || live.length == 0 || ![parts[0] isEqualToString:live] || listing == nil) {
                [self reply:requestID result:nil error:TDrivePhotoError(@"cursorExpired", @"The folder changed; scan again from the start")];
                return;
            }
            token = live;
            offset = TDriveCursorOffset(parts[1]);
        }
    }
    if (listing == nil) {
        NSURL *folder = [self openFolderForRoot:root];
        if (folder == nil) { [self reply:requestID result:nil error:TDrivePhotoError(@"folderUnavailable", @"That folder is no longer available. Add it again to keep backing it up.")]; return; }
        listing = [self walkFolder:folder root:folder];
        [folder stopAccessingSecurityScopedResource];
        token = [NSUUID.UUID.UUIDString substringToIndex:8];
        @synchronized (self.folderSnapshots) {
            self.folderSnapshots[root] = listing;
            self.folderTokens[root] = token;
        }
    }
    NSUInteger end = MIN(listing.count, offset + pageSize);
    NSMutableArray *assets = [NSMutableArray arrayWithCapacity:end - MIN(offset, listing.count)];
    for (NSUInteger i = MIN(offset, listing.count); i < end; i++) {
        NSDictionary *item = listing[i];
        // The id carries the root, because a staged file is asked for by id
        // alone and the folder it came from is the only way to open it.
        NSString *assetID = [root stringByAppendingString:item[@"relative"]];
        [assets addObject:@{
            @"id": assetID, @"version": item[@"version"], @"name": item[@"name"],
            @"media_type": item[@"kind"], @"mediaType": item[@"kind"],
            @"modified_at": item[@"modified"], @"modifiedAt": item[@"modified"],
            @"created_at": item[@"created"], @"createdAt": item[@"created"],
            @"rel_dir": item[@"relDir"], @"relDir": item[@"relDir"],
            @"size": item[@"size"],
        }];
    }
    BOOL hasMore = end < listing.count;
    if (!hasMore) @synchronized (self.folderSnapshots) { [self.folderTokens removeObjectForKey:root]; [self.folderSnapshots removeObjectForKey:root]; }
    [self reply:requestID result:@{ @"assets": assets, @"nextCursor": hasMore ? [NSString stringWithFormat:@"%@:%lu", token, (unsigned long)end] : (id)[NSNull null] } error:nil];
}

/** The picked folder an asset id belongs to, and its path inside it. */
- (NSString *)folderRootForAsset:(NSString *)assetID relative:(NSString **)relative {
    if (![assetID hasPrefix:TDriveFolderRootPrefix]) return nil;
    for (NSString *root in [self folderBookmarks]) {
        if (![assetID hasPrefix:root] || assetID.length <= root.length) continue;
        if (relative != NULL) *relative = [assetID substringFromIndex:root.length];
        return root;
    }
    return nil;
}

// Staging a file out of a picked folder. It is copied rather than handed over
// in place: the upload reads it from Go, outside any security scope this
// process is holding, and a folder in iCloud Drive may not be on the device
// until something asks for it.
- (void)materializeFolderAsset:(NSString *)requestID root:(NSString *)root relative:(NSString *)relative arguments:(NSDictionary *)arguments {
    @synchronized (self.materializations) {
        if (self.materializations.count > 0 || self.stagedTokens.count > 0) {
            [self reply:requestID result:nil error:TDrivePhotoError(@"stagingBusy", @"Release the current staged photo resource before staging another")];
            return;
        }
    }
    NSURL *folder = [self openFolderForRoot:root];
    if (folder == nil) { [self reply:requestID result:nil error:TDrivePhotoError(@"folderUnavailable", @"That folder is no longer available. Add it again to keep backing it up.")]; return; }
    NSURL *source = folder;
    for (NSString *component in [relative componentsSeparatedByString:@"/"]) {
        if (component.length == 0 || [component isEqualToString:@"."] || [component isEqualToString:@".."]) continue;
        source = [source URLByAppendingPathComponent:component isDirectory:NO];
    }
    NSNumber *size = nil;
    NSDate *modified = nil;
    [source getResourceValue:&size forKey:NSURLFileSizeKey error:nil];
    [source getResourceValue:&modified forKey:NSURLContentModificationDateKey error:nil];
    NSString *expectedVersion = [arguments[@"version"] isKindOfClass:[NSString class]] ? arguments[@"version"] : nil;
    NSString *currentVersion = [NSString stringWithFormat:@"%.0f:%llu", modified.timeIntervalSince1970, size.unsignedLongLongValue];
    if (modified == nil) { [folder stopAccessingSecurityScopedResource]; [self reply:requestID result:nil error:TDrivePhotoError(@"assetNotFound", @"That file is no longer in the folder")]; return; }
    if (expectedVersion.length > 0 && ![expectedVersion isEqualToString:currentVersion]) { [folder stopAccessingSecurityScopedResource]; [self reply:requestID result:nil error:TDrivePhotoError(@"assetChanged", @"The file changed after discovery; refresh before backing it up")]; return; }

    unsigned long long requestedMax = [arguments[@"maxBytes"] respondsToSelector:@selector(unsignedLongLongValue)] ? [arguments[@"maxBytes"] unsignedLongLongValue] : TDrivePhotoDefaultMaxBytes;
    unsigned long long maxBytes = MAX(1, MIN(TDrivePhotoMaximumMaxBytes, requestedMax));
    NSURL *cache = [[[NSFileManager defaultManager] URLsForDirectory:NSCachesDirectory inDomains:NSUserDomainMask] firstObject];
    NSNumber *availableCapacity = nil;
    [cache getResourceValue:&availableCapacity forKey:NSURLVolumeAvailableCapacityForImportantUsageKey error:nil];
    if (availableCapacity == nil || availableCapacity.unsignedLongLongValue <= TDrivePhotoMinimumFreeBytes) {
        [folder stopAccessingSecurityScopedResource];
        [self reply:requestID result:nil error:TDrivePhotoError(@"insufficientSpace", @"At least 512 MiB of free space is required to stage a photo or video")];
        return;
    }
    unsigned long long stagingLimit = MIN(maxBytes, availableCapacity.unsignedLongLongValue - TDrivePhotoMinimumFreeBytes);
    if (size.unsignedLongLongValue > stagingLimit) {
        [folder stopAccessingSecurityScopedResource];
        BOOL diskLimited = stagingLimit < maxBytes;
        [self reply:requestID result:nil
              error:TDrivePhotoError(diskLimited ? @"insufficientSpace" : @"resourceTooLarge",
                                     diskLimited ? @"Staging this file would leave less than 512 MiB free" : @"That file is larger than the staging limit")];
        return;
    }
    NSString *token = NSUUID.UUID.UUIDString;
    NSURL *directory = [[cache URLByAppendingPathComponent:@"TDrivePhotoBackup" isDirectory:YES] URLByAppendingPathComponent:token isDirectory:YES];
    NSError *error = nil;
    if (![[NSFileManager defaultManager] createDirectoryAtURL:directory withIntermediateDirectories:YES attributes:nil error:&error]) {
        [folder stopAccessingSecurityScopedResource];
        [self reply:requestID result:nil error:TDrivePhotoError(@"stagingFailed", error.localizedDescription)];
        return;
    }
    NSURL *staged = [directory URLByAppendingPathComponent:source.lastPathComponent.length ? source.lastPathComponent : @"file" isDirectory:NO];
    // A coordinated read is what pulls an iCloud Drive file down before it is
    // copied; an uncoordinated one would copy a placeholder.
    __block BOOL copied = NO;
    __block NSError *copyError = nil;
    NSFileCoordinator *coordinator = [[NSFileCoordinator alloc] initWithFilePresenter:nil];
    [coordinator coordinateReadingItemAtURL:source options:NSFileCoordinatorReadingWithoutChanges error:&error byAccessor:^(NSURL *readable) {
        copied = [[NSFileManager defaultManager] copyItemAtURL:readable toURL:staged error:&copyError];
    }];
    [folder stopAccessingSecurityScopedResource];
    if (!copied) {
        [[NSFileManager defaultManager] removeItemAtURL:directory error:nil];
        [self reply:requestID result:nil error:TDrivePhotoError(@"materializeFailed", (copyError ?: error).localizedDescription ?: @"That file could not be read")];
        return;
    }
    @synchronized (self.materializations) { [self.stagedTokens addObject:token]; }
    [self reply:requestID result:@{ @"token": token, @"path": staged.path, @"byteSize": size ?: @0, @"assetID": [root stringByAppendingString:relative], @"assetVersion": currentVersion } error:nil];
}

- (void)materialize:(NSString *)requestID arguments:(NSDictionary *)arguments {
    NSString *relative = nil;
    NSString *pickedRoot = [self folderRootForAsset:[arguments[@"assetID"] isKindOfClass:[NSString class]] ? arguments[@"assetID"] : @"" relative:&relative];
    if (pickedRoot != nil) { [self materializeFolderAsset:requestID root:pickedRoot relative:relative arguments:arguments]; return; }
    if (![self ensureReadable:requestID]) return;
    @synchronized (self.materializations) {
        if (self.materializations.count > 0 || self.stagedTokens.count > 0) {
            [self reply:requestID result:nil error:TDrivePhotoError(@"stagingBusy", @"Release the current staged photo resource before staging another")];
            return;
        }
    }
    NSString *assetID = [arguments[@"assetID"] isKindOfClass:[NSString class]] ? arguments[@"assetID"] : nil;
    if (assetID.length == 0) { [self reply:requestID result:nil error:TDrivePhotoError(@"invalidArgument", @"assetID is required")]; return; }
    PHAsset *asset = [PHAsset fetchAssetsWithLocalIdentifiers:@[assetID] options:nil].firstObject;
    if (asset == nil) { [self reply:requestID result:nil error:TDrivePhotoError(@"assetNotFound", @"The requested asset no longer exists")]; return; }
    NSString *expectedVersion = [arguments[@"version"] isKindOfClass:[NSString class]] ? arguments[@"version"] : nil;
    NSString *currentVersion = TDriveAssetManifest(asset)[@"version"];
    if (expectedVersion.length > 0 && ![expectedVersion isEqualToString:currentVersion]) { [self reply:requestID result:nil error:TDrivePhotoError(@"assetChanged", @"The photo changed after discovery; refresh before backing it up")]; return; }
    NSArray<PHAssetResource *> *resources = [PHAssetResource assetResourcesForAsset:asset];
    NSString *resourceID = [arguments[@"resourceID"] isKindOfClass:[NSString class]] ? arguments[@"resourceID"] : nil;
    NSUInteger selected = resourceID.length == 0 ? TDriveDefaultResourceIndex(asset, resources) : NSNotFound;
    if (resourceID.length > 0) for (NSUInteger i = 0; i < resources.count; i++) if ([TDriveResourceID(resources[i], i) isEqualToString:resourceID]) { selected = i; break; }
    if (selected == NSNotFound) { [self reply:requestID result:nil error:TDrivePhotoError(@"resourceNotFound", @"The requested photo resource no longer exists")]; return; }
    unsigned long long requestedMax = [arguments[@"maxBytes"] respondsToSelector:@selector(unsignedLongLongValue)] ? [arguments[@"maxBytes"] unsignedLongLongValue] : TDrivePhotoDefaultMaxBytes;
    unsigned long long maxBytes = MAX(1, MIN(TDrivePhotoMaximumMaxBytes, requestedMax));
    NSString *token = NSUUID.UUID.UUIDString;
    NSURL *root = [[[NSFileManager defaultManager] URLsForDirectory:NSCachesDirectory inDomains:NSUserDomainMask] firstObject];
    NSNumber *availableCapacity = nil;
    NSError *capacityError = nil;
    [root getResourceValue:&availableCapacity forKey:NSURLVolumeAvailableCapacityForImportantUsageKey error:&capacityError];
    if (availableCapacity == nil || availableCapacity.unsignedLongLongValue <= TDrivePhotoMinimumFreeBytes) {
        [self reply:requestID result:nil error:TDrivePhotoError(@"insufficientSpace", capacityError.localizedDescription ?: @"At least 512 MiB of free space is required to stage a photo or video")];
        return;
    }
    unsigned long long diskBound = availableCapacity.unsignedLongLongValue - TDrivePhotoMinimumFreeBytes;
    unsigned long long stagingLimit = MIN(maxBytes, diskBound);
    NSURL *directory = [[root URLByAppendingPathComponent:@"TDrivePhotoBackup" isDirectory:YES] URLByAppendingPathComponent:token isDirectory:YES];
    NSError *directoryError = nil;
    if (![[NSFileManager defaultManager] createDirectoryAtURL:directory withIntermediateDirectories:YES attributes:nil error:&directoryError]) { [self reply:requestID result:nil error:TDrivePhotoError(@"stagingFailed", directoryError.localizedDescription)]; return; }
    PHAssetResource *resource = resources[selected];
    NSString *filename = resource.originalFilename.lastPathComponent.length ? resource.originalFilename.lastPathComponent : @"resource";
    NSURL *fileURL = [directory URLByAppendingPathComponent:filename isDirectory:NO];
    [[NSFileManager defaultManager] createFileAtPath:fileURL.path contents:nil attributes:@{NSFileProtectionKey: NSFileProtectionCompleteUntilFirstUserAuthentication}];
    NSFileHandle *handle = [NSFileHandle fileHandleForWritingToURL:fileURL error:&directoryError];
    if (handle == nil) { [[NSFileManager defaultManager] removeItemAtURL:directory error:nil]; [self reply:requestID result:nil error:TDrivePhotoError(@"stagingFailed", directoryError.localizedDescription)]; return; }
    PHAssetResourceRequestOptions *options = [[PHAssetResourceRequestOptions alloc] init];
    options.networkAccessAllowed = YES;
    __block unsigned long long written = 0;
    __block BOOL exceeded = NO;
    __block PHAssetResourceDataRequestID dataRequestID = PHInvalidAssetResourceDataRequestID;
    dataRequestID = [[PHAssetResourceManager defaultManager] requestDataForAssetResource:resource options:options dataReceivedHandler:^(NSData *data) {
        if (exceeded) return;
        if (data.length > stagingLimit - written) {
            exceeded = YES;
            [[PHAssetResourceManager defaultManager] cancelDataRequest:dataRequestID];
            return;
        }
        @try { [handle writeData:data]; written += data.length; } @catch (__unused NSException *exception) { exceeded = YES; [[PHAssetResourceManager defaultManager] cancelDataRequest:dataRequestID]; }
    } completionHandler:^(NSError *error) {
        [handle closeFile];
        if (exceeded || error != nil) {
            @synchronized (self.materializations) { [self.materializations removeObjectForKey:requestID]; }
            [[NSFileManager defaultManager] removeItemAtURL:directory error:nil];
            BOOL diskLimited = stagingLimit < maxBytes;
            NSString *code = exceeded ? (diskLimited ? @"insufficientSpace" : @"resourceTooLarge") : @"materializeFailed";
            NSString *message = exceeded ? (diskLimited ? @"Staging this resource would leave less than 512 MiB free" : @"Photo resource exceeds the requested staging limit") : error.localizedDescription;
            [self reply:requestID result:nil error:TDrivePhotoError(code, message)];
            return;
        }
        PHAsset *currentAsset = [PHAsset fetchAssetsWithLocalIdentifiers:@[asset.localIdentifier] options:nil].firstObject;
        NSString *materializedVersion = currentAsset ? TDriveAssetManifest(currentAsset)[@"version"] : nil;
        if (materializedVersion == nil || ![materializedVersion isEqualToString:currentVersion]) {
            @synchronized (self.materializations) { [self.materializations removeObjectForKey:requestID]; }
            [[NSFileManager defaultManager] removeItemAtURL:directory error:nil];
            [self reply:requestID result:nil error:TDrivePhotoError(@"assetChanged", @"The photo changed while it was being staged; refresh before backing it up")];
            return;
        }
        @synchronized (self.materializations) {
            [self.materializations removeObjectForKey:requestID];
            [self.stagedTokens addObject:token];
        }
        [self reply:requestID result:@{ @"token": token, @"path": fileURL.path, @"byteSize": @(written), @"assetID": asset.localIdentifier, @"assetVersion": materializedVersion, @"resource": TDriveResourceManifest(resource, selected) } error:nil];
    }];
    @synchronized (self.materializations) { self.materializations[requestID] = @(dataRequestID); }
}

- (void)cancelMaterialization:(NSString *)requestID arguments:(NSDictionary *)arguments {
    NSString *materializationID = [arguments[@"requestID"] isKindOfClass:[NSString class]] ? arguments[@"requestID"] : nil;
    NSNumber *dataRequest = nil;
    @synchronized (self.materializations) { dataRequest = self.materializations[materializationID]; }
    if (dataRequest != nil) [[PHAssetResourceManager defaultManager] cancelDataRequest:(PHAssetResourceDataRequestID)dataRequest.intValue];
    [self reply:requestID result:@{ @"cancelled": @(dataRequest != nil) } error:nil];
}

- (void)cleanup:(NSString *)requestID arguments:(NSDictionary *)arguments {
    NSString *token = [arguments[@"token"] isKindOfClass:[NSString class]] ? arguments[@"token"] : nil;
    if (token.length == 0 || [token containsString:@"/"] || [token containsString:@".."] || ![[NSUUID alloc] initWithUUIDString:token]) { [self reply:requestID result:nil error:TDrivePhotoError(@"invalidArgument", @"A valid staging token is required")]; return; }
    NSURL *root = [[[NSFileManager defaultManager] URLsForDirectory:NSCachesDirectory inDomains:NSUserDomainMask] firstObject];
    NSURL *directory = [[root URLByAppendingPathComponent:@"TDrivePhotoBackup" isDirectory:YES] URLByAppendingPathComponent:token isDirectory:YES];
    NSError *error = nil;
    BOOL existed = [[NSFileManager defaultManager] fileExistsAtPath:directory.path];
    if (existed && ![[NSFileManager defaultManager] removeItemAtURL:directory error:&error]) { [self reply:requestID result:nil error:TDrivePhotoError(@"cleanupFailed", error.localizedDescription)]; return; }
    @synchronized (self.materializations) { [self.stagedTokens removeObject:token]; }
    [self reply:requestID result:@{ @"removed": @(existed) } error:nil];
}

- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)message {
    (void)controller;
    NSURL *origin = message.frameInfo.request.URL;
    NSString *host = origin.host.lowercaseString ?: @"";
    BOOL trustedOrigin = [origin.scheme isEqualToString:@"wails"]
        || (([origin.scheme isEqualToString:@"http"] || [origin.scheme isEqualToString:@"https"])
            && ([host isEqualToString:@"localhost"] || [host isEqualToString:@"127.0.0.1"] || [host isEqualToString:@"::1"] || [host hasSuffix:@".localhost"]));
    if (!message.frameInfo.isMainFrame || !trustedOrigin) return;
    NSDictionary *body = [message.body isKindOfClass:[NSDictionary class]] ? message.body : nil;
    NSString *requestID = [body[@"id"] isKindOfClass:[NSString class]] ? body[@"id"] : @"";
    NSString *operation = [body[@"operation"] isKindOfClass:[NSString class]] ? body[@"operation"] : @"";
    NSDictionary *arguments = [body[@"arguments"] isKindOfClass:[NSDictionary class]] ? body[@"arguments"] : @{};
    if ([operation isEqualToString:@"status"]) [self status:requestID];
    else if ([operation isEqualToString:@"requestAuthorization"]) [self requestAuthorization:requestID];
    else if ([operation isEqualToString:@"listAlbums"]) [self listAlbums:requestID arguments:arguments];
    else if ([operation isEqualToString:@"listAssets"]) [self listAssets:requestID arguments:arguments];
    else if ([operation isEqualToString:@"listBackupAssets"]) [self listBackupAssets:requestID arguments:arguments];
    else if ([operation isEqualToString:@"materialize"]) [self materialize:requestID arguments:arguments];
    else if ([operation isEqualToString:@"cancelMaterialization"]) [self cancelMaterialization:requestID arguments:arguments];
    else if ([operation isEqualToString:@"cleanup"]) [self cleanup:requestID arguments:arguments];
    else if ([operation isEqualToString:@"setBackgroundBackup"]) [self setBackgroundBackup:requestID arguments:arguments];
    else if ([operation isEqualToString:@"pickBackupFolder"]) [self pickBackupFolder:requestID];
    else [self reply:requestID result:nil error:TDrivePhotoError(@"unsupportedOperation", @"Unsupported photo library operation")];
}
@end

static char TDrivePhotoBridgeKey;

static void TDriveInstallPhotoBridge(WKWebView *webView, UIViewController *presenter) {
    if (webView == nil || objc_getAssociatedObject(webView, &TDrivePhotoBridgeKey) != nil) return;
    TDrivePhotoBridge *bridge = [[TDrivePhotoBridge alloc] init];
    bridge.webView = webView;
    // Held weakly: the picker is presented from whatever is on screen at the
    // time, and the bridge must not keep the app's controller alive.
    bridge.presenter = presenter;
    [bridge startObserving];
    objc_setAssociatedObject(webView, &TDrivePhotoBridgeKey, bridge, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    WKUserContentController *controller = webView.configuration.userContentController;
    [controller addScriptMessageHandler:bridge name:TDrivePhotoHandlerName];
    NSString *source = @"(function(){if(window.tdriveIOSPhotos)return;const pending=new Map();let seq=0;window.__tdriveIOSPhotosReplyBase64=function(b){try{const bytes=Uint8Array.from(atob(b),c=>c.charCodeAt(0));const m=JSON.parse(new TextDecoder().decode(bytes));const p=pending.get(m.id);if(!p)return;pending.delete(m.id);m.error?p.reject(Object.assign(new Error(m.error.message||'Photo operation failed'),{code:m.error.code})):p.resolve(m.result)}catch(e){console.error('TDrive photo bridge reply failed',e)}};const call=(operation,args={})=>{const id=Date.now().toString(36)+'-'+(++seq).toString(36);const promise=new Promise((resolve,reject)=>{pending.set(id,{resolve,reject});window.webkit.messageHandlers.tdrivePhotos.postMessage({id,operation,arguments:args})});promise.requestID=id;return promise};const api={status:()=>call('status'),requestAuthorization:()=>call('requestAuthorization'),listAlbums:(args={})=>call('listAlbums',args),listAssets:(args={})=>call('listAssets',args),materialize:(args)=>call('materialize',args||{}),cancelMaterialization:(requestID)=>call('cancelMaterialization',{requestID}),cleanup:(token)=>call('cleanup',{token}),setBackgroundBackup:(active)=>call('setBackgroundBackup',{active:!!active}),pickPhotoBackupFolder:()=>call('pickBackupFolder')};api.getPhotoBackupCapabilities=async()=>{const s=await api.status();return{available:true,authorization:s.status,canRead:s.canRead,limited:s.limited,backgroundDiscovery:false,backgroundUpload:'limited',resumeRefresh:true,maxPageSize:128}};api.listPhotoBackupSources=async(cursor=null,limit=50)=>{const first=cursor===null||cursor==='';const albumCursor=first?null:String(cursor).replace(/^albums:/,'');if(first&&limit<=1)return{sources:[{id:'all',root:'phassetlibrary:all',kind:'library',name:'All accessible photos and videos'}],nextCursor:'albums:0'};const p=await api.listAlbums({cursor:albumCursor,pageSize:first?limit-1:limit});const albums=p.items.map(x=>({id:x.id,root:'phassetcollection:'+x.id,kind:'album',name:x.title,estimatedAssetCount:x.estimatedAssetCount}));return{sources:first?[{id:'all',root:'phassetlibrary:all',kind:'library',name:'All accessible photos and videos'},...albums]:albums,nextCursor:p.nextCursor===null?null:'albums:'+p.nextCursor}};api.listPhotoBackupAssets=(sourceID,cursor=null,limit=50)=>call('listBackupAssets',{sourceID,cursor,pageSize:limit});api.materializePhotoBackupResource=(assetID,version,maxBytes,resourceID)=>api.materialize({assetID,version,maxBytes,resourceID});api.releasePhotoBackupResource=token=>api.cleanup(token);window.tdriveIOSPhotos=Object.freeze(api)})();";
    [controller addUserScript:[[WKUserScript alloc] initWithSource:source injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:YES]];
    [webView evaluateJavaScript:source completionHandler:nil];
}
