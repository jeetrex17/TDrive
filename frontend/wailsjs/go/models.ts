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
	    maxBytes: number;
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
	        this.maxBytes = source["maxBytes"];
	        this.errors = source["errors"];
	    }
	}

}

export namespace main {

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
	export class NativeMediaResult {
	    token: string;
	    name: string;
	    info: media.LogicalFile;

	    static createFrom(source: any = {}) {
	        return new NativeMediaResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.name = source["name"];
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
	export class OpenResult {
	    token: string;
	    url: string;
	    name: string;
	    info: LogicalFile;

	    static createFrom(source: any = {}) {
	        return new OpenResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.url = source["url"];
	        this.name = source["name"];
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

}

