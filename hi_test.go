package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindDatabasesAcrossInstallations(t *testing.T) {
	for browser, config := range browsers {
		t.Run(browser, func(t *testing.T) {
			home := t.TempDir()
			for _, root := range config.baseDirs {
				path := filepath.Join(home, root, "Default", config.database)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			found, err := findDatabases(browser, home)
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != len(config.baseDirs) {
				t.Fatalf("found %d databases, want %d", len(found), len(config.baseDirs))
			}
			if found[0].profile != "Default" {
				t.Fatal("original profile identity must remain unchanged")
			}
			seen := make(map[string]bool)
			var rows []archiveRow
			for _, database := range found {
				if seen[database.profile] {
					t.Fatalf("profile collision: %s", database.profile)
				}
				seen[database.profile] = true
				rows = append(rows, archiveRow{id: 1, profile: database.profile})
			}
			merged, _ := mergeEntries(nil, rows, found[0].profile, "test")
			if len(merged) != len(found) {
				t.Fatal("identical row IDs from different installations must not overwrite each other")
			}
		})
	}
}

func TestFindDatabasesMissingRoots(t *testing.T) {
	for browser, config := range browsers {
		t.Run(browser, func(t *testing.T) {
			home := t.TempDir()
			if _, err := findDatabases(browser, home); err == nil {
				t.Fatal("missing history must produce an error")
			}
			path := filepath.Join(home, config.baseDirs[len(config.baseDirs)-1], "Default", config.database)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			found, err := findDatabases(browser, home)
			if err != nil || len(found) != 1 {
				t.Fatalf("existing root must be found despite missing alternatives: %v", err)
			}
		})
	}
}
