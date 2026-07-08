// Package server implements the smelt HTTP API used by the dashboard WebUI.
// It stores programs in the history database, renders them to shell scripts on
// demand, and executes them as background subprocesses with SMELT_RUN_ID set.
package server

import (
	"net/http"
	"sync"

	"github.com/Raina-Hardik/smelt/api"
	"github.com/Raina-Hardik/smelt/internal/db"
	"github.com/rs/zerolog/log"
)

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

// Start begins serving on addr and blocks until the server stops.
func (s *Server) Start(addr string) error {
	log.Info().Str("addr", addr).Msg("smelt server listening")
	return http.ListenAndServe(addr, s.Handler())
}
