package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sort"
	"time"
	_ "time/tzdata"

	"github.com/apognu/gocal"
	"github.com/glasslabs/client-go"
)

var (
	//go:embed assets/style.css
	css []byte

	//go:embed assets/index.html
	html []byte
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
func NewConfig() Config {
	return Config{
		MaxDays:   5,
		MaxEvents: 20,
		Interval:  30 * time.Minute,
	}
}

func main() {
	log := client.NewLogger()
	mod, err := client.NewModule()
	if err != nil {
		log.Error("Could not create module", "error", err.Error())
		return
	}

	cfg := NewConfig()
	if err = mod.ParseConfig(&cfg); err != nil {
		log.Error("Could not parse config", "error", err.Error())
		return
	}

	log.Info("Loading Module", "module", mod.Name())

	m := &Module{
		mod: mod,
		cfg: cfg,
		log: log,
	}

	if err = m.setup(); err != nil {
		log.Error("Could not setup module", "error", err.Error())
		return
	}

	m.load()
	m.render()

	evntTicker := time.NewTicker(cfg.Interval)
	defer evntTicker.Stop()

	rndrTicker := time.NewTicker(time.Minute)
	defer rndrTicker.Stop()

	for {
		select {
		case <-evntTicker.C:
			m.load()
		case <-rndrTicker.C:
			m.render()
		}
	}
}

// Module is a calendar module.
type Module struct {
	mod *client.Module
	cfg Config

	tmpl *template.Template
	tz   *time.Location

	events []Event

	log *client.Logger
}

func (m *Module) setup() error {
	tmpl, err := template.New("html").Parse(string(html))
	if err != nil {
		return fmt.Errorf("parsing html: %w", err)
	}
	m.tmpl = tmpl

	//nolint:gosmopolitan
	m.tz = time.Local
	if m.cfg.Timezone != "" {
		tz, err := time.LoadLocation(m.cfg.Timezone)
		if err != nil {
			return fmt.Errorf("parsing timezone: %w", err)
		}
		m.tz = tz
	}

	if err = m.mod.LoadCSS(string(css)); err != nil {
		return fmt.Errorf("loading css: %w", err)
	}
	return nil
}

func (m *Module) load() {
	events, err := m.loadEvents()
	if err != nil {
		m.log.Error("Could not load events", "error", err.Error())
		return
	}
	m.events = events
}

func (m *Module) render() {
	var buf bytes.Buffer
	if err := m.tmpl.Execute(&buf, map[string]any{"Events": m.events}); err != nil {
		m.log.Error("Could not render HTML", "error", err.Error())
		return
	}
	m.mod.Element().SetInnerHTML(buf.String())
}

func (m *Module) loadEvents() ([]Event, error) {
	start := time.Now()
	end := time.Now().Add(time.Duration(m.cfg.MaxDays) * 24 * time.Hour)

	m.log.Info("Fetching events data", "module", "calendar", "id", m.mod.Name())

	var evnts []gocal.Event
	for _, cal := range m.cfg.Calendars {
		e, err := loadCalendar(cal.URL, cal.MaxEvents, start, end)
		if err != nil {
			return nil, err
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

func loadCalendar(url string, maxEvents int, start, end time.Time) ([]gocal.Event, error) {
	//nolint:noctx
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
