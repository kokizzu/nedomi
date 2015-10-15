package purge

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/net/context"

	"github.com/ironsmile/nedomi/config"
	"github.com/ironsmile/nedomi/contexts"
	"github.com/ironsmile/nedomi/types"
	"github.com/ironsmile/nedomi/utils/httputils"
)

// Handler is a simple handler that handles the server purge page.
type Handler struct {
	logger types.Logger
}

type purgeRequest config.StringSlice
type purgeResult map[string]bool

// RequestHandle servers the purge page.
func (ph *Handler) RequestHandle(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	//!TODO authentication
	if r.Method != "POST" {
		httputils.Error(w, http.StatusMethodNotAllowed)
		return
	}

	var pr = new(purgeRequest)
	if err := json.NewDecoder(r.Body).Decode(pr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		ph.logger.Errorf("[%p] error on parsing request %s", ph, err)
		return
	}

	var app, ok = contexts.GetApp(ctx)
	if !ok {
		httputils.Error(w, http.StatusInternalServerError)
		ph.logger.Errorf("[%p] no app in context", ph)
		return
	}
	var res, err = ph.purgeAll(app, *pr)
	if err != nil {
		httputils.Error(w, http.StatusInternalServerError)
		// previosly logged
		return
	}
	if err := json.NewEncoder(w).Encode(res); err != nil {
		ph.logger.Errorf(
			"[%p] error while encoding response %s", ph, err)
	}
}

func (ph *Handler) purgeAll(app types.App, pr purgeRequest) (purgeResult, error) {
	var pres = purgeResult(make(map[string]bool))

	for _, uString := range pr {
		var u, err = url.Parse(uString)
		if err != nil {
			continue
		}
		var location = app.GetLocationFor(u.Host, u.Path)
		if location == nil {
			ph.logger.Logf(
				"[%p] got request to purge an object (%s) that is for a not configured location",
				ph, uString)
			continue
		}

		var oid = location.NewObjectIDForURL(u)

		parts, err := location.Cache.Storage.GetAvailableParts(oid)

		if err != nil {
			if !os.IsNotExist(err) {
				ph.logger.Errorf(
					"[%p] got error while gettings parts of object '%s' - %s",
					ph, oid, err)
				return nil, err
			}
		}

		if len(parts) == 0 {
			continue
		}

		if err = location.Cache.Storage.Discard(oid); err != nil {
			if !os.IsNotExist(err) {
				ph.logger.Errorf(
					"[%p] got error while purging object '%s' - %s",
					ph, oid, err)
				return nil, err
			}
		}

		location.Cache.Algorithm.Remove(parts...)
		pres[uString] = err == nil // err is os.ErrNotExist
	}
	return pres, nil
}

// New creates and returns a ready to used ServerPurgeHandler.
func New(cfg *config.Handler, l *types.Location, next types.RequestHandler) (*Handler, error) {
	return &Handler{
		logger: l.Logger,
	}, nil
}
