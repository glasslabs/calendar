//go:build wasip1

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
	_ "time/tzdata"

	"github.com/apognu/gocal"
	"github.com/glasslabs/client-go"
)

// Event contains event information.
type Event struct {
	Title    string
	Time     time.Time
	IsAllDay bool
	IsToday  bool
}

// Config is the module configuration.
type Config struct {
	Timezone  string     `json:"timezone"`
	Calendars []Calendar `json:"calendars"`

	MaxDays   int `json:"maxDays"`
	MaxEvents int `json:"maxEvents"`

	Interval time.Duration `json:"interval"`
}

// Calendar is a calendar configuration.
type Calendar struct {
	URL       string `json:"url"`
	MaxEvents int    `json:"maxEvents"`
}

// NewConfig creates a default configuration for the module.
func NewConfig() Config {
	return Config{
		MaxDays:   5,
		MaxEvents: 20,
		Interval:  30 * time.Minute,
	}
}

var (
	mod    *client.Module
	log    *client.Logger
	cfg    Config
	tz     *time.Location
	events []Event
)

func main() {
	log = client.NewLogger()

	var err error
	mod, err = client.NewModule()
	if err != nil {
		log.Error("Could not create module", "error", err.Error())
		return
	}

	cfg = NewConfig()
	if err = mod.ParseConfig(&cfg); err != nil {
		log.Error("Could not parse config", "error", err.Error())
		return
	}

	//nolint:gosmopolitan
	tz = time.Local
	if cfg.Timezone != "" {
		tz, err = time.LoadLocation(cfg.Timezone)
		if err != nil {
			log.Error("Invalid timezone", "error", err.Error())

			//nolint:gosmopolitan
			tz = time.Local
		}
	}

	log.Info("Module ready", "module", mod.Name())

	load()

	reloadInterval := 30 * time.Minute
	if cfg.Interval != 0 {
		reloadInterval = cfg.Interval
	}

	for {
		time.Sleep(reloadInterval)
		load()
	}
}

func load() {
	loaded, err := loadEvents()
	if err != nil {
		log.Error("Could not load events", "error", err.Error())
		return
	}
	events = loaded

	render()
}

func render() {
	if len(events) == 0 {
		mod.Render(client.NewVStack(
			client.NewText("No upcoming events", client.WithColor("#888888"), client.WithFontSize(18)),
		))
		return
	}

	const (
		dowSize  float32 = 14
		dateSize float32 = 20
	)

	rows := make([]*client.Row, 0, len(events))
	for _, evt := range events {
		dateStr := evt.Time.Format("2 Jan")
		if !evt.IsAllDay {
			dateStr = evt.Time.Format("2 Jan 15:04")
		}

		effectiveDowSize := dowSize
		if evt.IsToday {
			effectiveDowSize = dateSize
		}

		rows = append(rows, client.NewRow(
			client.NewColumn(client.NewText(
				evt.Time.Format("Mon "), client.WithColor("#888888"), client.WithFontSize(effectiveDowSize),
			), 0),
			client.NewColumn(client.NewText(
				dateStr, client.WithColor("#ffffff"), client.WithFontSize(dateSize),
			), 0),
			client.NewColumn(client.NewText(
				" | ", client.WithColor("#cccccc"), client.WithFontSize(dateSize),
			), 0),
			client.NewColumn(client.NewText(
				evt.Title, client.WithColor("#cccccc"), client.WithFontSize(dateSize),
			), 0),
		))
	}

	mod.Render(client.NewTable(rows, client.WithRowSpacing(8)))
}

func loadEvents() ([]Event, error) {
	now := time.Now().In(tz)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz)
	end := start.AddDate(0, 0, cfg.MaxDays+1)

	log.Info("Fetching calendar events")

	var evnts []gocal.Event
	for _, cal := range cfg.Calendars {
		e, err := loadCalendar(cal.URL, cal.MaxEvents, start, end)
		if err != nil {
			return nil, err
		}
		evnts = append(evnts, e...)
	}

	sort.Slice(evnts, func(i, j int) bool {
		return evnts[i].Start.Before(*evnts[j].Start)
	})
	if cfg.MaxEvents > 0 && len(evnts) > cfg.MaxEvents {
		evnts = evnts[:cfg.MaxEvents]
	}

	result := make([]Event, 0, len(evnts))
	for _, evnt := range evnts {
		result = append(result, Event{
			Title:    evnt.Summary,
			Time:     evnt.Start.In(tz),
			IsAllDay: isAllDayEvent(evnt),
			IsToday:  isToday(evnt.Start),
		})
	}
	return result, nil
}

func loadCalendar(url string, maxEvents int, start, end time.Time) ([]gocal.Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting calendar %q: %w", url, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetching calendar %q: %d %s", url, resp.StatusCode, string(b))
	}

	gcal := gocal.NewParser(resp.Body)
	gcal.Start = &start
	gcal.End = &end
	if err = gcal.Parse(); err != nil {
		return nil, fmt.Errorf("parsing calendar %q: %w", url, err)
	}

	e := gcal.Events
	if maxEvents > 0 && len(gcal.Events) > maxEvents {
		e = e[:maxEvents]
	}
	return e, nil
}

func isAllDayEvent(evnt gocal.Event) bool {
	if evnt.RawStart.Params["VALUE"] == "DATE" {
		return true
	}

	var s time.Time
	if evnt.Start != nil {
		s = *evnt.Start
	}

	var e time.Time
	if evnt.End != nil {
		e = *evnt.End
	}

	return e.Sub(s) == 24*time.Hour && s.Hour() == 0 && s.Minute() == 0
}

func isToday(t *time.Time) bool {
	if t == nil {
		return false
	}
	lt := t.In(tz)
	now := time.Now().In(tz)
	return lt.Year() == now.Year() && lt.Month() == now.Month() && lt.Day() == now.Day()
}
