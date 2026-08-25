package mountdav

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseProppatchPropertiesRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty"},
		{name: "wrong root namespace", body: `<propertyupdate><set><prop><x/></prop></set></propertyupdate>`},
		{name: "unknown operation", body: `<D:propertyupdate xmlns:D="DAV:"><D:replace><D:prop><x/></D:prop></D:replace></D:propertyupdate>`},
		{name: "missing prop", body: `<D:propertyupdate xmlns:D="DAV:"><D:set/></D:propertyupdate>`},
		{name: "empty prop", body: `<D:propertyupdate xmlns:D="DAV:"><D:set><D:prop/></D:set></D:propertyupdate>`},
		{name: "unexpected text", body: `<D:propertyupdate xmlns:D="DAV:">text<D:set><D:prop><x/></D:prop></D:set></D:propertyupdate>`},
		{name: "second document", body: `<D:propertyupdate xmlns:D="DAV:"><D:set><D:prop><x/></D:prop></D:set></D:propertyupdate><x/>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseProppatchProperties(strings.NewReader(test.body)); err == nil {
				t.Fatalf("parseProppatchProperties(%q) succeeded", test.body)
			}
		})
	}
}

func TestParseProppatchPropertiesDeduplicatesAndBoundsNames(t *testing.T) {
	body := `<D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:">
<D:set><D:prop><Z:Win32CreationTime><nested/></Z:Win32CreationTime></D:prop></D:set>
<D:remove><D:prop><Z:Win32CreationTime/></D:prop></D:remove>
</D:propertyupdate>`
	properties, err := parseProppatchProperties(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseProppatchProperties: %v", err)
	}
	want := xml.Name{Space: "urn:schemas-microsoft-com:", Local: "Win32CreationTime"}
	if len(properties) != 1 || properties[0] != want {
		t.Fatalf("properties = %+v, want %+v", properties, want)
	}

	var builder strings.Builder
	builder.WriteString(`<D:propertyupdate xmlns:D="DAV:"><D:set><D:prop>`)
	for index := 0; index <= maxProppatchPropertyCount; index++ {
		builder.WriteString(`<p`)
		builder.WriteString(string(rune('a' + index%26)))
		builder.WriteString(` xmlns="urn:test:`)
		builder.WriteString(strings.Repeat("x", index/26+1))
		builder.WriteString(`"/>`)
	}
	builder.WriteString(`</D:prop></D:set></D:propertyupdate>`)
	if _, err := parseProppatchProperties(strings.NewReader(builder.String())); err == nil {
		t.Fatal("oversized property set was accepted")
	}
}

func TestWritableProppatchRejectsInvalidBoundaryInputs(t *testing.T) {
	valid := `<D:propertyupdate xmlns:D="DAV:"><D:set><D:prop><D:displayname>ignored</D:displayname></D:prop></D:set></D:propertyupdate>`
	tests := []struct {
		name        string
		target      string
		contentType string
		body        string
		status      int
	}{
		{name: "missing content type", target: "/Docs/note.txt", body: valid, status: http.StatusUnsupportedMediaType},
		{name: "wrong content type", target: "/Docs/note.txt", contentType: "application/json", body: valid, status: http.StatusUnsupportedMediaType},
		{name: "missing resource", target: "/Docs/missing.txt", contentType: "application/xml", body: valid, status: http.StatusNotFound},
		{name: "malformed XML", target: "/Docs/note.txt", contentType: "application/xml", body: `<D:propertyupdate`, status: http.StatusBadRequest},
		{name: "invalid document", target: "/Docs/note.txt", contentType: "application/xml", body: `<D:propertyupdate xmlns:D="DAV:"/>`, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingWriteCoordinator{}
			handler := newWritableTestHandler(t, writer)
			request := trustedRequest("PROPPATCH", testCapability+test.target, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d, body=%q", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
}
