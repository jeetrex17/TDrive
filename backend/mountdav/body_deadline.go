package mountdav

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

var (
	errBodyReadTimeout         = errors.New("mountdav: request body read timed out")
	errBodyDeadlineUnavailable = errors.New("mountdav: request body deadline unavailable")
)

type idleDeadlineBody struct {
	body        io.ReadCloser
	setDeadline func(time.Time) error
	idleTimeout time.Duration
	now         func() time.Time
}

func (body *idleDeadlineBody) Read(buffer []byte) (int, error) {
	if err := body.setDeadline(body.now().Add(body.idleTimeout)); err != nil {
		return 0, bodyDeadlineError("set PUT idle deadline", err)
	}
	n, readErr := body.body.Read(buffer)
	clearErr := body.setDeadline(time.Time{})
	if isBodyReadTimeout(readErr) {
		readErr = fmt.Errorf("%w: %v", errBodyReadTimeout, readErr)
	}
	if readErr != nil {
		return n, readErr
	}
	if clearErr != nil {
		return n, bodyDeadlineError("clear PUT idle deadline", clearErr)
	}
	return n, nil
}

func (body *idleDeadlineBody) Close() error {
	return body.body.Close()
}

func wrapPUTBodyWithIdleDeadline(
	response http.ResponseWriter,
	request *http.Request,
	timeout time.Duration,
	now func() time.Time,
) *http.Request {
	if request.Body == nil || request.Body == http.NoBody {
		return request
	}
	wrapped := request.Clone(request.Context())
	wrapped.Body = &idleDeadlineBody{
		body:        request.Body,
		setDeadline: http.NewResponseController(response).SetReadDeadline,
		idleTimeout: timeout,
		now:         now,
	}
	return wrapped
}

func bufferBodyWithAbsoluteDeadline(
	response http.ResponseWriter,
	request *http.Request,
	timeout time.Duration,
	now func() time.Time,
) ([]byte, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return bufferBoundedBody(response, request)
	}
	setDeadline := http.NewResponseController(response).SetReadDeadline
	if err := setDeadline(now().Add(timeout)); err != nil {
		return nil, bodyDeadlineError("set control-body deadline", err)
	}
	body, readErr := bufferBoundedBody(response, request)
	clearErr := setDeadline(time.Time{})
	if isBodyReadTimeout(readErr) {
		readErr = fmt.Errorf("%w: %v", errBodyReadTimeout, readErr)
	}
	if readErr != nil {
		return nil, readErr
	}
	if clearErr != nil {
		return nil, bodyDeadlineError("clear control-body deadline", clearErr)
	}
	return body, nil
}

func bodyDeadlineError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", errBodyDeadlineUnavailable, operation, err)
}

func isBodyReadTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errBodyReadTimeout) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
