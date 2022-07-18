package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/nkcr/hodor/deployer"
	"github.com/rs/zerolog"

	"github.com/narqo/go-badge"
)

// request is the expected input from a hook request
type request struct {
	BrowserDownloadURL string `json:"browser_download_url"`
	Tag                string `json:"tag"`
}

// HTTP defines the primitives expected from a basic HTTP server
type HTTP interface {
	Start() error
	Stop()
	GetAddr() net.Addr
}

type key int

const requestIDKey key = 0

// NewHookHTTP returns a new initialized HTTP server that responds to hooks.
func NewHookHTTP(addr string, deployer deployer.Deployer, logger zerolog.Logger) HTTP {

	logger = logger.With().Str("role", "http").Logger()
	logger.Info().Msg("Server is starting...")

	nextRequestID := func() string {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	mux := http.NewServeMux()

	// POST /api/hook/:releaseID
	mux.HandleFunc("/api/hook/", getHookHandler(deployer))
	// GET /api/status/:jobID
	mux.HandleFunc("/api/status/", getStatusHandler(deployer))
	// GET /api/tags/:releaseID
	mux.HandleFunc("/api/tags/", getTagsHandler(deployer))

	server := &http.Server{
		Addr:         addr,
		Handler:      tracing(nextRequestID)(logging(logger)(mux)),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	return &HookHTTP{
		logger: logger,
		server: server,
		quit:   make(chan struct{}),
	}
}

// HookHTTP implements an HTTP server that responds to release deployment hook
// requests.
//
// - implements server.HTTP
type HookHTTP struct {
	logger zerolog.Logger
	server *http.Server
	quit   chan struct{}
	ln     net.Listener
}

// Start implements server.HTTP
func (n *HookHTTP) Start() error {
	ln, err := net.Listen("tcp", n.server.Addr)
	if err != nil {
		return fmt.Errorf("failed to create conn '%s': %v", n.server.Addr, err)
	}

	n.ln = ln

	done := make(chan bool)

	go func() {
		<-n.quit
		n.logger.Info().Msg("Server is shutting down...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		n.server.SetKeepAlivesEnabled(false)

		err := n.server.Shutdown(ctx)
		if err != nil {
			n.logger.Err(err).Msg("Could not gracefully shutdown the server")
		}
		close(done)
	}()

	n.logger.Info().Msgf("Server is ready to handle requests at %s", ln.Addr().String())

	err = n.server.Serve(ln)
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to listen on %s: %v", ln.Addr().String(), err)
	}

	<-done
	n.logger.Info().Msg("Server stopped")

	return nil
}

// Stop implements server.HTTP
func (n HookHTTP) Stop() {
	n.logger.Info().Msg("stopping")
	// we don't close it so it can be called multiple times without harm
	select {
	case n.quit <- struct{}{}:
	default:
	}
}

// GetAddr implements server
func (n HookHTTP) GetAddr() net.Addr {
	if n.ln == nil {
		return nil
	}

	return n.ln.Addr()
}

// getHookHandler returns an HTTP handler that responds to POST action to deploy
// a release. The call is blocking until the release has been deployed. The last
// part of the URL must be the releaseID.
func getHookHandler(deployer deployer.Deployer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Access-Control-Allow-Origin", "*")

		if r.Method != http.MethodPost {
			http.Error(w, "wrong action", http.StatusForbidden)
			return
		}

		key := path.Base(r.URL.Path)

		var req request
		decoder := json.NewDecoder(r.Body)

		err := decoder.Decode(&req)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to decode request: %v", err), http.StatusBadRequest)
			return
		}

		releaseURL, err := url.ParseRequestURI(req.BrowserDownloadURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("wrong url: %v", err), http.StatusBadRequest)
			return
		}

		jobID, err := deployer.Deploy(key, req.Tag, releaseURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to deploy: %v", err),
				http.StatusInternalServerError)
			return
		}

		w.Header().Add("Content-Type", "application/json")

		response := fmt.Sprintf("{\"jobID\":\"%s\"}", jobID)

		w.Write([]byte(response))
	}
}

// getStatusHandler return a handler that responds to GET requests to get the
// status of a job. The jobID must be the last part of the URL.
func getStatusHandler(deployer deployer.Deployer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "wrong action", http.StatusForbidden)
			return
		}

		jobID := path.Base(r.URL.Path)

		status, err := deployer.GetStatus(jobID)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get status: %v", err),
				http.StatusInternalServerError)
			return
		}

		w.Header().Add("Content-Type", "application/json")
		w.Header().Add("Access-Control-Allow-Origin", "*")

		encoder := json.NewEncoder(w)

		err = encoder.Encode(status)
		if err != nil {
			http.Error(w, fmt.Errorf("failed to encode: %v", err).Error(),
				http.StatusInternalServerError)
			return
		}
	}
}

// getTagsHandler return a handler that responds to GET requests to get the
// latest tag saved for a releaseID.
func getTagsHandler(deployer deployer.Deployer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "wrong action", http.StatusForbidden)
			return
		}

		releaseID := path.Base(r.URL.Path)

		tag, err := deployer.GetLatestTag(releaseID)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get tag: %v", err),
				http.StatusInternalServerError)
			return
		}

		w.Header().Add("Access-Control-Allow-Origin", "*")

		format := r.FormValue("format")

		switch format {
		case "svg":
			w.Header().Add("Content-Type", "image/svg+xml;charset=utf-8")
			badge.Render("Deployed", tag, badge.ColorBlue, w)
		default:
			w.Header().Add("Content-Type", "text/plain")
			w.Write([]byte(tag))
		}
	}
}

// logging is a utility function that logs the http server events
func logging(logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				requestID, ok := r.Context().Value(requestIDKey).(string)
				if !ok {
					requestID = "unknown"
				}
				logger.Info().Str("requestID", requestID).
					Str("method", r.Method).
					Str("url", r.URL.Path).
					Str("remoteAddr", r.RemoteAddr).
					Str("agent", r.UserAgent()).Msg("")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// tracing is a utility function that adds header tracing
func tracing(nextRequestID func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = nextRequestID()
			}
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			w.Header().Set("X-Request-Id", requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
