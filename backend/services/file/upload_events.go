package file

import (
	"math"
	"sync"
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

const maxImportUploadFailureReasons = 3

// importUploadObserver folds a window's concurrent file updates into at most
// one event per whole percentage point. Its active map is bounded by the import
// upload window, so neither backend nor frontend state grows with the import.
type importUploadObserver struct {
	service *Service
	total   int

	mu             sync.Mutex
	active         map[int]float64
	activeProgress float64
	done           int
	failed         int
	lastPercent    int
	failureReasons []string
}

func newImportUploadObserver(service *Service, total int) *importUploadObserver {
	return &importUploadObserver{
		service:     service,
		total:       max(total, 0),
		active:      make(map[int]float64, importUploadBatchSize),
		lastPercent: -1,
	}
}

func (o *importUploadObserver) Started(int, string, int64, string) {}

func (o *importUploadObserver) Progress(id int, percent float64) {
	fraction := math.Max(0, math.Min(1, percent/100))
	o.mu.Lock()
	previous := o.active[id]
	if fraction > previous {
		o.active[id] = fraction
		o.activeProgress += fraction - previous
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
	o.removeActiveLocked(id)
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

func (o *importUploadObserver) removeActiveLocked(id int) {
	if fraction, ok := o.active[id]; ok {
		o.activeProgress -= fraction
		delete(o.active, id)
	}
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
	if !force && (!allowEmit || wholePercent <= o.lastPercent) {
		return nil, false
	}
	o.lastPercent = wholePercent
	return map[string]any{
		"total":    o.total,
		"done":     o.done,
		"failed":   o.failed,
		"progress": progress,
	}, true
}
