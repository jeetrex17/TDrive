package projection

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const wireVersionPrefix = "TDX1"

var (
	ErrWireMissingHeader = errors.New("wire: header missing")
	ErrWireBadVersion    = errors.New("wire: unknown version")
	ErrWireBadOpType     = errors.New("wire: unknown op type")
	ErrWireMalformed     = errors.New("wire: malformed header")
	ErrWireBadObject     = errors.New("wire: bad object id")
	ErrWireBadParent     = errors.New("wire: bad parent id")
)

func ExtractHeaderLine(raw string) string {
	head, _, _ := strings.Cut(raw, "\n")
	return head
}

func Parse(raw string) (Op, error) {
	header := strings.TrimSpace(ExtractHeaderLine(raw))
	if header == "" {
		return Op{}, ErrWireMissingHeader
	}

	parts := strings.Split(header, "|")
	if len(parts) < 2 {
		return Op{}, ErrWireMalformed
	}
	if parts[0] != wireVersionPrefix {
		return Op{}, ErrWireBadVersion
	}

	kv := make(map[string]string, len(parts)-1)
	for _, p := range parts[1:] {
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			return Op{}, ErrWireMalformed
		}
		kv[p[:eq]] = p[eq+1:]
	}

	t, ok := kv["t"]
	if !ok {
		return Op{}, ErrWireMalformed
	}

	op := Op{Type: OpType(t)}

	switch op.Type {
	case OpFileUpload:
		if err := setParent(&op, kv["p"]); err != nil {
			return Op{}, err
		}
		if err := setName(&op, kv["n"]); err != nil {
			return Op{}, err
		}
	case OpMeta:
		if err := setObj(&op, kv["obj"], FileIDPrefix); err != nil {
			return Op{}, err
		}
		if err := setParent(&op, kv["p"]); err != nil {
			return Op{}, err
		}
		if err := setName(&op, kv["n"]); err != nil {
			return Op{}, err
		}
	case OpMkdir:
		if err := setObj(&op, kv["obj"], FolderIDPrefix); err != nil {
			return Op{}, err
		}
		if err := setParent(&op, kv["p"]); err != nil {
			return Op{}, err
		}
		if err := setName(&op, kv["n"]); err != nil {
			return Op{}, err
		}
	case OpRename:
		if err := setObjAny(&op, kv["obj"]); err != nil {
			return Op{}, err
		}
		if err := setName(&op, kv["n"]); err != nil {
			return Op{}, err
		}
	case OpMove:
		if err := setObjAny(&op, kv["obj"]); err != nil {
			return Op{}, err
		}
		if err := setParent(&op, kv["p"]); err != nil {
			return Op{}, err
		}
	case OpRmdir:
		if err := setObj(&op, kv["obj"], FolderIDPrefix); err != nil {
			return Op{}, err
		}
	case OpTomb:
		if err := setObj(&op, kv["obj"], FileIDPrefix); err != nil {
			return Op{}, err
		}
	default:
		return Op{}, ErrWireBadOpType
	}

	if s, ok := kv["sz"]; ok {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Op{}, ErrWireMalformed
		}
		op.FileSize = n
	}
	if s, ok := kv["ts"]; ok {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Op{}, ErrWireMalformed
		}
		op.FileUploadTime = n
	}

	return op, nil
}

func Format(op Op) string {
	var b strings.Builder
	b.WriteString(wireVersionPrefix)
	b.WriteString("|t=")
	b.WriteString(string(op.Type))

	switch op.Type {
	case OpFileUpload:
		b.WriteString("|p=")
		b.WriteString(op.Parent)
		b.WriteString("|n=")
		b.WriteString(url.QueryEscape(op.Name))
		appendFileAttrs(&b, op)
	case OpMeta:
		b.WriteString("|obj=")
		b.WriteString(op.Obj)
		b.WriteString("|p=")
		b.WriteString(op.Parent)
		b.WriteString("|n=")
		b.WriteString(url.QueryEscape(op.Name))
		appendFileAttrs(&b, op)
	case OpMkdir:
		b.WriteString("|obj=")
		b.WriteString(op.Obj)
		b.WriteString("|p=")
		b.WriteString(op.Parent)
		b.WriteString("|n=")
		b.WriteString(url.QueryEscape(op.Name))
	case OpRename:
		b.WriteString("|obj=")
		b.WriteString(op.Obj)
		b.WriteString("|n=")
		b.WriteString(url.QueryEscape(op.Name))
	case OpMove:
		b.WriteString("|obj=")
		b.WriteString(op.Obj)
		b.WriteString("|p=")
		b.WriteString(op.Parent)
	case OpRmdir, OpTomb:
		b.WriteString("|obj=")
		b.WriteString(op.Obj)
	}

	return b.String()
}

func appendFileAttrs(b *strings.Builder, op Op) {
	if op.FileSize > 0 {
		b.WriteString("|sz=")
		b.WriteString(strconv.FormatInt(op.FileSize, 10))
	}
	if op.FileUploadTime > 0 {
		b.WriteString("|ts=")
		b.WriteString(strconv.FormatInt(op.FileUploadTime, 10))
	}
}

func setObj(op *Op, raw, requiredPrefix string) error {
	if raw == "" || !strings.HasPrefix(raw, requiredPrefix) {
		return ErrWireBadObject
	}
	op.Obj = raw
	return nil
}

func setObjAny(op *Op, raw string) error {
	if raw == "" {
		return ErrWireBadObject
	}
	if !strings.HasPrefix(raw, FolderIDPrefix) && !strings.HasPrefix(raw, FileIDPrefix) {
		return ErrWireBadObject
	}
	op.Obj = raw
	return nil
}

func setParent(op *Op, raw string) error {
	if raw == "" {
		op.Parent = RootParent
		return nil
	}
	if !strings.HasPrefix(raw, FolderIDPrefix) {
		return ErrWireBadParent
	}
	op.Parent = raw
	return nil
}

func setName(op *Op, raw string) error {
	if raw == "" {
		return ErrWireMalformed
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return ErrWireMalformed
	}
	op.Name = decoded
	return nil
}
