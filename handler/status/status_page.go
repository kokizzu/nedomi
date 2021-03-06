package status

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ironsmile/nedomi/config"
	"github.com/ironsmile/nedomi/contexts"
	"github.com/ironsmile/nedomi/types"
	"github.com/ironsmile/nedomi/utils"
)

// ServerStatusHandler is a simple handler that handles the server status page.
type ServerStatusHandler struct {
	tmpl *template.Template
	loc  *types.Location
}

// ServeHTTP servers the status page.
func (ssh *ServerStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID, _ := contexts.GetRequestID(r.Context())
	app, ok := contexts.GetApp(r.Context())
	if !ok {
		err := "Error: could not get the App from the context!"
		if _, writeErr := w.Write([]byte(err)); writeErr != nil {
			ssh.loc.Logger.Errorf("[%s] error while writing error to client: `%s`; Original error `%s`", reqID, writeErr, err)
		} else {
			ssh.loc.Logger.Errorf("[%s] %s", reqID, err)
		}
		return
	}

	cacheZones, ok := contexts.GetCacheZones(r.Context())
	if !ok {
		err := "Error: could not get the cache zones from the context!"
		if _, writeErr := w.Write([]byte(err)); writeErr != nil {
			ssh.loc.Logger.Errorf("[%s] error while writing error to client: `%s`; Original error `%s`", reqID, writeErr, err)
		} else {
			ssh.loc.Logger.Errorf("[%s] %s", reqID, err)
		}
		return
	}

	var stats = newStatistics(app, cacheZones)
	sort.Sort(stats.CacheZones)
	var err error
	if strings.HasSuffix(r.URL.Path, jsonSuffix) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		err = json.NewEncoder(w).Encode(stats)
	} else {
		err = ssh.tmpl.Execute(w, stats)
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if _, writeErr := w.Write([]byte(err.Error())); writeErr != nil {
			ssh.loc.Logger.Errorf("[%s] error while writing error to client: `%s`; Original error `%s`", reqID, writeErr, err)
		}
	}

	return
}

func newStatistics(app types.App, cacheZones map[string]*types.CacheZone) statisticsRoot {
	var zones = make([]zoneStat, 0, len(cacheZones))
	for _, cacheZone := range cacheZones {
		var stats = cacheZone.Algorithm.Stats()
		zones = append(zones, zoneStat{
			ID:          stats.ID(),
			Hits:        stats.Hits(),
			Requests:    stats.Requests(),
			Objects:     stats.Objects(),
			CacheHitPrc: stats.CacheHitPrc(),
			Size:        stats.Size().Bytes(),
		})
	}

	var appStats = app.Stats()
	return statisticsRoot{
		Requests:      appStats.Requests,
		Responded:     appStats.Responded,
		NotConfigured: appStats.NotConfigured,
		InFlight:      appStats.Requests - appStats.Responded - appStats.NotConfigured,
		CacheZones:    zones,
		Started:       app.Started(),
		Version:       versionFromAppVersion(app.Version()),
		CGOCalls:      uint64(runtime.NumCgoCall()),
		Goroutines:    uint64(runtime.NumGoroutine()),
	}
}

type statisticsRoot struct {
	Requests      uint64    `json:"requests"`
	Responded     uint64    `json:"responded"`
	NotConfigured uint64    `json:"not_configured"`
	InFlight      uint64    `json:"in_flight"`
	Version       version   `json:"version"`
	Started       time.Time `json:"started"`
	CacheZones    zoneStats `json:"zones"`
	CGOCalls      uint64    `json:"cgo_calls"`
	Goroutines    uint64    `json:"goroutines"`
}

type version struct {
	Dirty     bool      `json:"dirty"`
	Version   string    `json:"version"`
	GitHash   string    `json:"git_hash"`
	GitTag    string    `json:"git_tag"`
	BuildTime time.Time `json:"build_time"`
}

func versionFromAppVersion(appVersion types.AppVersion) version {
	return version{
		Dirty:     appVersion.Dirty,
		Version:   appVersion.Version,
		GitHash:   appVersion.GitHash,
		GitTag:    appVersion.GitTag,
		BuildTime: appVersion.BuildTime,
	}
}

type zoneStat struct {
	ID          string `json:"id"`
	Hits        uint64 `json:"hits"`
	Requests    uint64 `json:"requests"`
	Objects     uint64 `json:"objects"`
	CacheHitPrc string `json:"hit_percentage"`
	Size        uint64 `json:"size"`
}

// New creates and returns a ready to used ServerStatusHandler.
func New(cfg *config.Handler, l *types.Location, next http.Handler) (*ServerStatusHandler, error) {
	var s = defaultSettings
	if len(cfg.Settings) > 0 {
		if err := json.Unmarshal(cfg.Settings, &s); err != nil {
			return nil, fmt.Errorf("error while parsing settings for handler.status - %s",
				utils.ShowContextOfJSONError(err, cfg.Settings))
		}
	}

	// In case of:
	//  * the path is missing and it is relative
	//		or
	//  * the path is not a directory
	// we try to guess the project's root and use s.Path as a relative to it
	// one in hope it will match the templates' directory.
	if st, err := os.Stat(s.Path); (err != nil && err.(*os.PathError) != nil &&
		!strings.HasPrefix(s.Path, "/")) || (err == nil && !st.IsDir()) {

		projPath, err := utils.ProjectPath()
		if err == nil {
			fullPath := path.Join(projPath, s.Path)
			if st, err := os.Stat(fullPath); err == nil && st.IsDir() {
				s.Path = fullPath
			}
		}
	}

	var statusFilePath = path.Join(s.Path, "status_page.html")
	var tmpl, err = template.ParseFiles(statusFilePath)
	if err != nil {
		return nil, fmt.Errorf("error on opening %s - %s", statusFilePath, err)
	}

	return &ServerStatusHandler{
		tmpl: tmpl,
		loc:  l,
	}, nil
}

const jsonSuffix = ".json"

var defaultSettings = serverStatusHandlerSettings{
	Path: "handler/status/templates",
}

type serverStatusHandlerSettings struct {
	Path string `json:"path"`
}

type zoneStats []zoneStat

func (c zoneStats) Len() int {
	return len(c)
}

func (c zoneStats) Less(i, j int) bool {
	return strings.Compare(c[i].ID, c[j].ID) < 0
}

func (c zoneStats) Swap(i, j int) {
	c[j], (c)[i] = c[i], c[j]
}
