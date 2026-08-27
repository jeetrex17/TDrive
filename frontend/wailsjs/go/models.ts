export namespace backend {

	export class FileMetaData {
	    name: string;
	    size: number;
	    msg_id: number;
	    parent_id: string;
	    upload_time: number;
	    uploader_id: number;
	    encrypted?: boolean;
	    plaintext_size?: number;

	    static createFrom(source: any = {}) {
	        return new FileMetaData(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.size = source["size"];
	        this.msg_id = source["msg_id"];
	        this.parent_id = source["parent_id"];
	        this.upload_time = source["upload_time"];
	        this.uploader_id = source["uploader_id"];
	        this.encrypted = source["encrypted"];
	        this.plaintext_size = source["plaintext_size"];
	    }
	}
	export class Folder {
	    name: string;
	    id: string;
	    parent_id: string;

	    static createFrom(source: any = {}) {
	        return new Folder(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.id = source["id"];
	        this.parent_id = source["parent_id"];
	    }
	}
	export class FileSystem {
	    folders: Folder[];
	    files: FileMetaData[];

	    static createFrom(source: any = {}) {
	        return new FileSystem(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.folders = this.convertValues(source["folders"], Folder);
	        this.files = this.convertValues(source["files"], FileMetaData);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class SearchResult {
	    type: string;
	    id: string;
	    name: string;
	    parent_id: string;
	    size: number;
	    upload_time: number;
	    uploader_id: number;
	    encrypted?: boolean;
	    plaintext_size?: number;
	    path: string;

	    static createFrom(source: any = {}) {
	        return new SearchResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.id = source["id"];
	        this.name = source["name"];
	        this.parent_id = source["parent_id"];
	        this.size = source["size"];
	        this.upload_time = source["upload_time"];
	        this.uploader_id = source["uploader_id"];
	        this.encrypted = source["encrypted"];
	        this.plaintext_size = source["plaintext_size"];
	        this.path = source["path"];
	    }
	}

}

export namespace file {

	export class ImportPlan {
	    files: number;
	    folders: number;
	    bytes: number;
	    oversize: number;
	    archives: number;
	    ignored: number;
	    maxBytes: number;
	    maxItems: number;
	    limitExceeded: boolean;
	    errorCount: number;
	    errors: string[];

	    static createFrom(source: any = {}) {
	        return new ImportPlan(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.folders = source["folders"];
	        this.bytes = source["bytes"];
	        this.oversize = source["oversize"];
	        this.archives = source["archives"];
	        this.ignored = source["ignored"];
	        this.maxBytes = source["maxBytes"];
	        this.maxItems = source["maxItems"];
	        this.limitExceeded = source["limitExceeded"];
	        this.errorCount = source["errorCount"];
	        this.errors = source["errors"];
	    }
	}

}

export namespace main {

	export class AppVersionInfo {
	    version: string;
	    os: string;
	    arch: string;
	    dev_build: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AppVersionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.os = source["os"];
	        this.arch = source["arch"];
	        this.dev_build = source["dev_build"];
	    }
	}
	export class ChannelInfo {
	    id: number;
	    title: string;
	    kind: string;
	    is_active: boolean;
	    invite_link?: string;

	    static createFrom(source: any = {}) {
	        return new ChannelInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	        this.is_active = source["is_active"];
	        this.invite_link = source["invite_link"];
	    }
	}
	export class DownloadResult {
	    status: string;
	    message: string;
	    saved_path?: string;

	    static createFrom(source: any = {}) {
	        return new DownloadResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.message = source["message"];
	        this.saved_path = source["saved_path"];
	    }
	}
	export class EncryptionStatus {
	    available: boolean;
	    password_set: boolean;
	    password_remembered: boolean;
	    hint: string;

	    static createFrom(source: any = {}) {
	        return new EncryptionStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.password_set = source["password_set"];
	        this.password_remembered = source["password_remembered"];
	        this.hint = source["hint"];
	    }
	}
	export class PendingJoinInfo {
	    invite_hash: string;
	    invite_link: string;
	    title: string;
	    requested_at: number;
	    last_checked_at: number;
	    status: string;
	    last_error: string;

	    static createFrom(source: any = {}) {
	        return new PendingJoinInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.invite_hash = source["invite_hash"];
	        this.invite_link = source["invite_link"];
	        this.title = source["title"];
	        this.requested_at = source["requested_at"];
	        this.last_checked_at = source["last_checked_at"];
	        this.status = source["status"];
	        this.last_error = source["last_error"];
	    }
	}
	export class JoinDriveResult {
	    status: string;
	    channel?: ChannelInfo;
	    pending?: PendingJoinInfo;

	    static createFrom(source: any = {}) {
	        return new JoinDriveResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.channel = this.convertValues(source["channel"], ChannelInfo);
	        this.pending = this.convertValues(source["pending"], PendingJoinInfo);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class JoinRequestInfo {
	    user_id: number;
	    display_name: string;
	    username?: string;
	    requested_at: number;
	    about?: string;

	    static createFrom(source: any = {}) {
	        return new JoinRequestInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user_id = source["user_id"];
	        this.display_name = source["display_name"];
	        this.username = source["username"];
	        this.requested_at = source["requested_at"];
	        this.about = source["about"];
	    }
	}
	export class MountDriveView {
	    id?: number;
	    title?: string;
	    kind?: string;

	    static createFrom(source: any = {}) {
	        return new MountDriveView(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	    }
	}
	export class MountView {
	    phase: string;
	    mounted: boolean;
	    mode?: string;
	    write_state?: string;
	    accepting_writes?: boolean;
	    active_writes?: number;
	    label?: string;
	    location?: string;
	    error?: string;
	    drive?: MountDriveView;
	    windows_drive?: string;

	    static createFrom(source: any = {}) {
	        return new MountView(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.mounted = source["mounted"];
	        this.mode = source["mode"];
	        this.write_state = source["write_state"];
	        this.accepting_writes = source["accepting_writes"];
	        this.active_writes = source["active_writes"];
	        this.label = source["label"];
	        this.location = source["location"];
	        this.error = source["error"];
	        this.drive = this.convertValues(source["drive"], MountDriveView);
	        this.windows_drive = source["windows_drive"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class NativeMediaRange {
	    start: number;
	    end: number;

	    static createFrom(source: any = {}) {
	        return new NativeMediaRange(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.end = source["end"];
	    }
	}
	export class NativeMediaState {
	    token?: string;
	    sequence: number;
	    status: string;
	    error?: string;
	    eof: boolean;
	    paused: boolean;
	    current_time: number;
	    duration: number;
	    buffered: NativeMediaRange[];
	    volume: number;
	    muted: boolean;
	    rate: number;
	    loading: boolean;
	    tracks: nativeplayer.Track[];

	    static createFrom(source: any = {}) {
	        return new NativeMediaState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.sequence = source["sequence"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.eof = source["eof"];
	        this.paused = source["paused"];
	        this.current_time = source["current_time"];
	        this.duration = source["duration"];
	        this.buffered = this.convertValues(source["buffered"], NativeMediaRange);
	        this.volume = source["volume"];
	        this.muted = source["muted"];
	        this.rate = source["rate"];
	        this.loading = source["loading"];
	        this.tracks = this.convertValues(source["tracks"], nativeplayer.Track);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class NativeMediaResult {
	    token: string;
	    name: string;
	    thumbnail_url: string;
	    html_controls: boolean;
	    presentation: string;
	    initial_state?: NativeMediaState;
	    info: media.LogicalFile;

	    static createFrom(source: any = {}) {
	        return new NativeMediaResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.name = source["name"];
	        this.thumbnail_url = source["thumbnail_url"];
	        this.html_controls = source["html_controls"];
	        this.presentation = source["presentation"];
	        this.initial_state = this.convertValues(source["initial_state"], NativeMediaState);
	        this.info = this.convertValues(source["info"], media.LogicalFile);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PersonalDriveCandidate {
	    id: string;
	    title: string;
	    created_at: number;
	    has_activity: boolean;
	    recommended: boolean;

	    static createFrom(source: any = {}) {
	        return new PersonalDriveCandidate(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.created_at = source["created_at"];
	        this.has_activity = source["has_activity"];
	        this.recommended = source["recommended"];
	    }
	}
	export class PersonalDriveSetupState {
	    status: string;
	    active_channel_id: string;

	    static createFrom(source: any = {}) {
	        return new PersonalDriveSetupState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.active_channel_id = source["active_channel_id"];
	    }
	}
	export class PreviewPayload {
	    data_base64: string;
	    mime_type: string;

	    static createFrom(source: any = {}) {
	        return new PreviewPayload(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.data_base64 = source["data_base64"];
	        this.mime_type = source["mime_type"];
	    }
	}
	export class SelfUser {
	    user_id: number;
	    display_name: string;
	    username?: string;
	    photo_base64?: string;

	    static createFrom(source: any = {}) {
	        return new SelfUser(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user_id = source["user_id"];
	        this.display_name = source["display_name"];
	        this.username = source["username"];
	        this.photo_base64 = source["photo_base64"];
	    }
	}
	export class TDriveFile {
	    id: number;
	    name: string;
	    size: number;
	    access_hash: number;
	    date: number;

	    static createFrom(source: any = {}) {
	        return new TDriveFile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.access_hash = source["access_hash"];
	        this.date = source["date"];
	    }
	}

}

export namespace media {

	export class Segment {
	    msg_id: number;
	    size: number;

	    static createFrom(source: any = {}) {
	        return new Segment(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.msg_id = source["msg_id"];
	        this.size = source["size"];
	    }
	}
	export class LogicalFile {
	    channel_id: number;
	    file_id: number;
	    revision: number;
	    name: string;
	    stored_size: number;
	    plaintext_size: number;
	    encrypted: boolean;
	    encryption_version: number;
	    multipart: boolean;
	    segments: Segment[];

	    static createFrom(source: any = {}) {
	        return new LogicalFile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.channel_id = source["channel_id"];
	        this.file_id = source["file_id"];
	        this.revision = source["revision"];
	        this.name = source["name"];
	        this.stored_size = source["stored_size"];
	        this.plaintext_size = source["plaintext_size"];
	        this.encrypted = source["encrypted"];
	        this.encryption_version = source["encryption_version"];
	        this.multipart = source["multipart"];
	        this.segments = this.convertValues(source["segments"], Segment);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ThroughputStats {
	    bytes_per_second: number;
	    recent_flood_wait: boolean;
	    last_flood_wait_seconds: number;

	    static createFrom(source: any = {}) {
	        return new ThroughputStats(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bytes_per_second = source["bytes_per_second"];
	        this.recent_flood_wait = source["recent_flood_wait"];
	        this.last_flood_wait_seconds = source["last_flood_wait_seconds"];
	    }
	}
	export class MediaStats {
	    playback: ThroughputStats;
	    thumbnails: ThroughputStats;

	    static createFrom(source: any = {}) {
	        return new MediaStats(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.playback = this.convertValues(source["playback"], ThroughputStats);
	        this.thumbnails = this.convertValues(source["thumbnails"], ThroughputStats);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OpenResult {
	    token: string;
	    url: string;
	    thumbnail_url: string;
	    name: string;
	    kind: string;
	    mime_type: string;
	    supports_range: boolean;
	    info: LogicalFile;

	    static createFrom(source: any = {}) {
	        return new OpenResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.url = source["url"];
	        this.thumbnail_url = source["thumbnail_url"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.mime_type = source["mime_type"];
	        this.supports_range = source["supports_range"];
	        this.info = this.convertValues(source["info"], LogicalFile);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlaybackUpdate {
	    token: string;
	    current_time: number;
	    duration: number;
	    buffer_ahead: number;

	    static createFrom(source: any = {}) {
	        return new PlaybackUpdate(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.current_time = source["current_time"];
	        this.duration = source["duration"];
	        this.buffer_ahead = source["buffer_ahead"];
	    }
	}


}

export namespace nativeplayer {

	export class Rect {
	    x: number;
	    y: number;
	    width: number;
	    height: number;

	    static createFrom(source: any = {}) {
	        return new Rect(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.x = source["x"];
	        this.y = source["y"];
	        this.width = source["width"];
	        this.height = source["height"];
	    }
	}
	export class Track {
	    id: number;
	    type: string;
	    title: string;
	    language: string;
	    codec: string;
	    selected: boolean;
	    default: boolean;
	    forced: boolean;

	    static createFrom(source: any = {}) {
	        return new Track(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.title = source["title"];
	        this.language = source["language"];
	        this.codec = source["codec"];
	        this.selected = source["selected"];
	        this.default = source["default"];
	        this.forced = source["forced"];
	    }
	}

}

export namespace updater {
	
	export class ReleaseInfo {
	    version: string;
	    tag: string;
	    page_url: string;
	    published_at: string;
	    asset_name: string;
	    asset_size: number;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.tag = source["tag"];
	        this.page_url = source["page_url"];
	        this.published_at = source["published_at"];
	        this.asset_name = source["asset_name"];
	        this.asset_size = source["asset_size"];
	    }
	}
	export class State {
	    phase: string;
	    current_version: string;
	    latest?: ReleaseInfo;
	    installable: boolean;
	    install_hint: string;
	    downloaded_bytes: number;
	    total_bytes: number;
	    checked_at: number;
	    error: string;
	    error_stage: string;
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.current_version = source["current_version"];
	        this.latest = this.convertValues(source["latest"], ReleaseInfo);
	        this.installable = source["installable"];
	        this.install_hint = source["install_hint"];
	        this.downloaded_bytes = source["downloaded_bytes"];
	        this.total_bytes = source["total_bytes"];
	        this.checked_at = source["checked_at"];
	        this.error = source["error"];
	        this.error_stage = source["error_stage"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}
