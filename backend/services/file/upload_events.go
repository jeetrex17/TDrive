package file

import (
	"errors"
	"math"
	"slices"
	"sync"
	"time"
)

type uploadObserver interface {
	Started(id int, name string, size int64, parentID string)
	Progress(id int, percent float64)
	Completed(id int, name string)
	Failed(id int, name string, err error)
}

type detailedUploadObserver struct {
	service *Service
}

func (o detailedUploadObserver) Started(id int, name string, size int64, parentID string) {
	o.service.emitEvent("upload_start", id, name, size, parentID)
}

func (o detailedUploadObserver) Progress(id int, percent float64) {
	o.service.emitEvent("upload_progress", id, percent)
}

// Accepted tells the row its file is on Telegram and only the local index
// entry is pending, so it reads as saving rather than stuck at 100%.
func (o detailedUploadObserver) Accepted(id int) {
	o.service.emitEvent("upload_accepted", id)
}

func (o detailedUploadObserver) Completed(id int, name string) {
	o.service.emitEvent("upload_complete", id, name)
}

func (o detailedUploadObserver) Failed(id int, name string, err error) {
	o.service.emitEvent("upload_error", id, name, err.Error())
}

const (
	maxImportUploadFailureReasons = 3

	// How many in-flight files one payload names. Files Telegram has accepted
	// leave the in-flight set, so it is bounded by upload concurrency and every
	// sending file can be named; activeCount reports any left out.
	maxImportUploadActiveFiles = maxUploadConcurrency

	// Floor between two payloads that only report a change in *which* files are
	// moving. Percent-driven payloads are self-limiting -- at most one per whole
	// point, so 100 for the whole import -- but membership changes twice per
	// file, which for a ten-thousand-file import is not a bound at all.
	minImportUploadActiveInterval = 250 * time.Millisecond
)

// importUploadFile is one upload the window currently has in flight. It is kept
// only between that file's Started and its Completed/Failed, so the set is
// bounded by upload concurrency and not by the size of the import.
type importUploadFile struct {
	name     string
	size     int64
	fraction float64
}

// importUploadObserver folds a window's concurrent file updates into at most
// one event per whole percentage point, plus a rate-limited one when the set of
// files in flight changes. Its active map is bounded by the import upload
// window, so neither backend nor frontend state grows with the import.
type importUploadObserver struct {
	service *Service
	total   int
	now     func() time.Time

	mu             sync.Mutex
	active         map[int]*importUploadFile
	activeProgress float64
	doneBytes      int64
	done           int
	failed         int
	// Files the user cancelled on their own: neither uploaded nor failed, but
	// settled all the same.
	canceled       int
	lastPercent    int
	activeDirty    bool
	lastEmit       time.Time
	failureReasons []string
	// Files settled since the last payload. Each file is reported once, so the
	// list a reader assembles is bounded by the import itself.
	settled []map[string]any
	// Display path per upload id for the window in flight; an entry leaves when
	// its file settles, so this never outgrows one window.
	labels map[int]string
	// Files Telegram holds whose index entry waits for the rest of their
	// window, and those accepted since the last payload. Both are bounded by
	// one window.
	accepted    map[int]*importUploadFile
	acceptedNew []int
}

func newImportUploadObserver(service *Service, total int) *importUploadObserver {
	return &importUploadObserver{
		service:     service,
		total:       max(total, 0),
		now:         time.Now,
		active:      make(map[int]*importUploadFile, maxUploadConcurrency),
		labels:      make(map[int]string, importUploadBatchSize),
		accepted:    make(map[int]*importUploadFile, importUploadBatchSize),
		lastPercent: -1,
	}
}

// Started names the file so the aggregate row can show what is actually moving.
// Its size is kept to weigh partial progress in the byte total; the row's bar
// still runs on completed-file fractions, which no size estimate can skew.
func (o *importUploadObserver) Started(id int, name string, size int64, _ string) {
	o.mu.Lock()
	o.active[id] = &importUploadFile{name: name, size: max(size, 0)}
	o.activeDirty = true
	payload, emit := o.progressPayloadLocked(false, true)
	o.mu.Unlock()
	if emit {
		o.service.emitEvent("import_upload_progress", payload)
	}
}

func (o *importUploadObserver) Progress(id int, percent float64) {
	fraction := math.Max(0, math.Min(1, percent/100))
	o.mu.Lock()
	if file, ok := o.active[id]; ok && fraction > file.fraction {
		o.activeProgress += fraction - file.fraction
		file.fraction = fraction
	}
	// A file is not complete until its Telegram receipt is projected. Deferring
	// its final 100% update lets the aggregate counter and percentage advance in
	// the same bounded event.
	payload, emit := o.progressPayloadLocked(false, fraction < 1)
	o.mu.Unlock()
	if emit {
		o.service.emitEvent("import_upload_progress", payload)
	}
}

// Accepted moves a file from sending to saving: it counts as fully moved, and
// stops crowding the in-flight list while its window finishes.
func (o *importUploadObserver) Accepted(id int) {
	o.mu.Lock()
	o.accepted[id] = o.removeActiveLocked(id) // nil when its start was never seen
	o.acceptedNew = append(o.acceptedNew, id)
	payload, emit := o.progressPayloadLocked(false, true)
	o.mu.Unlock()
	if emit {
		o.service.emitEvent("import_upload_progress", payload)
	}
}

// settledFileLocked takes a file out of whichever set holds it.
func (o *importUploadObserver) settledFileLocked(id int) *importUploadFile {
	if file, ok := o.accepted[id]; ok {
		delete(o.accepted, id)
		return file
	}
	return o.removeActiveLocked(id)
}

func (o *importUploadObserver) Completed(id int, name string) {
	o.mu.Lock()
	file := o.settledFileLocked(id)
	if file != nil {
		o.doneBytes += file.size
	}
	o.settleLocked(id, name, file, "done", "")
	o.done++
	payload, emit := o.progressPayloadLocked(false, true)
	o.mu.Unlock()
	if emit {
		o.service.emitEvent("import_upload_progress", payload)
	}
}

func (o *importUploadObserver) Failed(id int, name string, err error) {
	o.mu.Lock()
	if errors.Is(err, errUploadCanceled) {
		o.settleLocked(id, name, o.settledFileLocked(id), "canceled", "")
		o.canceled++
	} else {
		o.settleLocked(id, name, o.settledFileLocked(id), "failed", err.Error())
		o.failed++
		if len(o.failureReasons) < maxImportUploadFailureReasons {
			if name == "" {
				o.failureReasons = append(o.failureReasons, err.Error())
			} else {
				o.failureReasons = append(o.failureReasons, name+": "+err.Error())
			}
		}
	}
	payload, emit := o.progressPayloadLocked(false, true)
	o.mu.Unlock()
	if emit {
		o.service.emitEvent("import_upload_progress", payload)
	}
}

func (o *importUploadObserver) EmitInitial() {
	o.mu.Lock()
	payload, emit := o.progressPayloadLocked(true, true)
	o.mu.Unlock()
	if emit {
		o.service.emitEvent("import_upload_progress", payload)
	}
}

// LabelWindow records where each file of the next window sits in the selection.
func (o *importUploadObserver) LabelWindow(idOffset int, labels []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for index, label := range labels {
		if label != "" {
			o.labels[idOffset+index] = label
		}
	}
}

// Flush sends any settled or accepted files no throttled payload has carried
// yet, so the last few outcomes reach the reader before import_complete does.
func (o *importUploadObserver) Flush() {
	o.mu.Lock()
	if len(o.settled) == 0 && len(o.acceptedNew) == 0 {
		o.mu.Unlock()
		return
	}
	payload, _ := o.progressPayloadLocked(true, true)
	o.mu.Unlock()
	o.service.emitEvent("import_upload_progress", payload)
}

func (o *importUploadObserver) settleLocked(id int, name string, file *importUploadFile, status, message string) {
	entry := map[string]any{"id": id, "name": name, "status": status}
	if label, ok := o.labels[id]; ok {
		entry["path"] = label
		delete(o.labels, id)
	}
	if file != nil {
		entry["size"] = file.size
		if name == "" {
			entry["name"] = file.name
		}
	}
	if message != "" {
		entry["message"] = message
	}
	o.settled = append(o.settled, entry)
}

func (o *importUploadObserver) Summary() (done, failed, canceled int, failureReasons []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.done, o.failed, o.canceled, append([]string(nil), o.failureReasons...)
}

func (o *importUploadObserver) removeActiveLocked(id int) *importUploadFile {
	file, ok := o.active[id]
	if !ok {
		return nil
	}
	o.activeProgress -= file.fraction
	delete(o.active, id)
	o.activeDirty = true
	return file
}

// activeFilesLocked lists the longest-running files in flight, oldest first,
// together with the bytes their partial progress is worth.
//
// Upload ids are handed out in start order across every window of an import, so
// sorting the (concurrency-bounded) key set is the start order -- no separate
// ordering structure has to be kept in step with the map.
func (o *importUploadObserver) activeFilesLocked() (files []map[string]any, partialBytes float64) {
	ids := make([]int, 0, len(o.active))
	for id, file := range o.active {
		ids = append(ids, id)
		partialBytes += file.fraction * float64(file.size)
	}
	slices.Sort(ids)
	files = make([]map[string]any, 0, min(len(ids), maxImportUploadActiveFiles))
	for _, id := range ids[:min(len(ids), maxImportUploadActiveFiles)] {
		file := o.active[id]
		entry := map[string]any{
			"id":      id,
			"name":    file.name,
			"size":    file.size,
			"percent": file.fraction * 100,
		}
		if label, ok := o.labels[id]; ok {
			entry["path"] = label
		}
		files = append(files, entry)
	}
	return files, partialBytes
}

func (o *importUploadObserver) progressPayloadLocked(force, allowEmit bool) (map[string]any, bool) {
	progress := 100.0
	if o.total > 0 {
		progress = (float64(o.done+o.failed+o.canceled+len(o.accepted)) + o.activeProgress) / float64(o.total) * 100
		progress = math.Max(0, math.Min(100, progress))
	}
	wholePercent := int(math.Floor(progress))
	if wholePercent < o.lastPercent {
		wholePercent = o.lastPercent
		progress = float64(wholePercent)
	}
	now := o.now()
	// Two reasons to speak, and both are bounded: the bar moved a whole point,
	// or a different file is moving and the last payload is old enough.
	advanced := wholePercent > o.lastPercent
	churned := o.activeDirty && now.Sub(o.lastEmit) >= minImportUploadActiveInterval
	if !force && (!allowEmit || (!advanced && !churned)) {
		return nil, false
	}
	o.lastPercent = wholePercent
	o.activeDirty = false
	o.lastEmit = now
	active, partialBytes := o.activeFilesLocked()
	settled := o.settled
	o.settled = nil
	acceptedNew := o.acceptedNew
	o.acceptedNew = nil
	var acceptedBytes int64
	for _, file := range o.accepted {
		if file != nil {
			acceptedBytes += file.size
		}
	}
	return map[string]any{
		"total":       o.total,
		"done":        o.done,
		"failed":      o.failed,
		"canceled":    o.canceled,
		"progress":    progress,
		"bytes":       o.doneBytes + acceptedBytes + int64(partialBytes),
		"active":      active,
		"activeCount": len(o.active),
		"settled":     settled,
		"accepted":    acceptedNew,
		"saving":      len(o.accepted),
	}, true
}
