package file

import (
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

func (o detailedUploadObserver) Completed(id int, name string) {
	o.service.emitEvent("upload_complete", id, name)
}

func (o detailedUploadObserver) Failed(id int, name string, err error) {
	o.service.emitEvent("upload_error", id, name, err.Error())
}

const (
	maxImportUploadFailureReasons = 3

	// How many in-flight files one payload names. Upload concurrency is capped
	// at maxUploadConcurrency, so this bounds the payload rather than the
	// observer's state; activeCount reports the ones left out.
	maxImportUploadActiveFiles = 4

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
	lastPercent    int
	activeDirty    bool
	lastEmit       time.Time
	failureReasons []string
}

func newImportUploadObserver(service *Service, total int) *importUploadObserver {
	return &importUploadObserver{
		service:     service,
		total:       max(total, 0),
		now:         time.Now,
		active:      make(map[int]*importUploadFile, maxUploadConcurrency),
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

func (o *importUploadObserver) Completed(id int, _ string) {
	o.mu.Lock()
	if file := o.removeActiveLocked(id); file != nil {
		o.doneBytes += file.size
	}
	o.done++
	payload, emit := o.progressPayloadLocked(false, true)
	o.mu.Unlock()
	if emit {
		o.service.emitEvent("import_upload_progress", payload)
	}
}

func (o *importUploadObserver) Failed(id int, name string, err error) {
	o.mu.Lock()
	o.removeActiveLocked(id)
	o.failed++
	if len(o.failureReasons) < maxImportUploadFailureReasons {
		if name == "" {
			o.failureReasons = append(o.failureReasons, err.Error())
		} else {
			o.failureReasons = append(o.failureReasons, name+": "+err.Error())
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

func (o *importUploadObserver) Summary() (done, failed int, failureReasons []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.done, o.failed, append([]string(nil), o.failureReasons...)
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
		files = append(files, map[string]any{
			"id":      id,
			"name":    file.name,
			"size":    file.size,
			"percent": file.fraction * 100,
		})
	}
	return files, partialBytes
}

func (o *importUploadObserver) progressPayloadLocked(force, allowEmit bool) (map[string]any, bool) {
	progress := 100.0
	if o.total > 0 {
		progress = (float64(o.done+o.failed) + o.activeProgress) / float64(o.total) * 100
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
	return map[string]any{
		"total":       o.total,
		"done":        o.done,
		"failed":      o.failed,
		"progress":    progress,
		"bytes":       o.doneBytes + int64(partialBytes),
		"active":      active,
		"activeCount": len(o.active),
	}, true
}
