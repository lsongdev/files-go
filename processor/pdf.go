package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

type PDFMetadata struct {
	catalog  *catalog.Catalog
	storages *storage.Registry
	binary   string
	timeout  time.Duration
}

func NewPDFMetadata(catalog *catalog.Catalog, storages *storage.Registry, binary string, timeout time.Duration) *PDFMetadata {
	if binary == "" {
		binary = "pdfinfo"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &PDFMetadata{catalog: catalog, storages: storages, binary: binary, timeout: timeout}
}

func (p *PDFMetadata) Name() string { return "pdf_metadata" }
func (p *PDFMetadata) Match(entry model.Entry) bool {
	return !isMetadataSidecar(entry) && strings.EqualFold(entry.Extension, "pdf")
}

func (p *PDFMetadata) Process(ctx context.Context, entry model.Entry) error {
	backend, ok := p.storages.Get(entry.StorageID)
	if !ok {
		return storage.ErrOffline
	}
	native, ok := backend.(storage.NativePather)
	if !ok {
		return storage.ErrUnsupported
	}
	filename, err := native.NativePath(ctx, entry.Path)
	if err != nil {
		return err
	}
	processCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	command := exec.CommandContext(processCtx, p.binary, filename)
	stdout, stderr := &limitedBuffer{limit: 1 << 20}, &limitedBuffer{limit: 64 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if processCtx.Err() != nil {
			return fmt.Errorf("pdfinfo timeout: %w", processCtx.Err())
		}
		if stdout.exceeded || stderr.exceeded {
			return ErrProcessOutputTooLarge
		}
		return fmt.Errorf("pdfinfo: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	metadata := parsePDFInfo(stdout.String())
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return writePDFMedia(ctx, p.catalog, entry, encoded)
}

func parsePDFInfo(output string) map[string]any {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if key, value, found := strings.Cut(line, ":"); found {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	metadata := map[string]any{}
	for source, target := range map[string]string{"Title": "title", "Author": "author", "Subject": "subject", "Keywords": "keywords", "Creator": "creator", "Producer": "producer", "CreationDate": "createdAt", "ModDate": "modifiedAt", "Encrypted": "encrypted", "Page size": "pageSize", "PDF version": "pdfVersion"} {
		if value := values[source]; value != "" {
			metadata[target] = value
		}
	}
	if pages, err := strconv.Atoi(values["Pages"]); err == nil && pages >= 0 {
		metadata["pageCount"] = pages
	}
	return metadata
}
