package mountdav

import (
	"encoding/xml"
	"io"
	"net/http"
)

const webDAVNamespace = "DAV:"

// serveReadOnlyPropfind preserves x/net/webdav's mature PROPFIND behavior but
// removes its unconditional exclusive-write lock advertisement. The pipe
// provides bounded-memory streaming and backpressure for large directories.
func serveReadOnlyPropfind(response http.ResponseWriter, request *http.Request, next http.Handler) {
	reader, writer := io.Pipe()
	filterDone := make(chan error, 1)
	go func() {
		err := filterWriteLockCapability(response, reader)
		_ = reader.CloseWithError(err)
		filterDone <- err
	}()

	filtered := &filterResponseWriter{target: response, body: writer}
	defer func() {
		_ = writer.Close()
		<-filterDone
	}()
	next.ServeHTTP(filtered, request)
}

type filterResponseWriter struct {
	target      http.ResponseWriter
	body        io.Writer
	status      int
	wroteHeader bool
}

func (response *filterResponseWriter) Header() http.Header {
	return response.target.Header()
}

func (response *filterResponseWriter) WriteHeader(status int) {
	if response.wroteHeader {
		return
	}
	response.wroteHeader = true
	response.status = status
	response.target.WriteHeader(status)
}

func (response *filterResponseWriter) Write(body []byte) (int, error) {
	if !response.wroteHeader {
		response.WriteHeader(http.StatusOK)
	}
	if response.status != http.StatusMultiStatus {
		return response.target.Write(body)
	}
	return response.body.Write(body)
}

func filterWriteLockCapability(destination io.Writer, source io.Reader) error {
	decoder := xml.NewDecoder(source)
	encoder := xml.NewEncoder(destination)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return encoder.Flush()
		}
		if err != nil {
			return err
		}
		start, isStart := token.(xml.StartElement)
		if !isStart || start.Name.Space != webDAVNamespace || start.Name.Local != "supportedlock" {
			if err := encoder.EncodeToken(token); err != nil {
				return err
			}
			continue
		}
		if err := encoder.EncodeToken(start); err != nil {
			return err
		}
		end, err := discardElementContents(decoder, start.Name)
		if err != nil {
			return err
		}
		if err := encoder.EncodeToken(end); err != nil {
			return err
		}
	}
}

func discardElementContents(decoder *xml.Decoder, name xml.Name) (xml.EndElement, error) {
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return xml.EndElement{}, err
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				return xml.EndElement{Name: name}, nil
			}
		}
	}
	return xml.EndElement{Name: name}, nil
}
