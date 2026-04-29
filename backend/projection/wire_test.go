package projection

import (
	"errors"
	"strings"
	"testing"
)

func TestParseFileUpload(t *testing.T) {
	op, err := Parse("TDX1|t=f|p=d:abc|n=sunset.jpg|sz=4321|ts=99\nTDrive: sunset.jpg")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Type != OpFileUpload {
		t.Fatalf("type = %q", op.Type)
	}
	if op.Parent != "d:abc" {
		t.Fatalf("parent = %q", op.Parent)
	}
	if op.Name != "sunset.jpg" {
		t.Fatalf("name = %q", op.Name)
	}
	if op.FileSize != 4321 {
		t.Fatalf("size = %d", op.FileSize)
	}
	if op.FileUploadTime != 99 {
		t.Fatalf("upload_time = %d", op.FileUploadTime)
	}
}

func TestParseFileUploadRoot(t *testing.T) {
	op, err := Parse("TDX1|t=f|p=|n=hello.txt")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Parent != RootParent {
		t.Fatalf("expected root, got %q", op.Parent)
	}
}

func TestParseMkdir(t *testing.T) {
	op, err := Parse("TDX1|t=mkdir|obj=d:goa-uuid|p=|n=Goa%20Trip")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Type != OpMkdir {
		t.Fatalf("type = %q", op.Type)
	}
	if op.Obj != "d:goa-uuid" {
		t.Fatalf("obj = %q", op.Obj)
	}
	if op.Parent != RootParent {
		t.Fatalf("parent = %q", op.Parent)
	}
	if op.Name != "Goa Trip" {
		t.Fatalf("name decode = %q", op.Name)
	}
}

func TestParseRenameFile(t *testing.T) {
	op, err := Parse("TDX1|t=rename|obj=f:42|n=new.png")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Type != OpRename || op.Obj != "f:42" || op.Name != "new.png" {
		t.Fatalf("op = %+v", op)
	}
}

func TestParseRenameFolder(t *testing.T) {
	op, err := Parse("TDX1|t=rename|obj=d:abc|n=Goa")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Obj != "d:abc" || op.Name != "Goa" {
		t.Fatalf("op = %+v", op)
	}
}

func TestParseMove(t *testing.T) {
	op, err := Parse("TDX1|t=move|obj=f:7|p=d:abc")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Type != OpMove || op.Obj != "f:7" || op.Parent != "d:abc" {
		t.Fatalf("op = %+v", op)
	}
}

func TestParseRmdir(t *testing.T) {
	op, err := Parse("TDX1|t=rmdir|obj=d:gone")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Type != OpRmdir || op.Obj != "d:gone" {
		t.Fatalf("op = %+v", op)
	}
}

func TestParseTomb(t *testing.T) {
	op, err := Parse("TDX1|t=tomb|obj=f:99")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if op.Type != OpTomb || op.Obj != "f:99" {
		t.Fatalf("op = %+v", op)
	}
}

func TestParseUnknownVersion(t *testing.T) {
	_, err := Parse("TDX2|t=mkdir|obj=d:x|p=|n=foo")
	if !errors.Is(err, ErrWireBadVersion) {
		t.Fatalf("err = %v, want ErrWireBadVersion", err)
	}
}

func TestParseUnknownOpType(t *testing.T) {
	_, err := Parse("TDX1|t=quux|obj=d:x|p=|n=foo")
	if !errors.Is(err, ErrWireBadOpType) {
		t.Fatalf("err = %v, want ErrWireBadOpType", err)
	}
}

func TestParseRequiresFolderPrefixOnMkdirObj(t *testing.T) {
	_, err := Parse("TDX1|t=mkdir|obj=raw|p=|n=Goa")
	if !errors.Is(err, ErrWireBadObject) {
		t.Fatalf("err = %v, want ErrWireBadObject", err)
	}
}

func TestParseRequiresFilePrefixOnTomb(t *testing.T) {
	_, err := Parse("TDX1|t=tomb|obj=d:x")
	if !errors.Is(err, ErrWireBadObject) {
		t.Fatalf("err = %v, want ErrWireBadObject", err)
	}
}

func TestParseRejectsNonFolderParent(t *testing.T) {
	_, err := Parse("TDX1|t=f|p=raw|n=x")
	if !errors.Is(err, ErrWireBadParent) {
		t.Fatalf("err = %v, want ErrWireBadParent", err)
	}
}

func TestParseEmptyHeader(t *testing.T) {
	_, err := Parse("")
	if !errors.Is(err, ErrWireMissingHeader) {
		t.Fatalf("err = %v, want ErrWireMissingHeader", err)
	}
}

func TestRoundTripFormatParse(t *testing.T) {
	cases := []Op{
		{Type: OpFileUpload, Parent: RootParent, Name: "alpha.png", FileSize: 123, FileUploadTime: 456},
		{Type: OpFileUpload, Parent: "d:abc", Name: "spaces here.txt", FileSize: 789, FileUploadTime: 1000},
		{Type: OpMeta, Obj: "f:9", Parent: RootParent, Name: "legacy.bin", FileSize: 2048, FileUploadTime: 88},
		{Type: OpMkdir, Obj: "d:abc", Parent: RootParent, Name: "Photos"},
		{Type: OpRename, Obj: "f:5", Name: "new.png"},
		{Type: OpRename, Obj: "d:abc", Name: "Renamed"},
		{Type: OpMove, Obj: "f:5", Parent: "d:abc"},
		{Type: OpMove, Obj: "d:abc", Parent: RootParent},
		{Type: OpRmdir, Obj: "d:abc"},
		{Type: OpTomb, Obj: "f:42"},
	}

	for _, want := range cases {
		raw := Format(want)
		if !strings.HasPrefix(raw, "TDX1|t=") {
			t.Fatalf("Format(%v) = %q, missing prefix", want, raw)
		}
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if got.Type != want.Type ||
			got.Obj != want.Obj ||
			got.Parent != want.Parent ||
			got.Name != want.Name ||
			got.FileSize != want.FileSize ||
			got.FileUploadTime != want.FileUploadTime {
			t.Fatalf("round trip mismatch: want %+v, got %+v (raw=%q)", want, got, raw)
		}
	}
}

func TestFormatFileUploadIncludesSizeAndTime(t *testing.T) {
	raw := Format(Op{
		Type:           OpFileUpload,
		Parent:         RootParent,
		Name:           "x.txt",
		FileSize:       55,
		FileUploadTime: 66,
	})
	if !strings.Contains(raw, "|sz=55") {
		t.Fatalf("formatted header missing size: %q", raw)
	}
	if !strings.Contains(raw, "|ts=66") {
		t.Fatalf("formatted header missing upload time: %q", raw)
	}
}

func TestExtractHeaderLineStripsBody(t *testing.T) {
	got := ExtractHeaderLine("TDX1|t=mkdir|obj=d:x|p=|n=foo\nhuman comment")
	want := "TDX1|t=mkdir|obj=d:x|p=|n=foo"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
