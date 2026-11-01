//go:build ignore

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func testViewer() viewerModel {
	input := textinput.New()
	input.Focus()
	return viewerModel{
		filter:         input,
		hostnameFilter: textinput.New(),
		browserFilter:  textinput.New(),
		entries: []historyEntry{
			{url: "https://example.com/shared", hostname: "X280", browser: "chrome", visitCount: 2, visitedAt: time.Unix(30, 0)},
			{url: "https://example.com/shared", hostname: "B570", browser: "brave", visitCount: 10, visitedAt: time.Unix(20, 0)},
			{url: "https://example.com/other", hostname: "B570", browser: "chrome", visitCount: 5, visitedAt: time.Unix(10, 0)},
		},
	}
}

func TestMetadataFiltersAndDeduplication(t *testing.T) {
	model := testViewer()
	model.refreshMatches()
	if len(model.matches) != 2 || model.matches[0].hostname != "X280" {
		t.Fatal("unfiltered list must retain newest occurrence of each URL")
	}
	model.hostnameFilter.SetValue("b570")
	model.refreshMatches()
	if len(model.matches) != 2 || model.matches[0].browser != "brave" {
		t.Fatal("hostname filter must recover older matching occurrence and sort by count")
	}
	model.browserFilter.SetValue("CHROME")
	model.filter.SetValue("other")
	model.refreshMatches()
	if len(model.matches) != 1 || model.matches[0].url != "https://example.com/other" {
		t.Fatal("URL, hostname, and browser filters must combine case-insensitively")
	}
	model.browserFilter.SetValue("firefox")
	model.refreshMatches()
	if len(model.matches) != 0 {
		t.Fatal("nonmatching browser must yield no results")
	}
}

func TestFilterFocusAndClearing(t *testing.T) {
	model := testViewer()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(viewerModel)
	if model.activeFilter != 1 || !model.hostnameFilter.Focused() || model.filter.Focused() {
		t.Fatal("Tab must focus only the hostname input")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("B570")})
	model = updated.(viewerModel)
	if model.hostnameFilter.Value() != "B570" || model.filter.Value() != "" {
		t.Fatal("typing must update only the focused field")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(viewerModel)
	if model.activeFilter != 0 {
		t.Fatal("Shift-Tab must return to URL input")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(viewerModel)
	if model.hostnameFilter.Value() != "" || len(model.matches) != 2 {
		t.Fatal("Escape on an empty field must clear remaining filters")
	}
}

func TestFilterLayoutAndMouseSelection(t *testing.T) {
	model := testViewer()
	model.refreshMatches()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(viewerModel)
	view := model.View()
	if len(strings.Split(view, "\n")) > 24 {
		t.Fatal("view exceeds terminal height")
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 100 {
			t.Fatal("view exceeds terminal width")
		}
	}
	updated, _ = model.Update(tea.MouseMsg{X: 10, Y: 8, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = updated.(viewerModel)
	if model.selected != 1 {
		t.Fatal("mouse must select the second row below the new filter fields")
	}
}
