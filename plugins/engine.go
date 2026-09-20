package plugins

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
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/storage"
)

const JobProcessEntry = "process_entry"
const pipelineVersion = 10

type Plugin interface {
	Name() string
	Match(model.Entry) bool
	Steps() []processor.Processor
}

type pipeline struct {
	name  string
	match func(model.Entry) bool
	steps []processor.Processor
}

func NewPlugin(name string, match func(model.Entry) bool, steps ...processor.Processor) Plugin {
	if name == "" || match == nil || len(steps) == 0 {
		panic("enrichment plugin requires a name, matcher and steps")
	}
	for _, step := range steps {
		if step == nil {
			panic("enrichment plugin contains a nil step")
		}
	}
	return &pipeline{name: name, match: match, steps: append([]processor.Processor(nil), steps...)}
}

func (p *pipeline) Name() string                 { return p.name }
func (p *pipeline) Match(entry model.Entry) bool { return p.match(entry) }
func (p *pipeline) Steps() []processor.Processor           { return append([]processor.Processor(nil), p.steps...) }

// SelectPlugins applies the configured plugin order. An empty order enables
// every available plugin in its declaration order; otherwise presence means
// enabled and omission means disabled.
func SelectPlugins(order []string, available ...Plugin) ([]Plugin, error) {
	if order == nil {
		return append([]Plugin(nil), available...), nil
	}
	byName := make(map[string]Plugin, len(available))
	for _, plugin := range available {
		if _, exists := byName[plugin.Name()]; exists {
			return nil, fmt.Errorf("duplicate available plugin %q", plugin.Name())
		}
		byName[plugin.Name()] = plugin
	}
	selected := make([]Plugin, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, raw := range order {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, errors.New("plugin name cannot be empty")
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate plugin %q", name)
		}
		plugin, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown plugin %q", name)
		}
		seen[name] = true
		selected = append(selected, plugin)
	}
	return selected, nil
}

type Engine struct {
	catalog     *catalog.Catalog
	queue       *jobs.Queue
	plugins     []registeredPlugin
	fingerprint string
}

type registeredPlugin struct {
	plugin Plugin
	steps  []processor.Processor
}

func New(catalog *catalog.Catalog, queue *jobs.Queue, plugins ...Plugin) *Engine {
	names := make([]string, 0, len(plugins))
	registered := make([]registeredPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		steps := plugin.Steps()
		registered = append(registered, registeredPlugin{plugin: plugin, steps: steps})
		names = append(names, plugin.Name())
		for _, step := range steps {
			names = append(names, step.Name())
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(names, ",")))
	return &Engine{catalog: catalog, queue: queue, plugins: registered, fingerprint: fmt.Sprintf("v%d-%x", pipelineVersion, sum[:4])}
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
	for _, plugin := range e.plugins {
		if plugin.plugin.Match(entry) {
			return true
		}
	}
	return false
}

func (e *Engine) ReprocessEntry(ctx context.Context, entry model.Entry) error {
	return e.ReprocessEntryPriority(ctx, entry, 1000)
}

func (e *Engine) ReprocessEntryPriority(ctx context.Context, entry model.Entry, priority int) error {
	key := e.processEntryKey(entry)
	if err := e.queue.Requeue(ctx, JobProcessEntry, key, priority); err == nil {
		return nil
	} else if !errors.Is(err, jobs.ErrNotFound) {
		return err
	}
	return e.queue.EnqueueMany(ctx, []jobs.Request{{
		Type: JobProcessEntry, Payload: entryPayload{EntryID: entry.ID},
		Options: jobs.EnqueueOptions{Key: key, Priority: priority},
	}})
}

func (e *Engine) processEntryKey(entry model.Entry) string {
	return fmt.Sprintf("%s:%s:%d:%d", e.fingerprint, entry.ID, entry.ModifiedAt.UnixNano(), entry.Size)
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
	var failures []error
	for _, plugin := range e.plugins {
		if !plugin.plugin.Match(*entry) {
			continue
		}
		for _, step := range plugin.steps {
			if !step.Match(*entry) {
				continue
			}
			if err := step.Process(ctx, *entry); err != nil {
				failures = append(failures, fmt.Errorf("plugin %s step %s: %w", plugin.plugin.Name(), step.Name(), err))
			}
		}
	}
	return errors.Join(failures...)
}
