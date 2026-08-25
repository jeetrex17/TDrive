package mountdav

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"
)

const (
	defaultMaxActiveLocks = 1024
	defaultLockDuration   = 10 * time.Minute
	maximumLockDuration   = time.Hour
	temporaryLockDuration = 5 * time.Minute
	maxLockOwnerBytes     = 4 << 10
	statusLocked          = 423
)

var errLockCapacity = errors.New("mountdav: active lock limit reached")

type trackedLock struct {
	internalToken string
	details       webdav.LockDetails
	expires       time.Time
}

// boundedLockSystem gives x/net/webdav's mature hierarchy semantics a bounded
// public-token layer. All externally visible tokens are unguessable absolute
// opaquelocktoken URIs; the delegate's implementation token never escapes.
type boundedLockSystem struct {
	mu       sync.Mutex
	delegate webdav.LockSystem
	maximum  int
	byToken  map[string]trackedLock
}

func newBoundedLockSystem(maximum int) *boundedLockSystem {
	if maximum <= 0 {
		maximum = defaultMaxActiveLocks
	}
	return &boundedLockSystem{
		delegate: webdav.NewMemLS(),
		maximum:  maximum,
		byToken:  make(map[string]trackedLock),
	}
}

func (locks *boundedLockSystem) Confirm(now time.Time, name0, name1 string, conditions ...webdav.Condition) (func(), error) {
	translated := make([]webdav.Condition, len(conditions))
	locks.mu.Lock()
	locks.collectExpired(now)
	for index, condition := range conditions {
		translated[index] = condition
		if condition.Token == "" {
			continue
		}
		tracked, exists := locks.byToken[condition.Token]
		if !exists {
			translated[index] = lockCondition(unknownExternalLockToken())
			continue
		}
		translated[index] = lockCondition(tracked.internalToken)
	}
	locks.mu.Unlock()
	return locks.delegate.Confirm(now, name0, name1, translated...)
}

func (locks *boundedLockSystem) Create(now time.Time, details webdav.LockDetails) (string, error) {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	locks.collectExpired(now)
	if len(locks.byToken) >= locks.maximum {
		return "", errLockCapacity
	}
	internalToken, err := locks.delegate.Create(now, details)
	if err != nil {
		return "", err
	}
	publicToken, err := randomLockToken()
	if err != nil {
		_ = locks.delegate.Unlock(now, internalToken)
		return "", err
	}
	locks.byToken[publicToken] = trackedLock{
		internalToken: internalToken,
		details:       details,
		expires:       lockExpiry(now, details.Duration),
	}
	return publicToken, nil
}

func (locks *boundedLockSystem) Refresh(now time.Time, token string, duration time.Duration) (webdav.LockDetails, error) {
	return locks.refreshPath(now, "", token, duration)
}

func (locks *boundedLockSystem) refreshPath(now time.Time, path, token string, duration time.Duration) (webdav.LockDetails, error) {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	locks.collectExpired(now)
	tracked, exists := locks.byToken[token]
	if !exists {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}
	if path != "" && tracked.details.Root != path {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}
	details, err := locks.delegate.Refresh(now, tracked.internalToken, duration)
	if err != nil {
		return webdav.LockDetails{}, err
	}
	tracked.details = details
	tracked.expires = lockExpiry(now, duration)
	locks.byToken[token] = tracked
	return details, nil
}

func (locks *boundedLockSystem) Unlock(now time.Time, token string) error {
	return locks.unlockPath(now, "", token)
}

func (locks *boundedLockSystem) unlockPath(now time.Time, path, token string) error {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	locks.collectExpired(now)
	tracked, exists := locks.byToken[token]
	if !exists {
		return webdav.ErrNoSuchLock
	}
	if path != "" && tracked.details.Root != path {
		return webdav.ErrForbidden
	}
	if err := locks.delegate.Unlock(now, tracked.internalToken); err != nil {
		return err
	}
	delete(locks.byToken, token)
	return nil
}

func (locks *boundedLockSystem) tokenApplies(now time.Time, token, path string) bool {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	locks.collectExpired(now)
	tracked, exists := locks.byToken[token]
	if !exists {
		return false
	}
	root := tracked.details.Root
	return path == root ||
		(!tracked.details.ZeroDepth && (root == "/" || strings.HasPrefix(path, root+"/")))
}

func (locks *boundedLockSystem) collectExpired(now time.Time) {
	for token, tracked := range locks.byToken {
		if !tracked.expires.IsZero() && !now.Before(tracked.expires) {
			delete(locks.byToken, token)
		}
	}
}

func lockExpiry(now time.Time, duration time.Duration) time.Time {
	if duration < 0 {
		return time.Time{}
	}
	return now.Add(duration)
}

func randomLockToken() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("mountdav: generate lock token: %w", err)
	}
	return "opaquelocktoken:" + hex.EncodeToString(entropy[:]), nil
}

func lockCondition(value string) webdav.Condition {
	return webdav.Condition{Token: value}
}

func unknownExternalLockToken() string {
	return strings.Join([]string{"mountdav", "unknown", "lock", "token"}, "-")
}

type lockInfoDocument struct {
	XMLName   xml.Name      `xml:"lockinfo"`
	LockScope lockScopeXML  `xml:"lockscope"`
	LockType  lockTypeXML   `xml:"locktype"`
	Owner     *lockOwnerXML `xml:"owner"`
}

type lockScopeXML struct {
	XMLName   xml.Name      `xml:"lockscope"`
	Exclusive *namedElement `xml:"exclusive"`
	Shared    *namedElement `xml:"shared"`
}

type lockTypeXML struct {
	XMLName xml.Name      `xml:"locktype"`
	Write   *namedElement `xml:"write"`
}

type lockOwnerXML struct {
	XMLName xml.Name
	Inner   string `xml:",innerxml"`
}

type namedElement struct {
	XMLName xml.Name
}

type lockPropertyXML struct {
	XMLName       xml.Name         `xml:"DAV: prop"`
	LockDiscovery lockDiscoveryXML `xml:"lockdiscovery"`
}

type lockDiscoveryXML struct {
	ActiveLock activeLockXML `xml:"activelock"`
}

type activeLockXML struct {
	LockType  activeLockTypeXML  `xml:"locktype"`
	LockScope activeLockScopeXML `xml:"lockscope"`
	Depth     string             `xml:"depth"`
	Owner     *lockOwnerXML      `xml:"owner,omitempty"`
	Timeout   string             `xml:"timeout"`
	LockToken hrefXML            `xml:"locktoken"`
	LockRoot  hrefXML            `xml:"lockroot"`
}

type activeLockTypeXML struct {
	Write struct{} `xml:"write"`
}

type activeLockScopeXML struct {
	Exclusive struct{} `xml:"exclusive"`
}

type hrefXML struct {
	Href string `xml:"href"`
}

func (application *readApplication) serveLock(response http.ResponseWriter, request *http.Request) {
	path, status := application.requestResourcePath(request, true)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	duration, ok := parseLockTimeout(request.Header.Get("Timeout"))
	if !ok {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	body, err := readBoundedApplicationBody(request.Body)
	if err != nil {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}

	now := time.Now()
	if len(bytes.TrimSpace(body)) == 0 {
		application.refreshLock(response, request, path, duration, now)
		return
	}
	document, status := parseLockInfo(body)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	depth, ok := parseLockDepth(request.Header.Get("Depth"))
	if !ok {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	details := webdav.LockDetails{
		Root:      path,
		Duration:  duration,
		ZeroDepth: depth == "0",
	}
	if document.Owner != nil {
		details.OwnerXML = document.Owner.Inner
	}
	token, err := application.lockSystem.Create(now, details)
	if err != nil {
		slog.Debug("mountdav: LOCK create rejected", "path", path, "zero_depth", details.ZeroDepth, "error", err)
		serveLockError(response, err)
		return
	}
	slog.Debug("mountdav: LOCK created", "path", path, "zero_depth", details.ZeroDepth, "duration", duration)
	response.Header().Set("Lock-Token", "<"+token+">")
	application.writeLockResponse(response, request, token, details, document.Owner)
}

func (application *readApplication) refreshLock(
	response http.ResponseWriter,
	request *http.Request,
	path string,
	duration time.Duration,
	now time.Time,
) {
	conditions, status := parseMutationConditions(request, path, application.resolveTaggedResource(request))
	if status != 0 || len(conditions.DAVIf) != 1 ||
		conditions.DAVIf[0].ResourcePath != path ||
		len(conditions.DAVIf[0].Conditions) != 1 {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	condition := conditions.DAVIf[0].Conditions[0]
	if condition.Not || condition.LockToken == "" || condition.ETag != nil {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	var details webdav.LockDetails
	var err error
	if locks, ok := application.lockSystem.(*boundedLockSystem); ok {
		details, err = locks.refreshPath(now, path, condition.LockToken, duration)
	} else {
		details, err = application.lockSystem.Refresh(now, condition.LockToken, duration)
	}
	if err != nil {
		slog.Debug("mountdav: LOCK refresh rejected", "path", path, "error", err)
		serveLockError(response, err)
		return
	}
	slog.Debug("mountdav: LOCK refreshed", "path", path, "duration", duration)
	application.writeLockResponse(response, request, condition.LockToken, details, nil)
}

func (application *readApplication) serveUnlock(response http.ResponseWriter, request *http.Request) {
	path, status := application.requestResourcePath(request, true)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	tokenHeader := request.Header.Get("Lock-Token")
	if len(tokenHeader) < 3 || tokenHeader[0] != '<' || tokenHeader[len(tokenHeader)-1] != '>' {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	token := tokenHeader[1 : len(tokenHeader)-1]
	if !validStateURI(token) {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	var err error
	if locks, ok := application.lockSystem.(*boundedLockSystem); ok {
		err = locks.unlockPath(time.Now(), path, token)
	} else {
		err = application.lockSystem.Unlock(time.Now(), token)
	}
	if err != nil {
		slog.Debug("mountdav: UNLOCK rejected", "path", path, "error", err)
		serveUnlockError(response, err)
		return
	}
	slog.Debug("mountdav: UNLOCK", "path", path)
	response.WriteHeader(http.StatusNoContent)
}

func (application *readApplication) writeLockResponse(
	response http.ResponseWriter,
	request *http.Request,
	token string,
	details webdav.LockDetails,
	owner *lockOwnerXML,
) {
	depth := "infinity"
	if details.ZeroDepth {
		depth = "0"
	}
	durationSeconds := max(int64(1), int64(details.Duration/time.Second))
	document := lockPropertyXML{LockDiscovery: lockDiscoveryXML{ActiveLock: activeLockXML{
		Depth:     depth,
		Owner:     owner,
		Timeout:   "Second-" + strconv.FormatInt(durationSeconds, 10),
		LockToken: hrefXML{Href: token},
		LockRoot:  hrefXML{Href: application.absoluteResourceURL(request, details.Root)},
	}}}
	payload, err := xml.Marshal(document)
	if err != nil {
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/xml; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(append([]byte(xml.Header), payload...))
}

func parseLockInfo(body []byte) (lockInfoDocument, int) {
	var document lockInfoDocument
	if err := xml.Unmarshal(body, &document); err != nil {
		return lockInfoDocument{}, http.StatusBadRequest
	}
	if document.Owner != nil && len(document.Owner.Inner) > maxLockOwnerBytes {
		return lockInfoDocument{}, http.StatusRequestEntityTooLarge
	}
	if document.XMLName.Space != webDAVNamespace ||
		document.LockScope.XMLName.Space != webDAVNamespace ||
		document.LockType.XMLName.Space != webDAVNamespace ||
		document.LockType.Write == nil ||
		document.LockType.Write.XMLName.Space != webDAVNamespace ||
		document.LockScope.Exclusive == nil ||
		document.LockScope.Exclusive.XMLName.Space != webDAVNamespace ||
		(document.Owner != nil && document.Owner.XMLName.Space != webDAVNamespace) {
		if document.LockScope.Shared != nil {
			return lockInfoDocument{}, http.StatusNotImplemented
		}
		return lockInfoDocument{}, http.StatusBadRequest
	}
	if document.LockScope.Shared != nil {
		if document.LockScope.Shared.XMLName.Space != webDAVNamespace {
			return lockInfoDocument{}, http.StatusBadRequest
		}
		return lockInfoDocument{}, http.StatusNotImplemented
	}
	return document, 0
}

func parseLockDepth(value string) (string, bool) {
	switch value {
	case "", "infinity":
		return "infinity", true
	case "0":
		return "0", true
	default:
		return "", false
	}
}

func parseLockTimeout(value string) (time.Duration, bool) {
	if value == "" {
		return defaultLockDuration, true
	}
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "Infinite" {
			return maximumLockDuration, true
		}
		if !strings.HasPrefix(candidate, "Second-") {
			continue
		}
		seconds, err := strconv.ParseUint(strings.TrimPrefix(candidate, "Second-"), 10, 32)
		if err != nil || seconds == 0 {
			continue
		}
		duration := time.Duration(seconds) * time.Second
		return min(duration, maximumLockDuration), true
	}
	return 0, false
}

func (application *readApplication) confirmMutationLocks(
	paths []string,
	conditions *MutationConditions,
) (func(), int) {
	paths = uniqueSortedPaths(paths)
	if len(paths) == 0 {
		return func() {}, 0
	}
	hold := &mutationLockHold{locks: application.lockSystem}
	validatedTokens := make(map[string]struct{})
	hasIfHeader := len(conditions.DAVIf) > 0

	for _, path := range paths {
		confirmed, status := false, 0
		if hasIfHeader {
			confirmed, status = application.confirmPathLock(path, conditions.DAVIf, hold, validatedTokens)
			if status != 0 {
				hold.release()
				return nil, status
			}
		}
		if confirmed {
			continue
		}
		if status = application.holdTemporaryLock(path, hasIfHeader, hold); status != 0 {
			hold.release()
			return nil, status
		}
	}

	conditions.LockTokens = make([]string, 0, len(validatedTokens))
	for token := range validatedTokens {
		conditions.LockTokens = append(conditions.LockTokens, token)
	}
	slices.Sort(conditions.LockTokens)
	return hold.release, 0
}

type mutationLockHold struct {
	locks           webdav.LockSystem
	releases        []func()
	temporaryTokens []string
}

func (hold *mutationLockHold) release() {
	for index := len(hold.releases) - 1; index >= 0; index-- {
		hold.releases[index]()
	}
	for index := len(hold.temporaryTokens) - 1; index >= 0; index-- {
		_ = hold.locks.Unlock(time.Now(), hold.temporaryTokens[index])
	}
}

func (application *readApplication) confirmPathLock(
	path string,
	lists []DAVConditionList,
	hold *mutationLockHold,
	validatedTokens map[string]struct{},
) (bool, int) {
	for _, list := range lists {
		if list.ResourcePath != path {
			continue
		}
		token, ok := application.lockTokenForConfirmation(path, list.Conditions)
		if !ok {
			continue
		}
		release, err := application.lockSystem.Confirm(
			time.Now(),
			path,
			"",
			webdav.Condition{Token: token},
		)
		if errors.Is(err, webdav.ErrConfirmationFailed) {
			continue
		}
		if err != nil {
			return false, http.StatusInternalServerError
		}
		hold.releases = append(hold.releases, release)
		validatedTokens[token] = struct{}{}
		return true, 0
	}
	return false, 0
}

// lockTokenForConfirmation intentionally accepts the interoperable exclusive
// lock subset: one positive token plus optional ETag terms. x/net/webdav's
// in-memory lock system does not evaluate Not or conjunctions; treating those
// as authority would let a false state list bypass an exclusive lock.
func (application *readApplication) lockTokenForConfirmation(path string, conditions []DAVCondition) (string, bool) {
	locks, hasInspectableLocks := application.lockSystem.(*boundedLockSystem)
	if !hasInspectableLocks {
		return conservativeLockToken(conditions)
	}
	now := time.Now()
	positiveToken := ""
	for _, condition := range conditions {
		if condition.LockToken == "" {
			continue
		}
		applies := locks.tokenApplies(now, condition.LockToken, path)
		if condition.Not {
			if applies {
				return "", false
			}
			continue
		}
		if !applies || (positiveToken != "" && positiveToken != condition.LockToken) {
			return "", false
		}
		positiveToken = condition.LockToken
	}
	return positiveToken, positiveToken != ""
}

func conservativeLockToken(conditions []DAVCondition) (string, bool) {
	token := ""
	for _, condition := range conditions {
		if condition.LockToken == "" {
			continue
		}
		if condition.Not || (token != "" && token != condition.LockToken) {
			return "", false
		}
		token = condition.LockToken
	}
	return token, token != ""
}

func (application *readApplication) holdTemporaryLock(path string, hasIfHeader bool, hold *mutationLockHold) int {
	token, err := application.lockSystem.Create(time.Now(), webdav.LockDetails{
		Root:      path,
		Duration:  temporaryLockDuration,
		ZeroDepth: false,
	})
	if err == nil {
		hold.temporaryTokens = append(hold.temporaryTokens, token)
		return 0
	}
	switch {
	case errors.Is(err, webdav.ErrLocked):
		if hasIfHeader {
			return http.StatusPreconditionFailed
		}
		return statusLocked
	case errors.Is(err, errLockCapacity):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func uniqueSortedPaths(paths []string) []string {
	result := slices.Clone(paths)
	slices.Sort(result)
	return slices.Compact(result)
}

func serializeEntityTag(tag EntityTag) string {
	prefix := ""
	if tag.Weak {
		prefix = "W/"
	}
	return prefix + `"` + tag.Opaque + `"`
}

func readBoundedApplicationBody(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	limited := io.LimitReader(body, maxRequestBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxRequestBodyBytes {
		return nil, io.ErrShortBuffer
	}
	return data, nil
}

func serveLockError(response http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, webdav.ErrLocked):
		status = statusLocked
	case errors.Is(err, webdav.ErrNoSuchLock):
		status = http.StatusPreconditionFailed
	case errors.Is(err, errLockCapacity):
		response.Header().Set("Retry-After", serverBusyRetrySeconds)
		status = http.StatusServiceUnavailable
	}
	writeHTTPError(response, status)
}

func serveUnlockError(response http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, webdav.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, webdav.ErrLocked):
		status = statusLocked
	case errors.Is(err, webdav.ErrNoSuchLock):
		status = http.StatusConflict
	}
	writeHTTPError(response, status)
}

func (application *readApplication) absoluteResourceURL(request *http.Request, path string) string {
	resource := &url.URL{
		Scheme: "http",
		Host:   application.authority,
		Path:   application.capabilityPath + path,
	}
	if application.authority == "" {
		resource.Host = request.Host
	}
	return resource.String()
}

var _ webdav.LockSystem = (*boundedLockSystem)(nil)
