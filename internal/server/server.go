// Package server implements the smelt HTTP API used by the dashboard WebUI.
// It stores programs in the history database, renders them to shell scripts on
// demand, and executes them as background subprocesses with SMELT_RUN_ID set.
package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Raina-Hardik/smelt/api"
	"github.com/Raina-Hardik/smelt/internal/db"
	"github.com/rs/zerolog/log"
)

// shutdownTimeout bounds how long Start waits for in-flight requests to
// finish once ctx is cancelled before forcibly closing listeners.
const shutdownTimeout = 5 * time.Second

var _ api.ServerInterface = (*Server)(nil)

// Server is the smelt HTTP API server.
type Server struct {
	db         *db.DB
	scriptsDir string
	binary     string
	dbPath     string
	procs      sync.Map // run_id string → *os.Process
}

// New creates a Server. binary is the smelt executable path used inside
// rendered scripts. scriptsDir is where generated scripts and their log files
// are written. dbPath is the resolved --db value the server itself opened;
// it is passed through to every rendered script so the subcommands it shells
// out to (each/do/finish-run) write history to the same database the server
// reads from, rather than falling back to the default path.
func New(database *db.DB, scriptsDir, binary, dbPath string) *Server {
	return &Server{db: database, scriptsDir: scriptsDir, binary: binary, dbPath: dbPath}
}

// Handler returns the HTTP mux for the API. Routing is generated from
// api/openapi.yaml; handlers below implement api.ServerInterface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	api.HandlerWithOptions(s, api.StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
	})

	mux.HandleFunc("GET /openapi.yaml", handleSpec)
	registerDocs(mux)

	return mux
}

func handleSpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(api.SpecYAML)
}

// Start begins serving on addr and blocks until ctx is cancelled (by
// SIGINT/SIGTERM — see cmd.Execute's signal.NotifyContext) or the listener
// fails. On cancellation it shuts down gracefully, bounded by
// shutdownTimeout; background program runs it already spawned are
// independent subprocesses and keep running.
func (s *Server) Start(ctx context.Context, addr string) error {
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           guard(s.Handler(), addr),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", addr).Msg("smelt server listening")
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info().Msg("shutting down smelt server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}

// guard protects the unauthenticated API from being driven by a web page the
// operator happens to visit:
//
//   - A request carrying an Origin header whose host differs from the Host
//     header is a cross-site browser request and is refused (CSRF). CLI and
//     script clients send no Origin and are unaffected.
//   - When listening on a loopback address, the Host header must name a
//     loopback host, which defeats DNS rebinding (an attacker's domain
//     resolving to 127.0.0.1 still arrives with the attacker's Host).
//
// The API has no authentication of its own: bind it to loopback, or put it
// behind an authenticating reverse proxy.
func guard(next http.Handler, addr string) http.Handler {
	loopbackOnly := isLoopbackAddr(addr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		if loopbackOnly && !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "unexpected Host header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return isLoopbackName(host)
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return isLoopbackName(strings.Trim(host, "[]"))
}

func isLoopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
