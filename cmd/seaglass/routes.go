package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"seaglass/broadcaster"
	"seaglass/web"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
)

const ReloadTriggerEndpoint = "hot"

func setupMux(mux *http.ServeMux) error {

	setupHotReload(mux)

	staticDir := os.DirFS(web.StaticDirectoryPath)
	staticFileServer := http.StripPrefix(web.StaticURLPrefix, http.FileServerFS(staticDir))

	mux.HandleFunc("GET "+web.StaticURLPrefix, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		staticFileServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if path.Ext(name) == "" {
			name += ".html"
		}
		if path.Ext(name) != ".html" || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}

		doc, err := fs.ReadFile(staticDir, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		sum := xxhash.Sum64(doc)
		w.Header().Set("ETag", fmt.Sprintf(`"%08x"`, sum))
		w.Header().Set("Cache-Control", "no-cache")
		// zero modtime omits Last-Modified, leaving ETag as the sole validator
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(doc))
	})

	return nil
}

func setupHotReload(mux *http.ServeMux) {
	broadcast := broadcaster.New()

	// handle server restarts
	serverID := strconv.FormatInt(time.Now().UnixNano(), 10)

	mux.HandleFunc("GET /reload", func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		if r.ProtoMajor == 1 {
			w.Header().Set("Connection", "keep-alive")
		}

		// flush headers so the browser opens the stream immediately
		if err := rc.Flush(); err != nil {
			return
		}

		// send writes one SSE frame and returns whether the client is still there
		send := func(frame string) bool {
			if _, err := fmt.Fprint(w, frame); err != nil {
				return false
			}
			return rc.Flush() == nil
		}

		if last := r.Header.Get("Last-Event-ID"); last != "" && last != serverID {
			send("id: " + serverID + "\ndata:reload\n\n")
			return
		}

		if !send("id: " + serverID + "\n\n") {
			return
		}

		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()

		for {
			wait := broadcast.Listen()

			select {
			case <-r.Context().Done():
				slog.Debug("client disconnected")
				return
			case <-heartbeat.C:
				// SSE comments (": ...") keep idle connections alive
				// also surfaces dead peers via failed write
				if !send(": ping\n\n") {
					return
				}
			case <-wait:
				if !send("data: reload\n\n") {
					return
				}
			}
		}
	})

	mux.HandleFunc("GET /"+ReloadTriggerEndpoint, func(w http.ResponseWriter, r *http.Request) {
		broadcast.Broadcast()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}
