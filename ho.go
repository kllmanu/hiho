//go:build ignore

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type historyEntry struct {
	browser    string
	hostname   string
	visitCount int64
	title      string
	url        string
	lastVisit  string
	profile    string
	visitedAt  time.Time
}

type viewerModel struct {
	entries      []historyEntry
	matches      []historyEntry
	filter       textinput.Model
	selected     int
	width        int
	height       int
	selectedURL  string
	hasSelection bool
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7DD3FC"))
	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#164E63"))
	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8"))
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determining home directory: %w", err)
	}
	entries, err := loadHistory(filepath.Join(home, "Documents"))
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("history archives contain no entries")
	}

	filter := textinput.New()
	filter.Prompt = "Filter  "
	filter.Placeholder = "type to fuzzy search history"
	filter.Focus()

	model := viewerModel{
		entries: entries,
		matches: entries,
		filter:  filter,
	}
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("running history viewer: %w", err)
	}
	final, ok := finalModel.(viewerModel)
	if !ok {
		return errors.New("history viewer returned an unexpected model")
	}
	if final.hasSelection {
		fmt.Println(final.selectedURL)
	}
	return nil
}

func loadHistory(documentsDir string) ([]historyEntry, error) {
	paths, err := filepath.Glob(filepath.Join(documentsDir, "history_*.json"))
	if err != nil {
		return nil, fmt.Errorf("finding history archives: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no history archives found in %s", documentsDir)
	}

	var entries []historyEntry
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading history archive %s: %w", path, err)
		}
		var archived []struct {
			Title      string `json:"title"`
			URL        string `json:"url"`
			LastVisit  string `json:"last_visit"`
			VisitCount int64  `json:"visit_count"`
			Hostname   string `json:"hostname"`
			Profile    string `json:"profile"`
		}
		if err := json.Unmarshal(data, &archived); err != nil {
			return nil, fmt.Errorf("decoding history archive %s: %w", path, err)
		}

		browser := archiveBrowser(path)
		for _, item := range archived {
			if item.URL == "" {
				return nil, fmt.Errorf("history archive %s contains an entry without a URL", path)
			}
			if item.LastVisit == "" {
				return nil, fmt.Errorf("history archive %s contains an entry without a visit date", path)
			}
			if item.Title == "" {
				item.Title = "No Title"
			}
			var visitedAt time.Time
			if item.LastVisit != "Unknown" {
				visitedAt, err = time.ParseInLocation("2006-01-02 15:04:05", item.LastVisit, time.Local)
				if err != nil {
					return nil, fmt.Errorf("history archive %s has invalid visit date %q: %w", path, item.LastVisit, err)
				}
			}
			entries = append(entries, historyEntry{
				browser:    browser,
				hostname:   item.Hostname,
				visitCount: item.VisitCount,
				title:      item.Title,
				url:        item.URL,
				lastVisit:  item.LastVisit,
				profile:    item.Profile,
				visitedAt:  visitedAt,
			})
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].visitedAt.After(entries[j].visitedAt)
	})
	unique := make([]historyEntry, 0, len(entries))
	seenURLs := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, exists := seenURLs[entry.url]; exists {
			continue
		}
		seenURLs[entry.url] = struct{}{}
		unique = append(unique, entry)
	}
	return unique, nil
}

func archiveBrowser(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name = strings.TrimPrefix(name, "history_")
	browser, _, _ := strings.Cut(name, "_")
	return browser
}

func (model viewerModel) Init() tea.Cmd {
	return textinput.Blink
}

func (model viewerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width = message.Width
		model.height = message.Height
		model.filter.Width = max(1, message.Width-10)
		return model, nil

	case tea.MouseMsg:
		switch message.Button {
		case tea.MouseButtonWheelUp:
			model.selected = max(0, model.selected-3)
		case tea.MouseButtonWheelDown:
			model.selected = min(max(0, len(model.matches)-1), model.selected+3)
		case tea.MouseButtonLeft:
			listHeight := max(1, model.height-9)
			start := max(0, model.selected-listHeight+1)
			row := message.Y - 5
			index := start + row
			if message.Action == tea.MouseActionPress &&
				message.X >= 0 && message.X < model.width &&
				row >= 0 && row < listHeight && index < len(model.matches) {
				model.selected = index
			}
		}
		return model, nil

	case tea.KeyMsg:
		switch message.String() {
		case "ctrl+c":
			return model, tea.Quit
		case "esc":
			if model.filter.Value() != "" {
				model.filter.SetValue("")
				model.refreshMatches()
				return model, nil
			}
			return model, tea.Quit
		case "enter":
			if len(model.matches) > 0 {
				model.selectedURL = model.matches[model.selected].url
				model.hasSelection = true
				return model, tea.Quit
			}
			return model, nil
		case "up", "ctrl+p":
			if model.selected > 0 {
				model.selected--
			}
			return model, nil
		case "down", "ctrl+n":
			if model.selected+1 < len(model.matches) {
				model.selected++
			}
			return model, nil
		case "pgup":
			model.selected = max(0, model.selected-10)
			return model, nil
		case "pgdown":
			model.selected = min(max(0, len(model.matches)-1), model.selected+10)
			return model, nil
		}
	}

	oldQuery := model.filter.Value()
	var command tea.Cmd
	model.filter, command = model.filter.Update(message)
	if model.filter.Value() != oldQuery {
		model.refreshMatches()
	}
	return model, command
}

func (model *viewerModel) refreshMatches() {
	model.matches = filterHistory(model.entries, model.filter.Value())
	model.selected = 0
}

func (model viewerModel) View() string {
	if model.width == 0 || model.height == 0 {
		return "Loading history…"
	}

	width := max(1, model.width)
	listHeight := max(1, model.height-9)
	lines := make([]string, 0, model.height)
	lines = append(lines, titleStyle.Render(fitToWidth("Browser history", width)))
	lines = append(lines, fitToWidth(model.filter.View(), width))
	lines = append(lines, mutedStyle.Render(fitToWidth("↑/↓ or wheel move  ·  Click select  ·  Enter print URL  ·  Esc clear/quit  ·  Ctrl-C quit", width)))
	lines = append(lines, mutedStyle.Render(strings.Repeat("─", width)))
	dateWidth, browserWidth, hostnameWidth, countWidth, urlWidth, gapWidth := historyColumnWidths(width)
	header := formatHistoryColumns("Date", "Browser", "Host", "Count", "URL", dateWidth, browserWidth, hostnameWidth, countWidth, urlWidth, gapWidth)
	lines = append(lines, mutedStyle.Render(header))

	start := max(0, model.selected-listHeight+1)
	end := min(len(model.matches), start+listHeight)
	for index := start; index < end; index++ {
		entry := model.matches[index]
		line := formatHistoryColumns(
			cleanDisplay(entry.lastVisit),
			cleanDisplay(entry.browser),
			cleanDisplay(entry.hostname),
			fmt.Sprintf("%d", entry.visitCount),
			cleanDisplay(entry.url),
			dateWidth,
			browserWidth,
			hostnameWidth,
			countWidth,
			urlWidth,
			gapWidth,
		)
		if index == model.selected {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	for len(lines) < 5+listHeight {
		lines = append(lines, "")
	}

	lines = append(lines, mutedStyle.Render(strings.Repeat("─", width)))
	if len(model.matches) == 0 {
		lines = append(lines, "No matching history entries")
	} else {
		entry := model.matches[model.selected]
		lines = append(lines, fitToWidth(cleanDisplay(entry.title), width))
		lines = append(lines, fitToWidth(cleanDisplay(entry.url), width))
	}

	status := fmt.Sprintf("%d matching entries", len(model.matches))
	if len(model.matches) > 0 {
		status += fmt.Sprintf("  ·  %d of %d", model.selected+1, len(model.matches))
	}
	lines = append(lines, mutedStyle.Render(fitToWidth(status, width)))

	if len(lines) > model.height {
		lines = lines[:model.height]
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func historyColumnWidths(width int) (date, browser, hostname, count, url, gap int) {
	date = min(19, max(1, width/4))
	browser = min(8, max(1, width/8))
	count = min(7, max(3, width/12))
	gap = min(2, max(0, width-date-browser-count)/4)
	available := max(0, width-date-browser-count-gap*4)
	hostname = min(5, available)
	url = max(0, available-hostname)
	return date, browser, hostname, count, url, gap
}

func formatHistoryColumns(date, browser, hostname, count, url string, dateWidth, browserWidth, hostnameWidth, countWidth, urlWidth, gapWidth int) string {
	columns := []struct {
		value string
		width int
	}{
		{date, dateWidth},
		{browser, browserWidth},
		{hostname, hostnameWidth},
		{count, countWidth},
		{url, urlWidth},
	}
	var line strings.Builder
	for i, column := range columns {
		if i > 0 {
			line.WriteString(strings.Repeat(" ", gapWidth))
		}
		value := fitToWidth(column.value, column.width)
		line.WriteString(value)
		line.WriteString(strings.Repeat(" ", max(0, column.width-lipgloss.Width(value))))
	}
	return line.String()
}

func filterHistory(entries []historyEntry, query string) []historyEntry {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return entries
	}

	urls := make([]string, len(entries))
	for i, entry := range entries {
		urls[i] = entry.url
	}

	type rankedMatch struct {
		index int
		score int
	}
	ranked := make(map[int]int)
	for termIndex, term := range terms {
		found := make(map[int]int)
		for _, match := range fuzzy.Find(term, urls) {
			found[match.Index] = match.Score
		}

		literalMatches := make(map[int]bool)
		normalizedTerm := strings.ToLower(term)
		for index, url := range urls {
			if termIndex > 0 {
				if _, exists := ranked[index]; !exists {
					continue
				}
			}
			if strings.Contains(strings.ToLower(url), normalizedTerm) {
				literalMatches[index] = true
			}
		}

		nextRanked := make(map[int]int)
		for index, score := range found {
			if termIndex > 0 {
				if previousScore, exists := ranked[index]; exists {
					score += previousScore
				} else {
					continue
				}
			}
			if len(literalMatches) == 0 || literalMatches[index] {
				nextRanked[index] = score
			}
		}
		ranked = nextRanked
	}

	results := make([]rankedMatch, 0, len(ranked))
	for index, score := range ranked {
		results = append(results, rankedMatch{index: index, score: score})
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := entries[results[i].index], entries[results[j].index]
		if left.visitCount != right.visitCount {
			return left.visitCount > right.visitCount
		}
		if !left.visitedAt.Equal(right.visitedAt) {
			return left.visitedAt.After(right.visitedAt)
		}
		return results[i].index < results[j].index
	})

	matches := make([]historyEntry, len(results))
	for i, result := range results {
		matches[i] = entries[result.index]
	}
	return matches
}

func cleanDisplay(value string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, value)
}

func fitToWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	var result strings.Builder
	for _, char := range value {
		if lipgloss.Width(result.String())+lipgloss.Width(string(char))+1 > width {
			break
		}
		result.WriteRune(char)
	}
	return result.String() + "…"
}
