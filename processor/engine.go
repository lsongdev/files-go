package processor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/jobs"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

const JobProcessEntry = "process_entry"
const pipelineVersion = 3

type Processor interface {
	Name() string
	Match(model.Entry) bool
	Process(context.Context, model.Entry) error
}

type Engine struct {
	catalog     *catalog.Catalog
	queue       *jobs.Queue
	processors  []Processor
	fingerprint string
}

func New(catalog *catalog.Catalog, queue *jobs.Queue, processors ...Processor) *Engine {
	names := make([]string, 0, len(processors))
	for _, item := range processors {
		names = append(names, item.Name())
	}
	sum := sha256.Sum256([]byte(strings.Join(names, ",")))
	return &Engine{catalog: catalog, queue: queue, processors: append([]Processor(nil), processors...), fingerprint: fmt.Sprintf("v%d-%x", pipelineVersion, sum[:4])}
}

type entryPayload struct {
	EntryID string `json:"entryId"`
}

func (e *Engine) EnqueueEntries(ctx context.Context, entries []model.Entry) error {
	requests := make([]jobs.Request, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != model.EntryFile || !e.matches(entry) {
			continue
		}
		key := e.processEntryKey(entry)
		requests = append(requests, jobs.Request{Type: JobProcessEntry, Payload: entryPayload{EntryID: entry.ID}, Options: jobs.EnqueueOptions{Key: key}})
	}
	return e.queue.EnqueueMany(ctx, requests)
}

func (e *Engine) matches(entry model.Entry) bool {
	for _, item := range e.processors {
		if item.Match(entry) {
			return true
		}
	}
	return false
}

func (e *Engine) ReprocessEntry(ctx context.Context, entry model.Entry) error {
	key := e.processEntryKey(entry)
	if err := e.queue.Requeue(ctx, JobProcessEntry, key, 100); err == nil {
		return nil
	} else if !errors.Is(err, jobs.ErrNotFound) {
		return err
	}
	return e.queue.EnqueueMany(ctx, []jobs.Request{{
		Type: JobProcessEntry, Payload: entryPayload{EntryID: entry.ID},
		Options: jobs.EnqueueOptions{Key: key, Priority: 100},
	}})
}

func (e *Engine) processEntryKey(entry model.Entry) string {
	return fmt.Sprintf("%s:%s:%d:%d", e.fingerprint, entry.ID, entry.ModifiedAt.UnixNano(), entry.Size)
}

func isMetadataSidecar(entry model.Entry) bool {
	return strings.HasPrefix(entry.Name, "._")
}

func (e *Engine) Handle(ctx context.Context, job *jobs.Job) error {
	var payload entryPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.EntryID == "" {
		return errors.New("invalid process_entry payload")
	}
	entry, err := e.catalog.Entry(ctx, payload.EntryID)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !entry.Available {
		return storage.ErrOffline
	}
	for _, item := range e.processors {
		if !item.Match(*entry) {
			continue
		}
		if err := item.Process(ctx, *entry); err != nil {
			return fmt.Errorf("processor %s: %w", item.Name(), err)
		}
	}
	return nil
}
