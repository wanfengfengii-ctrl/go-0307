package httpapi

import (
	"net/http"
	"strconv"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/lethality"
	"lyophilizer-sterilization-validation/internal/plan"
	"lyophilizer-sterilization-validation/internal/probe"
)

// --- Validation plans ---

type lockPlanBody struct {
	OperationID domain.OperationID    `json:"operation_id"`
	Plan        domain.ValidationPlan `json:"plan"`
}

func (s *Server) handleLockPlan(w http.ResponseWriter, r *http.Request) {
	var body lockPlanBody
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	gen, err := s.plans.Lock(r.Context(), plan.LockRequest{OperationID: body.OperationID, Plan: body.Plan})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"validation_id": body.Plan.ID, "generation": gen})
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.plans.Get(r.Context(), domain.ValidationID(r.PathValue("id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.plans.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

// --- Probes ---

func (s *Server) handleRegisterProbe(w http.ResponseWriter, r *http.Request) {
	var p domain.ProbeLineage
	if err := decode(r, &p); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	if err := s.probes.Register(r.Context(), p); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleListProbes(w http.ResponseWriter, r *http.Request) {
	probes, err := s.probes.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, probes)
}

func (s *Server) handleGetProbe(w http.ResponseWriter, r *http.Request) {
	p, err := s.probes.Get(r.Context(), domain.ProbeID(r.PathValue("id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// --- Bindings and leases ---

func (s *Server) handleBindProbe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OperationID domain.OperationID `json:"operation_id"`
		ProbeID     domain.ProbeID     `json:"probe_id"`
		PositionID  domain.PositionID  `json:"position_id"`
		Generation  domain.Generation  `json:"generation"`
		ValidFrom   domain.LogicalTime `json:"valid_from"`
		ValidUntil  domain.LogicalTime `json:"valid_until"`
		RangeMin    int64              `json:"range_min"`
		RangeMax    int64              `json:"range_max"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	err := s.probes.Bind(r.Context(), probe.BindRequest{
		OperationID: body.OperationID,
		ProbeID:     body.ProbeID,
		PositionID:  body.PositionID,
		Generation:  body.Generation,
		ValidFrom:   body.ValidFrom,
		ValidUntil:  body.ValidUntil,
		RangeMin:    body.RangeMin,
		RangeMax:    body.RangeMax,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"bound": true})
}

func (s *Server) handleAcquireLease(w http.ResponseWriter, r *http.Request) {
	var body probe.LeaseRequest
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	token, err := s.probes.Acquire(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token})
}

func (s *Server) handleRenewLease(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token domain.TokenID     `json:"token"`
		Until domain.LogicalTime `json:"until"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	if err := s.probes.Renew(r.Context(), body.Token, body.Until); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"renewed": true})
}

func (s *Server) handleReleaseLease(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token domain.TokenID `json:"token"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	if err := s.probes.Release(r.Context(), body.Token); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"released": true})
}

func (s *Server) handleRecordDeviceCall(w http.ResponseWriter, r *http.Request) {
	var body cycle.DeviceCallRequest
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	call, err := s.cycles.RecordDeviceCall(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, call)
}

func (s *Server) handleListDeviceCalls(w http.ResponseWriter, r *http.Request) {
	calls, err := s.cycles.DeviceCalls(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, calls)
}

// --- Cycles ---

func (s *Server) handleStage(w http.ResponseWriter, r *http.Request) {
	var body cycle.StageRequest
	body.CycleID = domain.CycleID(r.PathValue("id"))
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	if err := s.cycles.Stage(r.Context(), body); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"phase": string(body.Phase)})
}

func (s *Server) handleSample(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OperationID        domain.OperationID `json:"operation_id"`
		ExpectedGeneration domain.Generation  `json:"expected_generation"`
		Token              domain.TokenID     `json:"token"`
		Sample             domain.Sample      `json:"sample"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	err := s.cycles.Sample(r.Context(), cycle.SampleRequest{
		OperationID:        body.OperationID,
		CycleID:            domain.CycleID(r.PathValue("id")),
		ExpectedGeneration: body.ExpectedGeneration,
		Token:              body.Token,
		Sample:             body.Sample,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"recorded": true})
}

func (s *Server) handleIndicator(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Generation domain.Generation          `json:"generation"`
		Indicator  domain.BiologicalIndicator `json:"indicator"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	err := s.lethality.RecordIndicator(r.Context(), domain.CycleID(r.PathValue("id")), body.Generation, body.Indicator)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"recorded": true})
}

func (s *Server) handleCalculate(w http.ResponseWriter, r *http.Request) {
	var body lethality.CalculateRequest
	body.CycleID = domain.CycleID(r.PathValue("id"))
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	results, err := s.lethality.Calculate(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	results, err := s.lethality.Results(r.Context(), domain.CycleID(r.PathValue("id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleOpenRetest(w http.ResponseWriter, r *http.Request) {
	var body lethality.RetestRequest
	body.CycleID = domain.CycleID(r.PathValue("id"))
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	generation, err := s.lethality.OpenRetest(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retest_generation": generation})
}

func (s *Server) handleRetests(w http.ResponseWriter, r *http.Request) {
	// Return the members of every retest generation for the cycle.
	cycleID := domain.CycleID(r.PathValue("id"))
	gen := r.URL.Query().Get("generation")
	var members []domain.RetestMember
	if gen == "" {
		// List across all retest generations is not directly supported; return
		// an empty set rather than guessing a generation.
		writeJSON(w, http.StatusOK, members)
		return
	}
	n, err := strconv.ParseInt(gen, 10, 64)
	if err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid generation"))
		return
	}
	g := domain.Generation(n)
	members, err = s.lethality.RetestMembers(r.Context(), cycleID, g)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var body domain.Review
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	if err := s.lethality.Review(r.Context(), domain.CycleID(r.PathValue("id")), body); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reviewed": true})
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var body domain.FinalDecision
	if err := decode(r, &body); err != nil {
		writeError(w, domain.NewError(domain.CodeInvalidPlan, "", false, "invalid request body"))
		return
	}
	body.CycleID = domain.CycleID(r.PathValue("id"))
	if err := s.lethality.Decide(r.Context(), body.CycleID, body); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.cycles.Audit(r.Context(), domain.CycleID(r.PathValue("id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleGetFinal(w http.ResponseWriter, r *http.Request) {
	decision, err := s.lethality.Final(r.Context(), domain.CycleID(r.PathValue("id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decision)
}
