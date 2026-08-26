// Package httpapi is the "HTTP and operations page" component. It exposes the
// JSON interfaces and the embedded frontend that drives plan editing, the
// stage timeline, the probe matrix, the sample stream, the retest set, review
// and final decision through those interfaces.
package httpapi

import (
	"io/fs"
	"log"
	"net/http"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/lethality"
	"lyophilizer-sterilization-validation/internal/plan"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
	"lyophilizer-sterilization-validation/web"
)

// Server wires every business service to the HTTP routes.
type Server struct {
	plans     *plan.Service
	probes    *probe.Service
	cycles    *cycle.Service
	lethality *lethality.Service
}

// NewServer constructs the full HTTP handler over a persistence store.
func NewServer(s *store.Store) http.Handler {
	srv := &Server{
		plans:     plan.NewService(s),
		probes:    probe.NewService(s),
		cycles:    cycle.NewService(s),
		lethality: lethality.NewService(s),
	}
	return srv.routes()
}

// NewHandler returns the handler backed by an in-memory store, used where a
// full server is needed without external files (health/version/static tests).
func NewHandler() http.Handler {
	s, err := store.Open(":memory:")
	if err != nil {
		log.Fatalf("open in-memory store: %v", err)
	}
	return NewServer(s)
}

// routes registers the JSON API and the embedded frontend.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)

	mux.HandleFunc("POST /api/v1/validations/lock", s.handleLockPlan)
	mux.HandleFunc("GET /api/v1/validations", s.handleListPlans)
	mux.HandleFunc("GET /api/v1/validations/{id}", s.handleGetPlan)
	mux.HandleFunc("POST /api/v1/validations/{id}/probes/bind", s.handleBindProbe)

	mux.HandleFunc("POST /api/v1/probes", s.handleRegisterProbe)
	mux.HandleFunc("GET /api/v1/probes", s.handleListProbes)
	mux.HandleFunc("GET /api/v1/probes/{id}", s.handleGetProbe)

	mux.HandleFunc("POST /api/v1/leases/acquire", s.handleAcquireLease)
	mux.HandleFunc("POST /api/v1/leases/renew", s.handleRenewLease)
	mux.HandleFunc("POST /api/v1/leases/release", s.handleReleaseLease)

	mux.HandleFunc("POST /api/v1/device-calls", s.handleRecordDeviceCall)
	mux.HandleFunc("GET /api/v1/device-calls", s.handleListDeviceCalls)

	mux.HandleFunc("POST /api/v1/cycles/{id}/stages", s.handleStage)
	mux.HandleFunc("POST /api/v1/cycles/{id}/samples", s.handleSample)
	mux.HandleFunc("POST /api/v1/cycles/{id}/indicators", s.handleIndicator)
	mux.HandleFunc("POST /api/v1/cycles/{id}/calculate", s.handleCalculate)
	mux.HandleFunc("GET /api/v1/cycles/{id}/results", s.handleResults)
	mux.HandleFunc("POST /api/v1/cycles/{id}/deviations", s.handleOpenRetest)
	mux.HandleFunc("GET /api/v1/cycles/{id}/retests", s.handleRetests)
	mux.HandleFunc("POST /api/v1/cycles/{id}/reviews", s.handleReview)
	mux.HandleFunc("POST /api/v1/cycles/{id}/final-decisions", s.handleDecide)
	mux.HandleFunc("GET /api/v1/cycles/{id}/final", s.handleGetFinal)
	mux.HandleFunc("GET /api/v1/cycles/{id}/audit", s.handleAudit)

	// Serve the embedded frontend at every other path, falling back to
	// index.html so the single-page UI owns routing.
	static, err := fs.Sub(web.FS, "dist")
	if err != nil {
		log.Fatalf("embedded frontend missing: %v", err)
	}
	fileServer := http.FileServer(http.FS(static))
	mux.Handle("/", fileServer)

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "lyophilizer-sterilization-validation"})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service":           "lyophilizer-sterilization-validation",
		"algorithm_version": lethality.AlgorithmVersion,
	})
}
