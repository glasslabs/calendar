package calendar

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/apognu/gocal"
	"github.com/glasslabs/looking-glass/module/types"
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
	Timezone  string     `yaml:"timezone"`
	Calendars []Calendar `yaml:"calendars"`

	MaxDays   int `yaml:"maxDays"`
	MaxEvents int `yaml:"maxEvents"`

	Interval time.Duration `yaml:"interval"`
}

// Calendar is a calendar configuration.
type Calendar struct {
	URL       string `yaml:"url"`
	MaxEvents int    `yaml:"maxEvents"`
}

// NewConfig creates a default configuration for the module.
func NewConfig() *Config {
	return &Config{
		MaxDays:   5,
		MaxEvents: 20,
		Interval:  30 * time.Minute,
	}
}

// Module is a calendar module.
type Module struct {
	name string
	path string
	cfg  *Config
	ui   types.UI
	log  types.Logger

	evnts []Event
	tmpl  *template.Template
	tz    *time.Location

	done chan struct{}
}

// New returns a running calendar module.
func New(_ context.Context, cfg *Config, info types.Info, ui types.UI) (io.Closer, error) {
	html, err := os.ReadFile(filepath.Clean(filepath.Join(info.Path, "assets/index.html")))
	if err != nil {
		return nil, fmt.Errorf("calendar: could not read html: %w", err)
	}
	tmpl, err := template.New("html").Parse(string(html))
	if err != nil {
		return nil, fmt.Errorf("calendar: could not parse html: %w", err)
	}

	tz := time.Local
	if cfg.Timezone != "" {
		tz, err = time.LoadLocation(cfg.Timezone)
		if err != nil {
			return nil, fmt.Errorf("calendar: could not parse timezone: %w", err)
		}
	}

	m := &Module{
		name: info.Name,
		path: info.Path,
		cfg:  cfg,
		ui:   ui,
		log:  info.Log,
		tmpl: tmpl,
		tz:   tz,
		done: make(chan struct{}),
	}

	if err = m.loadCSS("assets/style.css"); err != nil {
		return nil, err
	}

	evnts, err := m.loadEvents()
	if err != nil {
		return nil, fmt.Errorf("calendar: %w", err)
	}
	m.evnts = evnts

	if err = m.render(); err != nil {
		return nil, err
	}

	go m.run()

	return m, nil
}

func (m *Module) run() {
	evntTicker := time.NewTicker(m.cfg.Interval)
	defer evntTicker.Stop()

	rndrTicker := time.NewTicker(time.Minute)
	defer rndrTicker.Stop()

	for {
		select {
		case <-m.done:
			return
		case <-evntTicker.C:
			evnts, err := m.loadEvents()
			if err != nil {
				m.log.Error("loading events", "module", "calendar", "error", err)
				continue
			}
			m.evnts = evnts

		case <-rndrTicker.C:
			if err := m.render(); err != nil {
				m.log.Error("could not render calendar data", "module", "calendar", "id", m.name, "error", err.Error())
			}
		}
	}
}

func (m *Module) loadCSS(path string) error {
	css, err := os.ReadFile(filepath.Clean(filepath.Join(m.path, path)))
	if err != nil {
		return fmt.Errorf("calendar: could not read css: %w", err)
	}
	return m.ui.LoadCSS(string(css))
}

func (m *Module) render() error {
	var buf bytes.Buffer
	if err := m.tmpl.Execute(&buf, map[string]interface{}{"Events": m.evnts}); err != nil {
		return fmt.Errorf("calendar: could not render html: %w", err)
	}
	return m.ui.LoadHTML(buf.String())
}

func (m *Module) loadEvents() ([]Event, error) {
	start := time.Now()
	end := time.Now().Add(time.Duration(m.cfg.MaxDays) * 24 * time.Hour)

	m.log.Info("fetching events data", "module", "calendar", "id", m.name)

	var evnts []gocal.Event
	for _, cal := range m.cfg.Calendars {
		resp, err := http.Get(cal.URL)
		if err != nil {
			return nil, fmt.Errorf("could not load calendar %q: %w", cal.URL, err)
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("could not load calendar %q", cal.URL)
		}

		gcal := gocal.NewParser(resp.Body)
		gcal.Start = &start
		gcal.End = &end
		if err = gcal.Parse(); err != nil {
			return nil, fmt.Errorf("could not load calendar %q: %w", cal.URL, err)
		}

		e := gcal.Events
		if cal.MaxEvents > 0 && len(gcal.Events) > cal.MaxEvents {
			e = e[:cal.MaxEvents]
		}
		evnts = append(evnts, e...)
	}

	sort.Slice(evnts, func(i, j int) bool {
		return evnts[i].Start.Before(*evnts[j].Start)
	})
	if m.cfg.MaxEvents > 0 && len(evnts) > m.cfg.MaxEvents {
		evnts = evnts[:m.cfg.MaxEvents]
	}

	events := make([]Event, 0, len(evnts))
	for _, evnt := range evnts {
		events = append(events, Event{
			Title:    evnt.Summary,
			Time:     evnt.Start.In(m.tz),
			IsAllDay: isAllDayEvent(evnt),
			IsToday:  isToday(evnt.Start),
		})
	}
	return events, nil
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
	if evnt.Start != nil {
		e = *evnt.End
	}

	return e.Sub(s) == 24*time.Hour && s.Hour() == 0 && s.Minute() == 0
}

func isToday(t *time.Time) bool {
	if t == nil {
		return false
	}

	return t.Truncate(24 * time.Hour).Equal(time.Now().UTC().Truncate(24 * time.Hour))
}

// Close stops and closes the module.
func (m *Module) Close() error {
	close(m.done)
	return nil
}
