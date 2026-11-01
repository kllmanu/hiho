package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

type browserConfig struct {
	baseDirs []string
	database string
	query    string
}

type historyDatabase struct {
	path    string
	profile string
}

type archiveRow struct {
	id         int64
	url        string
	title      sql.NullString
	lastVisit  sql.NullString
	visitCount int64
	profile    string
}

type archivedEntry map[string]json.RawMessage

var browsers = map[string]browserConfig{
	"firefox": {
		baseDirs: []string{
			".config/mozilla/firefox",
			".mozilla/firefox",
			".var/app/org.mozilla.firefox/.mozilla/firefox",
		},
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
		baseDirs: []string{
			".config/google-chrome",
			".var/app/com.google.Chrome/config/google-chrome",
		},
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
	"brave": {
		baseDirs: []string{
			".var/app/com.brave.Browser/config/BraveSoftware/Brave-Browser",
			".config/BraveSoftware/Brave-Browser",
		},
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
	for _, browser := range []string{"firefox", "chrome", "brave"} {
		if err := exportToJSON(browser, home, hostname); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func findDatabases(browser, home string) ([]historyDatabase, error) {
	config, ok := browsers[browser]
	if !ok {
		return nil, fmt.Errorf("unsupported browser %q", browser)
	}
	var found []historyDatabase
	for rootIndex, baseDir := range config.baseDirs {
		basePath := filepath.Join(home, baseDir)
		err := filepath.WalkDir(basePath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, fs.ErrNotExist) && path == basePath {
					return filepath.SkipDir
				}
				return walkErr
			}
			if !entry.IsDir() && entry.Name() == config.database {
				profile, err := filepath.Rel(basePath, filepath.Dir(path))
				if err != nil {
					return fmt.Errorf("determining profile for %s: %w", path, err)
				}
				// Keep existing profile keys for the original root; namespace new roots.
				if rootIndex > 0 {
					profile = filepath.Join(baseDir, profile)
				}
				found = append(found, historyDatabase{path: path, profile: profile})
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("searching for %s history databases: %w", browser, err)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("could not locate %s history database", browser)
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

	historyPaths, err := findDatabases(browser, home)
	if err != nil {
		return err
	}
	for _, database := range historyPaths {
		fmt.Printf("Found database at: %s\n", database.path)
	}

	entries, err := loadEntries(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not read existing JSON file, starting fresh: %v\n", err)
		entries = nil
	} else if entries != nil {
		fmt.Printf("Loaded %d existing entries from %s\n", len(entries), outputPath)
	}

	var allRows []archiveRow
	for _, database := range historyPaths {
		rows, err := readHistory(database.path, config.query)
		if err != nil {
			return fmt.Errorf("could not read %s history database %s: %w", browser, database.path, err)
		}
		for i := range rows {
			rows[i].profile = database.profile
		}
		allRows = append(allRows, rows...)
	}

	merged, added := mergeEntries(entries, allRows, historyPaths[0].profile, hostname)
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
		if profile, ok := entry["profile"]; ok {
			var value string
			if err := json.Unmarshal(profile, &value); err != nil {
				return nil, errors.New("existing JSON entry has an invalid profile")
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func readHistory(path, query string) ([]archiveRow, error) {
	tempDir, err := os.MkdirTemp("", "hi-history-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	snapshotPath := filepath.Join(tempDir, "history.sqlite")
	if err := copyDatabaseSnapshot(path, snapshotPath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", sqliteFileDSN(snapshotPath))
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

type fileState struct {
	exists  bool
	size    int64
	modTime time.Time
}

func copyDatabaseSnapshot(sourcePath, destinationPath string) error {
	sourceFiles := []string{sourcePath, sourcePath + "-wal", sourcePath + "-journal"}
	destinationFiles := []string{destinationPath, destinationPath + "-wal", destinationPath + "-journal"}

	for attempt := 0; attempt < 5; attempt++ {
		before, err := getFileStates(sourceFiles)
		if err != nil {
			return err
		}
		if !before[0].exists {
			return fmt.Errorf("SQLite database %s does not exist", sourcePath)
		}
		sidecarDisappeared := false
		for i, source := range sourceFiles {
			if before[i].exists {
				if err := copyFile(source, destinationFiles[i]); err != nil {
					if i > 0 && errors.Is(err, os.ErrNotExist) {
						sidecarDisappeared = true
						break
					}
					return fmt.Errorf("copying SQLite file %s: %w", source, err)
				}
			} else if err := os.Remove(destinationFiles[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("removing stale SQLite snapshot file %s: %w", destinationFiles[i], err)
			}
		}
		if sidecarDisappeared {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		after, err := getFileStates(sourceFiles)
		if err != nil {
			return err
		}
		if equalFileStates(before, after) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("SQLite database changed while being copied; retry after browser activity settles")
}

func getFileStates(paths []string) ([]fileState, error) {
	states := make([]fileState, len(paths))
	for i, path := range paths {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking SQLite file %s: %w", path, err)
		}
		states[i] = fileState{exists: true, size: info.Size(), modTime: info.ModTime()}
	}
	return states, nil
}

func equalFileStates(left, right []fileState) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func copyFile(sourcePath, destinationPath string) (resultErr error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := source.Close(); resultErr == nil {
			resultErr = err
		}
	}()

	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err := destination.Close(); resultErr == nil {
			resultErr = err
		}
	}()

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	return nil
}

func sqliteFileDSN(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	return uri.String()
}

func mergeEntries(existing []archivedEntry, rows []archiveRow, legacyProfile, hostname string) ([]archivedEntry, int) {
	entries := append([]archivedEntry(nil), existing...)
	indices := make(map[string]int, len(entries)+len(rows))
	for i, entry := range entries {
		if _, ok := entry["hostname"]; !ok {
			entry["hostname"] = mustJSON(hostname)
		}
		profile := legacyProfile
		if rawProfile, ok := entry["profile"]; ok {
			_ = json.Unmarshal(rawProfile, &profile)
		}
		indices[entryKey(profile, string(entry["id"]))] = i
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
			"hostname":    mustJSON(hostname),
			"profile":     mustJSON(row.profile),
			"url":         mustJSON(row.url),
			"title":       mustJSON(title),
			"last_visit":  mustJSON(lastVisit),
			"visit_count": mustJSON(row.visitCount),
		}
		key := entryKey(row.profile, strconv.FormatInt(row.id, 10))
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

func entryKey(profile, id string) string {
	return profile + "\x00" + id
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
