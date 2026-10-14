package warcvalidator

import (
	"io"
	"os"
	"path/filepath"
	"sort"

	warc "github.com/internetarchive/gowarc"
)

// readWARCs reads all WARC files from a directory and returns a map of records keyed by WARC-Record-ID.
func readWARCs(dir string) (map[string]*warc.Record, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, nil
	}

	sort.Strings(files)

	allRecords := make(map[string]*warc.Record)

	for _, file := range files {
		stat, err := os.Stat(file)
		if err != nil || stat.IsDir() {
			continue
		}

		records, err := readWARCFile(file)
		if err != nil {
			return nil, err
		}

		for id, rec := range records {
			allRecords[id] = rec
		}
	}

	return allRecords, nil
}

// readWARCFile reads a single WARC file and returns its records as a map keyed by WARC-Record-ID.
func readWARCFile(file string) (map[string]*warc.Record, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader, err := warc.NewReader(f)
	if err != nil {
		return nil, err
	}

	records := make(map[string]*warc.Record)

	for {
		rec, err := reader.ReadRecord()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := validateRecordDigests(rec, file); err != nil {
			rec.Content.Close()
			return nil, err
		}
		// we only care about request, response and revisit records
		// info records are dropped (digests still validated above)
		if isInfoRecord(rec) {
			rec.Content.Close()
			continue
		}
		recordID := rec.Header.Get("WARC-Record-ID")
		records[recordID] = rec
	}

	return records, nil
}
