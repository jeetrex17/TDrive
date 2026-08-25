package mountdav

import (
	"net/http"
	"reflect"
	"testing"
)

func TestParseETagConditions(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []string
		want   ETagConditions
		ok     bool
	}{
		{name: "absent", ok: true},
		{name: "wildcard", values: []string{" * "}, want: ETagConditions{Present: true, Any: true}, ok: true},
		{
			name:   "strong and weak list",
			values: []string{`"one,with-comma"`, ` W/"two"`},
			want: ETagConditions{Present: true, Tags: []EntityTag{
				{Opaque: "one,with-comma"},
				{Weak: true, Opaque: "two"},
			}},
			ok: true,
		},
		{name: "empty", values: []string{""}},
		{name: "bare", values: []string{"abc"}},
		{name: "unterminated", values: []string{`"abc`}},
		{name: "trailing comma", values: []string{`"abc",`}},
		{name: "missing comma", values: []string{`"abc" "def"`}},
		{name: "control", values: []string{"\"a\tb\""}},
		{name: "wildcard mixed", values: []string{`*, "abc"`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseETagConditions(test.values)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseETagConditions(%q) = (%+v, %t), want (%+v, %t)", test.values, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestParseDAVIfTaggedAndNegatedConditions(t *testing.T) {
	value := `<http://127.0.0.1:7331` + testCapability + `/Docs/note.txt> (` +
		`<opaquelocktoken:abc> ["r1"]) (Not <opaquelocktoken:def>) ` +
		`<http://127.0.0.1:7331` + testCapability + `/Docs/other.txt> ([W/"r2"])`
	lists, ok := parseDAVIf(value)
	if !ok || len(lists) != 3 {
		t.Fatalf("parseDAVIf = (%+v, %t)", lists, ok)
	}
	if lists[0].resourceTag == "" || len(lists[0].conditions) != 2 || lists[0].conditions[0].LockToken != "opaquelocktoken:abc" {
		t.Fatalf("first list = %+v", lists[0])
	}
	if lists[0].conditions[1].ETag == nil || lists[0].conditions[1].ETag.Opaque != "r1" {
		t.Fatalf("first list ETag = %+v", lists[0].conditions[1])
	}
	if !lists[1].conditions[0].Not || lists[1].conditions[0].LockToken != "opaquelocktoken:def" {
		t.Fatalf("negated condition = %+v", lists[1])
	}
	if lists[2].conditions[0].ETag == nil || !lists[2].conditions[0].ETag.Weak {
		t.Fatalf("tagged weak ETag = %+v", lists[2])
	}
}

func TestParseDAVIfRejectsMalformedGrammar(t *testing.T) {
	for _, input := range []string{
		"",
		"token",
		"()",
		"(<relative-token>)",
		"(<opaquelocktoken:x>",
		"(<opaquelocktoken:x>) trailing",
		"<http://example.test/x>",
		"<http://example.test/x> ()",
		"([not-an-etag])",
		"(not <opaquelocktoken:x>)",
	} {
		if lists, ok := parseDAVIf(input); ok {
			t.Errorf("parseDAVIf(%q) = %+v, want rejection", input, lists)
		}
	}
}

func TestParseMutationConditionsResolvesTaggedResources(t *testing.T) {
	request := trustedRequest(http.MethodPut, testCapability+"/Docs/note.txt", nil)
	request.Header.Set("If-Match", `"r1"`)
	request.Header.Set("If", `<http://127.0.0.1:7331`+testCapability+`/Docs/note.txt> (<opaquelocktoken:abc>)`)
	application := &readApplication{capabilityPath: testCapability, authority: "127.0.0.1:7331"}

	conditions, status := parseMutationConditions(
		request,
		"/Docs/note.txt",
		application.resolveTaggedResource(request),
	)
	if status != 0 || len(conditions.DAVIf) != 1 {
		t.Fatalf("conditions/status = %+v/%d", conditions, status)
	}
	if conditions.DAVIf[0].ResourcePath != "/Docs/note.txt" || len(conditions.LockTokens) != 1 {
		t.Fatalf("tagged conditions = %+v", conditions)
	}

	request.Header.Set("If", `<http://localhost:7331`+testCapability+`/Docs/note.txt> (<opaquelocktoken:abc>)`)
	if _, status := parseMutationConditions(request, "/Docs/note.txt", application.resolveTaggedResource(request)); status != http.StatusBadRequest {
		t.Fatalf("foreign tagged resource status = %d, want 400", status)
	}
}

func TestEntityTagSerializationAndValidation(t *testing.T) {
	if got := serializeEntityTag(EntityTag{Weak: true, Opaque: "revision"}); got != `W/"revision"` {
		t.Fatalf("serialized weak ETag = %q", got)
	}
	for _, value := range []string{`"ok"`, `"comma,ok"`, `"back\\slash"`} {
		if !validStrongETag(value) {
			t.Errorf("validStrongETag(%q) = false", value)
		}
	}
	for _, value := range []string{`W/"weak"`, "", `"has space"`, `"unterminated`} {
		if validStrongETag(value) {
			t.Errorf("validStrongETag(%q) = true", value)
		}
	}
}
