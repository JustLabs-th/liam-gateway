package httptransport

import (
	"context"
	"net/http"
	"time"
)

func (a *Admin) Analytics(w http.ResponseWriter, r *http.Request) {
	var window, bucket int64
	switch r.URL.Query().Get("range") {
	case "", "24h":
		window, bucket = 86400, 3600
	case "7d":
		window, bucket = 7*86400, 6*3600
	case "30d":
		window, bucket = 30*86400, 86400
	default:
		http.Error(w, "range must be 24h, 7d or 30d", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := a.Store.Analytics(ctx, window, bucket, r.URL.Query().Get("model"), r.URL.Query().Get("key_id"))
	if err != nil {
		http.Error(w, "analytics unavailable", 503)
		return
	}
	respond(w, result)
}
