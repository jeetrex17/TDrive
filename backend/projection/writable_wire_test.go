package projection

import (
	"errors"
	"reflect"
	"testing"
)

func TestWritableOpsWireRoundTrip(t *testing.T) {
	ops := []Op{
		{
			Type: OpFolderCommit, ProtocolVersion: 1, OpID: "mkdir-1",
			Obj: "d:docs", Parent: RootParent, Name: "Docs",
		},
		{
			Type: OpFileCommit, ProtocolVersion: 1, OpID: "commit-1",
			Parent: "d:docs", Name: "file.txt", ContentMsgID: 91,
			ContentHash: "sha256:abc", FileSize: 12, FileUploadTime: 34,
		},
		{
			Type: OpFileReplace, ProtocolVersion: 1, OpID: "replace-1",
			Obj: "f:41", ExpectedRevision: 2, ContentMsgID: 92,
			ContentHash: "sha256:def", FileSize: 56, FileUploadTime: 78, RetainedUntil: 200,
		},
		{
			Type: OpRelocate, ProtocolVersion: 1, OpID: "move-1",
			Obj: "f:41", Parent: "d:docs", Name: "renamed.txt",
			ExpectedRevision: 3, Overwrite: true, DestinationObj: "f:42",
			ExpectedDestinationRevision: 5, DeletedAt: 100, PurgeAfter: 300,
		},
		{
			Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-1",
			Obj: "d:docs", ExpectedRevision: 4, DeletedAt: 101, PurgeAfter: 301,
		},
	}
	for _, op := range ops {
		got, err := Parse(Format(op))
		if err != nil {
			t.Fatalf("round trip %s: %v", op.Type, err)
		}
		if !reflect.DeepEqual(got, op) {
			t.Fatalf("round trip %s\n got: %#v\nwant: %#v\nwire: %s", op.Type, got, op, Format(op))
		}
	}
}

func TestWritableWireRequiresVersionAndOperationID(t *testing.T) {
	for _, raw := range []string{
		"TDX1|t=fcommit|v=1|p=|n=a|cmid=1",
		"TDX1|t=fcommit|oid=op|p=|n=a|cmid=1",
		"TDX1|t=fcommit|v=2|oid=op|p=|n=a|cmid=1",
	} {
		if _, err := Parse(raw); !errors.Is(err, ErrWireMalformed) {
			t.Fatalf("Parse(%q) error=%v, want ErrWireMalformed", raw, err)
		}
	}
}
