package mountdav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strings"
)

const (
	davNamespace               = "DAV:"
	maxProppatchPropertyCount  = 256
	proppatchSuccessStatusLine = "HTTP/1.1 200 OK"
)

var errInvalidProppatch = errors.New("mountdav: invalid PROPPATCH document")

// serveProppatch accepts bounded WebDAV client metadata such as MiniRedir's
// Win32 timestamps. TDrive's logical namespace does not persist OS-specific
// dead properties, so successful values are advisory and content mutations
// remain exclusively owned by the write coordinator.
func (application *readApplication) serveProppatch(response http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/xml" && mediaType != "text/xml") {
		writeHTTPError(response, http.StatusUnsupportedMediaType)
		return
	}
	path, status := application.requestResourcePath(request, false)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	conditions, status := parseMutationConditions(request, path, application.resolveTaggedResource(request))
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	release, status := application.confirmMutationLocks([]string{path}, &conditions)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	defer release()

	if status = application.validateProppatchTarget(request, path, conditions); status != 0 {
		if status == http.StatusServiceUnavailable {
			response.Header().Set("Retry-After", serverBusyRetrySeconds)
		}
		writeHTTPError(response, status)
		return
	}

	properties, err := parseProppatchProperties(request.Body)
	if err != nil {
		slog.Debug("mountdav: PROPPATCH rejected, malformed document", "path", path, "error", err)
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	slog.Debug("mountdav: PROPPATCH faking success, properties are advisory only", "path", path, "property_count", len(properties))
	writeProppatchSuccess(response, request.URL.EscapedPath(), properties)
}

func (application *readApplication) validateProppatchTarget(request *http.Request, path string, conditions MutationConditions) int {
	if _, err := application.fs.Stat(request.Context(), path); err == nil {
		return 0
	} else if !os.IsNotExist(err) {
		return fileErrorStatus(err)
	}
	// confirmMutationLocks rewrites LockTokens to only tokens proven to cover
	// this request path, which is enough to accept Windows lock-null metadata.
	if len(conditions.LockTokens) > 0 {
		return 0
	}
	return http.StatusNotFound
}

func parseProppatchProperties(reader io.Reader) ([]xml.Name, error) {
	if reader == nil {
		return nil, errInvalidProppatch
	}
	decoder := xml.NewDecoder(reader)
	root, err := nextStartElement(decoder)
	if err != nil || root.Name.Space != davNamespace || root.Name.Local != "propertyupdate" {
		return nil, errInvalidProppatch
	}
	properties, err := parsePropertyOperations(decoder, root)
	if err != nil || len(properties) == 0 {
		return nil, errInvalidProppatch
	}
	if _, err = nextStartElement(decoder); !errors.Is(err, io.EOF) {
		return nil, errInvalidProppatch
	}
	return properties, nil
}

func parsePropertyOperations(decoder *xml.Decoder, root xml.StartElement) ([]xml.Name, error) {
	properties := make([]xml.Name, 0, 4)
	seen := make(map[xml.Name]struct{})
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, errInvalidProppatch
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Space != davNamespace || (token.Name.Local != "set" && token.Name.Local != "remove") {
				return nil, errInvalidProppatch
			}
			names, parseErr := parsePropertyOperation(decoder, token)
			if parseErr != nil {
				return nil, parseErr
			}
			for _, name := range names {
				if _, exists := seen[name]; exists {
					continue
				}
				seen[name] = struct{}{}
				properties = append(properties, name)
				if len(properties) > maxProppatchPropertyCount {
					return nil, errInvalidProppatch
				}
			}
		case xml.EndElement:
			if token.Name == root.Name {
				return properties, nil
			}
			return nil, errInvalidProppatch
		case xml.CharData:
			if len(bytes.TrimSpace(token)) != 0 {
				return nil, errInvalidProppatch
			}
		}
	}
}

func parsePropertyOperation(decoder *xml.Decoder, operation xml.StartElement) ([]xml.Name, error) {
	var properties []xml.Name
	seenContainer := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, errInvalidProppatch
		}
		switch token := token.(type) {
		case xml.StartElement:
			if seenContainer || token.Name.Space != davNamespace || token.Name.Local != "prop" {
				return nil, errInvalidProppatch
			}
			seenContainer = true
			properties, err = parsePropertyContainer(decoder, token)
			if err != nil {
				return nil, err
			}
		case xml.EndElement:
			if token.Name != operation.Name || !seenContainer || len(properties) == 0 {
				return nil, errInvalidProppatch
			}
			return properties, nil
		case xml.CharData:
			if len(bytes.TrimSpace(token)) != 0 {
				return nil, errInvalidProppatch
			}
		}
	}
}

func parsePropertyContainer(decoder *xml.Decoder, container xml.StartElement) ([]xml.Name, error) {
	properties := make([]xml.Name, 0, 4)
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, errInvalidProppatch
		}
		switch token := token.(type) {
		case xml.StartElement:
			properties = append(properties, token.Name)
			if len(properties) > maxProppatchPropertyCount || decoder.Skip() != nil {
				return nil, errInvalidProppatch
			}
		case xml.EndElement:
			if token.Name != container.Name {
				return nil, errInvalidProppatch
			}
			return properties, nil
		case xml.CharData:
			if len(bytes.TrimSpace(token)) != 0 {
				return nil, errInvalidProppatch
			}
		}
	}
}

func nextStartElement(decoder *xml.Decoder) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			return token, nil
		case xml.CharData:
			if strings.TrimSpace(string(token)) != "" {
				return xml.StartElement{}, errInvalidProppatch
			}
		}
	}
}

type proppatchMultiStatus struct {
	XMLName  xml.Name          `xml:"DAV: multistatus"`
	Response proppatchResponse `xml:"response"`
}

type proppatchResponse struct {
	Href     string            `xml:"href"`
	Propstat proppatchPropstat `xml:"propstat"`
}

type proppatchPropstat struct {
	Prop   proppatchProp `xml:"prop"`
	Status string        `xml:"status"`
}

type proppatchProp struct {
	Properties []proppatchProperty `xml:",any"`
}

type proppatchProperty struct {
	XMLName xml.Name
}

func writeProppatchSuccess(response http.ResponseWriter, href string, properties []xml.Name) {
	values := make([]proppatchProperty, len(properties))
	for index, name := range properties {
		values[index] = proppatchProperty{XMLName: name}
	}
	document := proppatchMultiStatus{Response: proppatchResponse{
		Href: href,
		Propstat: proppatchPropstat{
			Prop:   proppatchProp{Properties: values},
			Status: proppatchSuccessStatusLine,
		},
	}}
	response.Header().Set("Content-Type", "application/xml; charset=utf-8")
	response.WriteHeader(http.StatusMultiStatus)
	_, _ = response.Write([]byte(xml.Header))
	_ = xml.NewEncoder(response).Encode(document)
}
