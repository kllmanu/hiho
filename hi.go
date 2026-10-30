package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	_ "modernc.org/sqlite"
)

type browserConfig struct {
	baseDir  string
	database string
	query    string
}

type archiveRow struct {
	id         int64
	url        string
	title      sql.NullString
	lastVisit  sql.NullString
	visitCount int64
}

type archivedEntry map[string]json.RawMessage

var browsers = map[string]browserConfig{
	"firefox": {
		baseDir:  ".config/mozilla/firefox",
		database: "places.sqlite",
		query: `SELECT
			moz_places.id,
			moz_places.url,
			moz_places.title,
			datetime(moz_places.last_visit_date / 1000000, 'unixepoch', 'localtime'),
			moz_places.visit_count
		FROM moz_places
		WHERE moz_places.last_visit_date IS NOT NULL
		ORDER BY moz_places.last_visit_date DESC`,
	},
	"chrome": {
		baseDir:  ".config/google-chrome",
		database: "History",
		query: `SELECT
			id,
			url,
			title,
			datetime(last_visit_time / 1000000 - 11644473600, 'unixepoch', 'localtime'),
			visit_count
		FROM urls
		WHERE last_visit_time > 0
		ORDER BY last_visit_time DESC`,
	},
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not determine home directory: %v\n", err)
		os.Exit(1)
	}
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not determine hostname: %v\n", err)
		os.Exit(1)
	}

	failed := false
	for _, browser := range []string{"firefox", "chrome"} {
		if err := exportToJSON(browser, home, hostname); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func findDatabase(browser, home string) (string, error) {
	config, ok := browsers[browser]
	if !ok {
		return "", fmt.Errorf("unsupported browser %q", browser)
	}
	basePath := filepath.Join(home, config.baseDir)
	var found string
	err := filepath.WalkDir(basePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) && path == basePath {
				return filepath.SkipDir
			}
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == config.database {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("searching for %s history database: %w", browser, err)
	}
	if found == "" {
		return "", fmt.Errorf("could not locate %s history database", browser)
	}
	return found, nil
}

func exportToJSON(browser, home, hostname string) error {
	config, ok := browsers[browser]
	if !ok {
		return fmt.Errorf("unsupported browser %q", browser)
	}
	docsDir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return fmt.Errorf("creating documents directory: %w", err)
	}
	outputPath := filepath.Join(docsDir, fmt.Sprintf("history_%s_%s.json", browser, hostname))

	historyPath, err := findDatabase(browser, home)
	if err != nil {
		return err
	}
	fmt.Printf("Found database at: %s\n", historyPath)

	entries, err := loadEntries(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not read existing JSON file, starting fresh: %v\n", err)
		entries = nil
	} else if entries != nil {
		fmt.Printf("Loaded %d existing entries from %s\n", len(entries), outputPath)
	}

	rows, err := readHistory(historyPath, config.query)
	if err != nil {
		return fmt.Errorf("could not read %s history: %w", browser, err)
	}

	merged, added := mergeEntries(entries, rows)
	if err := saveEntries(outputPath, merged); err != nil {
		return fmt.Errorf("saving archive to %s: %w", outputPath, err)
	}
	fmt.Printf("Successfully saved archive to %s. Total entries: %d (Added/Updated: %d)\n", outputPath, len(merged), added)
	return nil
}

func loadEntries(path string) ([]archivedEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var rawEntries []json.RawMessage
	if err := json.Unmarshal(data, &rawEntries); err != nil {
		return nil, err
	}
	entries := make([]archivedEntry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		var entry archivedEntry
		if err := json.Unmarshal(raw, &entry); err != nil || entry == nil {
			return nil, errors.New("existing JSON contains an invalid entry")
		}
		if _, ok := entry["id"]; !ok {
			return nil, errors.New("existing JSON entry has no id")
		}
		if _, ok := entry["last_visit"]; !ok {
			return nil, errors.New("existing JSON entry has no last_visit")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func readHistory(path, query string) ([]archiveRow, error) {
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []archiveRow
	for rows.Next() {
		var row archiveRow
		if err := rows.Scan(&row.id, &row.url, &row.title, &row.lastVisit, &row.visitCount); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func sqliteReadOnlyDSN(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	return uri.String() + "?mode=ro"
}

func mergeEntries(existing []archivedEntry, rows []archiveRow) ([]archivedEntry, int) {
	entries := append([]archivedEntry(nil), existing...)
	indices := make(map[string]int, len(entries)+len(rows))
	for i, entry := range entries {
		indices[string(entry["id"])] = i
	}

	added := 0
	for _, row := range rows {
		title := row.title.String
		if !row.title.Valid || title == "" {
			title = "No Title"
		}
		lastVisit := row.lastVisit.String
		if !row.lastVisit.Valid || lastVisit == "" {
			lastVisit = "Unknown"
		}
		entry := archivedEntry{
			"id":          mustJSON(row.id),
			"url":         mustJSON(row.url),
			"title":       mustJSON(title),
			"last_visit":  mustJSON(lastVisit),
			"visit_count": mustJSON(row.visitCount),
		}
		key := strconv.FormatInt(row.id, 10)
		if index, exists := indices[key]; exists {
			entries[index] = entry
		} else {
			indices[key] = len(entries)
			entries = append(entries, entry)
			added++
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return lastVisitValue(entries[i]) > lastVisitValue(entries[j])
	})
	return entries, added
}

func lastVisitValue(entry archivedEntry) string {
	var value string
	if err := json.Unmarshal(entry["last_visit"], &value); err != nil || value == "Unknown" {
		return ""
	}
	return value
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func saveEntries(path string, entries []archivedEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return nil
}
