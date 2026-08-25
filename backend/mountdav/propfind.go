package mountdav

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const webDAVNamespace = "DAV:"

// serveReadOnlyPropfind preserves x/net/webdav's mature PROPFIND behavior but
// removes its unconditional exclusive-write lock advertisement and, for the
// Windows extension, the requested collection response. The pipe provides
// bounded-memory streaming and backpressure for large directories.
func serveReadOnlyPropfind(response http.ResponseWriter, request *http.Request, next http.Handler, omitRootHref string) {
	reader, writer := io.Pipe()
	filterDone := make(chan error, 1)
	go func() {
		err := filterPropfindDocument(response, reader, omitRootHref)
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

func filterPropfindDocument(destination io.Writer, source io.Reader, omitRootHref string) error {
	decoder := xml.NewDecoder(source)
	encoder := xml.NewEncoder(destination)
	stack := make([]xml.Name, 0, 8)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return encoder.Flush()
		}
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if omitRootHref != "" && isDirectDAVResponse(token, stack) {
				tokens, href, err := readDAVResponse(decoder, token)
				if err != nil {
					return err
				}
				if href == omitRootHref {
					omitRootHref = ""
					continue
				}
				if err := encodeFilteredTokens(encoder, tokens); err != nil {
					return err
				}
				continue
			}
			if isDAVElement(token.Name, "supportedlock") {
				if err := encoder.EncodeToken(token); err != nil {
					return err
				}
				end, err := discardElementContents(decoder, token.Name)
				if err != nil {
					return err
				}
				if err := encoder.EncodeToken(end); err != nil {
					return err
				}
				continue
			}
			if err := encoder.EncodeToken(token); err != nil {
				return err
			}
			stack = append(stack, token.Name)
		case xml.EndElement:
			if err := encoder.EncodeToken(token); err != nil {
				return err
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			if err := encoder.EncodeToken(token); err != nil {
				return err
			}
		}
	}
}

func propfindResponseHref(prefix, name string, isDirectory bool) string {
	href := path.Join(prefix, name)
	if href != "/" && isDirectory {
		href += "/"
	}
	return (&url.URL{Path: href}).EscapedPath()
}

func isDirectDAVResponse(start xml.StartElement, stack []xml.Name) bool {
	return isDAVElement(start.Name, "response") &&
		len(stack) > 0 && isDAVElement(stack[len(stack)-1], "multistatus")
}

func isDAVElement(name xml.Name, local string) bool {
	return name.Space == webDAVNamespace && name.Local == local
}

// readDAVResponse buffers one metadata response at a time. Directory results
// remain streaming, while the href can be checked before any part of a root
// response is emitted.
func readDAVResponse(decoder *xml.Decoder, start xml.StartElement) ([]xml.Token, string, error) {
	tokens := []xml.Token{xml.CopyToken(start)}
	depth := 1
	hrefDepth := 0
	hrefFound := false
	var href strings.Builder
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return nil, "", err
		}
		tokens = append(tokens, xml.CopyToken(token))
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if !hrefFound && depth == 2 && isDAVElement(token.Name, "href") {
				hrefFound = true
				hrefDepth = depth
			}
		case xml.CharData:
			if hrefDepth == depth {
				_, _ = href.Write(token)
			}
		case xml.EndElement:
			if hrefDepth == depth && isDAVElement(token.Name, "href") {
				hrefDepth = 0
			}
			depth--
		}
	}
	return tokens, href.String(), nil
}

func encodeFilteredTokens(encoder *xml.Encoder, tokens []xml.Token) error {
	for index := 0; index < len(tokens); index++ {
		start, isStart := tokens[index].(xml.StartElement)
		if !isStart || !isDAVElement(start.Name, "supportedlock") {
			if err := encoder.EncodeToken(tokens[index]); err != nil {
				return err
			}
			continue
		}
		if err := encoder.EncodeToken(start); err != nil {
			return err
		}
		depth := 1
		for index++; index < len(tokens); index++ {
			switch token := tokens[index].(type) {
			case xml.StartElement:
				depth++
			case xml.EndElement:
				depth--
				if depth == 0 {
					if err := encoder.EncodeToken(token); err != nil {
						return err
					}
					break
				}
			}
			if depth == 0 {
				break
			}
		}
		if depth != 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
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
