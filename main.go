package main

import (
	"embed"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"text/template"
	"time"
)

//go:embed index.html
//go:embed htmx.min.js
var embedFS embed.FS

const (
	severityTrace              = "Trace"
	severityLevelTrace         = 0
	severityInfo               = "Info"
	severityLevelInfo          = 1
	severityWarning            = "Warning"
	severityLevelWarning       = 2
	severityError              = "Error"
	severityLevelError         = 3
	severityLevelInvalid       = 4
	logCashSize                = 3
	logTestTracePeriod         = 3 * time.Second
	statusUpdateMinimumSeconds = 1
	statusUpdateMaximumSeconds = 60
	port                       = 8000
)

type Status struct {
	Date              string
	Time              string
	Uptime            string
	StatusUpdateCount int
	ConfigUpdateCount int
	LogCount          int
}

type Config struct {
	StatusUpdatePeriod       time.Duration
	StatusUpdatePeriodString string
	StatusUpdatePeriodError  string
	LogSeverity              string
}

type ConfigUpdate struct {
	Values       map[string][]string
	errorChannel chan error
}

type Log struct {
	Time       string
	Severity   string
	Message    string
	SequenceNr int
}

type Logs struct {
	LoadNext int
	Entries  []Log
}

type Model struct {
	Status Status
	Config Config
	Logs   Logs
}

type Controller struct {
	statusPeriod chan time.Duration
	modelRead    chan Model
	statusWrite  chan Status
	configWrite  chan ConfigUpdate
	configReset  chan struct{}
	logWrite     chan Log
}

func SeverityLevel(s string) int {
	switch s {
	case severityTrace:
		return severityLevelTrace
	case severityInfo:
		return severityLevelInfo
	case severityWarning:
		return severityLevelWarning
	case severityError:
		return severityLevelError
	}
	return severityLevelInvalid
}

func (cfg *Config) Update(u map[string][]string) error {
	logSeverity := u["LogSeverity"]
	statusUpdatePeriod := u["StatusUpdatePeriod"]
	if len(logSeverity) < 1 || len(statusUpdatePeriod) < 1 {
		return errors.New("Missing Entries")
	}
	cfg.LogSeverity = html.EscapeString(logSeverity[0])
	if SeverityLevel(cfg.LogSeverity) == severityLevelInvalid {
		return errors.New(fmt.Sprintf("Invalid severity got %s ", cfg.LogSeverity))
	}
	cfg.StatusUpdatePeriodString = html.EscapeString(statusUpdatePeriod[0])
	update, err := strconv.Atoi(cfg.StatusUpdatePeriodString)
	if err != nil {
		cfg.StatusUpdatePeriodError =
			fmt.Sprintf("Could not interpret '%s' as integer", cfg.StatusUpdatePeriodString)
		return errors.New(cfg.StatusUpdatePeriodError)
	} else {
		cfg.StatusUpdatePeriodError = ""
	}
	if update < statusUpdateMinimumSeconds || update > statusUpdateMaximumSeconds {
		cfg.StatusUpdatePeriodError =
			fmt.Sprintf("StatusUpdatePeriod must be in range %d - %d s. Got %d",
				statusUpdateMinimumSeconds, statusUpdateMaximumSeconds, update)
		return errors.New(cfg.StatusUpdatePeriodError)
	} else {
		cfg.StatusUpdatePeriod = time.Duration(update) * time.Second
	}
	return nil
}

func (cfg *Config) Reset() {
	rate := cfg.StatusUpdatePeriod
	severity := cfg.LogSeverity
	*cfg = Config{rate, fmt.Sprintf("%d", int(rate.Seconds())), "", severity}
}

func (m *Model) filterLogsUpTo(CurrentLoad int) {
	logEntriesUnfiltered := m.Logs.Entries
	logEntries := make([]Log, 0)
	LoadNext := CurrentLoad - 1
	for _, l := range logEntriesUnfiltered {
		if l.SequenceNr >= CurrentLoad {
			logEntries = append(logEntries, l)
			if l.SequenceNr > LoadNext {
				LoadNext = l.SequenceNr
			}
		}
	}
	dropped := 0
	if len(logEntries) > 0 {
		dropped = logEntries[len(logEntries)-1].SequenceNr - CurrentLoad
	}
	if dropped > 0 {
		msg := fmt.Sprintf("Dropped %d Log Messages", dropped)
		l := Log{
			Time:     time.Now().Format(time.TimeOnly),
			Severity: severityWarning,
			Message:  msg,
		}
		logEntries = append(logEntries, l)
	}

	m.Logs = Logs{LoadNext + 1, logEntries}
}

func (c Controller) run() {
	s := Status{}
	cfg := Config{}
	cfg.StatusUpdatePeriod = time.Duration(time.Second)
	cfg.LogSeverity = severityTrace
	cfg.Reset()
	logs := Logs{0, []Log{}}
	for {
		select {
		case c.modelRead <- Model{
			Status: s,
			Config: cfg,
			Logs:   logs,
		}:
		case sNew := <-c.statusWrite:
			sNew.StatusUpdateCount = s.StatusUpdateCount + 1
			sNew.ConfigUpdateCount = s.ConfigUpdateCount
			sNew.LogCount = s.LogCount
			s = sNew
			c.log(severityTrace, "Status Updated")
		case <-c.configReset:
			cfg.Reset()
		case cfgUpdate := <-c.configWrite:
			err := cfg.Update(cfgUpdate.Values)
			if err != nil {
				c.log(severityError, err.Error())
			} else {
				c.log(severityInfo, "Config Updated")
				s.ConfigUpdateCount = s.ConfigUpdateCount + 1
			}
			c.statusPeriod <- cfg.StatusUpdatePeriod
			cfgUpdate.errorChannel <- err
		case l := <-c.logWrite:
			if SeverityLevel(l.Severity) >= SeverityLevel(cfg.LogSeverity) {
				l.SequenceNr = s.LogCount
				s.LogCount += 1
				newLog := []Log{l}
				cnt := len(logs.Entries)
				if cnt > logCashSize {
					cnt = logCashSize
				}
				logs.Entries = append(newLog, logs.Entries[:cnt]...)
			}
		}
	}
}

func (c Controller) setStatus(s Status) {
	c.statusWrite <- s
}

func (c Controller) setConfig(v map[string][]string) error {
	errorChannel := make(chan error)
	c.configWrite <- ConfigUpdate{v, errorChannel}
	return <-errorChannel
}

func (c Controller) resetConfig() {
	c.configReset <- struct{}{}
}

func (c Controller) getModel() Model {
	return <-c.modelRead
}

func (c Controller) log(severity string, msg string) {
	// Async, so it can be called in run() without deadlock
	go func() {
		c.logWrite <- Log{
			Time:     time.Now().Format(time.TimeOnly),
			Severity: severity,
			Message:  msg,
		}
	}()
}

func (c Controller) periodicallyUpdateStatus(d time.Duration) {
	period := d
	ticker := time.NewTicker(period)
	start := time.Now()
	for {
		select {
		case d = <-c.statusPeriod:
			if d != period {
				period = d
				ticker = time.NewTicker(period)
			}
		case <-ticker.C:
			now := time.Now()
			s := Status{
				Date:   now.Format(time.DateOnly),
				Time:   now.Format(time.TimeOnly),
				Uptime: fmt.Sprintf("%s", now.Sub(start)),
			}
			c.setStatus(s)
		}
	}
}

func (c Controller) periodicallyPutLog() {
	ticker := time.NewTicker(logTestTracePeriod)
	for {
		<-ticker.C
		c.log(severityTrace, "Some trace log for testing")
	}
}

func handle(c Controller) func(http.ResponseWriter, *http.Request) {
	tmpl, err := template.ParseFS(embedFS, "index.html")
	if err != nil {
		log.Fatal(err)
	}
	htmxLib, err := embedFS.ReadFile("htmx.min.js")
	if err != nil {
		log.Fatal(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		nextPath := r.URL.Path
		m := c.getModel()
		LoadNext := 0
		if r.URL.Path == "/htmx.min.js" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			_, err := w.Write(htmxLib)
			if err != nil {
				log.Print(err)
			}
			return
		} else if r.URL.Path == "/" && r.Method == http.MethodGet {
			// nothing special
		} else if r.URL.Path == "/status" && r.Method == http.MethodGet {
			// nothing special
		} else if r.URL.Path == "/logs" && r.Method == http.MethodGet {
			LoadNextString := html.EscapeString(r.URL.Query().Get("LoadNext"))
			var err error
			LoadNext, err = strconv.Atoi(LoadNextString)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				log.Print(err)
				return
			}
		} else if r.URL.Path == "/config" && r.Method == http.MethodGet {
			// nothing special
		} else if r.URL.Path == "/config" && r.Method == http.MethodPut {
			r.ParseForm()
			err := c.setConfig(r.Form)
			m = c.getModel()
			if err != nil {
				nextPath = "/config/edit"
			}
		} else if r.URL.Path == "/config/edit" && r.Method == http.MethodDelete {
			nextPath = "/config"
			c.resetConfig()
		} else if r.URL.Path == "/config/edit" && r.Method == http.MethodGet {
			// nothing special
		} else {
			w.WriteHeader(http.StatusNotFound)
			errorMsg := fmt.Sprintf("Unsupported request with Method '%s' to URL '%s'", r.Method, r.URL.Path)
			log.Print(errorMsg)
			c.log(severityError, errorMsg)
			return
		}
		m.filterLogsUpTo(LoadNext)
		err := tmpl.ExecuteTemplate(w, nextPath, m)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Print(err)
		}
	}
}

func main() {
	c := Controller{
		statusPeriod: make(chan time.Duration),
		modelRead:    make(chan Model),
		statusWrite:  make(chan Status),
		configWrite:  make(chan ConfigUpdate),
		configReset:  make(chan struct{}),
		logWrite:     make(chan Log),
	}
	go c.run()
	go c.periodicallyUpdateStatus(c.getModel().Config.StatusUpdatePeriod)
	go c.periodicallyPutLog()
	http.HandleFunc("/", handle(c))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}
