package lethality

import (
	"context"
	"errors"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// RecordIndicator stores a biological-indicator culture result for a cycle.
func (s *Service) RecordIndicator(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, b domain.BiologicalIndicator) error {
	if b.Result != domain.IndicatorNegative && b.Result != domain.IndicatorPositive {
		return domain.NewError(domain.CodeInvalidIndicator, "", false, "indicator result must be negative or positive")
	}
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		return tx.InsertIndicator(ctx, cycleID, generation, b)
	})
}

// Review records one reviewer's independent conclusion. A reviewer must be
// qualified; two different qualified reviewers are required for release.
func (s *Service) Review(ctx context.Context, cycleID domain.CycleID, r domain.Review) error {
	if r.ReviewerID == "" {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "reviewer id is empty")
	}
	if !r.Qualified {
		return domain.NewError(domain.CodeUnqualifiedReviewer, "", false, "reviewer is not qualified")
	}
	if r.Conclusion != domain.ReviewApprove && r.Conclusion != domain.ReviewReject {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "review conclusion must be approve or reject")
	}

	generation, err := s.currentGeneration(ctx, cycleID)
	if err != nil {
		return err
	}
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertReview(ctx, cycleID, generation, r); err != nil {
			if uniqueViolation(err) {
				return domain.NewError(domain.CodeReviewerConflict, "", false, "reviewer already recorded a conclusion")
			}
			return err
		}
		return nil
	})
}

// Decide commits the unique terminal decision after validating the timeline,
// lethality, biological indicators, retest closure and dual review. Only the
// first qualifying request wins the single-writer barrier.
func (s *Service) Decide(ctx context.Context, cycleID domain.CycleID, d domain.FinalDecision) error {
	if d.Decision != domain.DecisionRelease && d.Decision != domain.DecisionQuarantine && d.Decision != domain.DecisionVoid {
		return domain.NewError(domain.CodeInvalidPlan, d.OperationID, false, "unknown decision kind")
	}

	var (
		generation   domain.Generation
		validationID domain.ValidationID
		status       domain.CycleStatus
		plan         domain.ValidationPlan
		calculations []domain.CalculationResult
		indicators   []domain.BiologicalIndicator
		reviews      []domain.Review
		deviations   []domain.DeviationCase
	)
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		snap, err := tx.GetSnapshot(ctx, cycleID)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "cycle not found")
		}
		if err != nil {
			return err
		}
		generation = snap.Generation
		validationID = snap.ValidationID
		status = snap.Status

		plan, err = tx.GetPlan(ctx, validationID)
		if err != nil {
			return err
		}
		calculations, err = tx.ListCalculations(ctx, cycleID, generation)
		if err != nil {
			return err
		}
		indicators, err = tx.GetIndicators(ctx, cycleID, generation)
		if err != nil {
			return err
		}
		reviews, err = tx.ListReviews(ctx, cycleID, generation)
		if err != nil {
			return err
		}
		deviations, err = tx.ListDeviations(ctx, cycleID)
		return err
	})
	if err != nil {
		return err
	}

	// An open retest blocks release before any other prerequisite is
	// evaluated: a deviation promotes the cycle to a new retest generation,
	// after which the prior generation's calculations no longer reflect the
	// active state. Retest closure must therefore be established first, so
	// that an open retest is reported as RETEST_OPEN rather than being masked
	// by the incomplete-timeline or missing-sample checks.
	if err := validateRetestClosed(deviations); err != nil {
		return err.WithOperation(d.OperationID)
	}
	if status != domain.CycleComplete {
		return domain.NewError(domain.CodeInvalidState, d.OperationID, false, "cycle timeline is not complete")
	}
	if err := validateLethality(plan, calculations); err != nil {
		return err.WithOperation(d.OperationID)
	}
	if err := validateIndicators(indicators); err != nil {
		return err.WithOperation(d.OperationID)
	}
	if err := validateReviews(reviews); err != nil {
		return err.WithOperation(d.OperationID)
	}

	d.CycleID = cycleID
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.CommitFinal(ctx, d); err != nil {
			if uniqueViolation(err) {
				return domain.NewError(domain.CodeFinalConflict, d.OperationID, false, "a final decision already exists")
			}
			return err
		}
		return nil
	})
}

// Final returns the terminal decision for a cycle, or NOT_FOUND.
func (s *Service) Final(ctx context.Context, cycleID domain.CycleID) (domain.FinalDecision, error) {
	var out domain.FinalDecision
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.GetFinal(ctx, cycleID)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return out, domain.NewError(domain.CodeNotFound, "", false, "no final decision")
	}
	return out, err
}

// validateLethality requires every calculated position to meet the plan's
// lethality threshold.
func validateLethality(plan domain.ValidationPlan, results []domain.CalculationResult) *domain.Error {
	if len(results) == 0 {
		return domain.NewError(domain.CodeMissingSample, "", false, "no calculation results")
	}
	for _, r := range results {
		if r.Lethality < plan.LethalityThreshold {
			return domain.NewError(domain.CodeInsufficientLethality, "", false, "position "+string(r.PositionID)+" lethality below threshold")
		}
	}
	return nil
}

// validateIndicators requires every biological indicator to be negative.
func validateIndicators(indicators []domain.BiologicalIndicator) *domain.Error {
	if len(indicators) == 0 {
		return domain.NewError(domain.CodeInvalidIndicator, "", false, "no biological indicator results")
	}
	for _, b := range indicators {
		if b.Result != domain.IndicatorNegative {
			return domain.NewError(domain.CodeInvalidIndicator, "", false, "indicator "+string(b.ID)+" is not negative")
		}
	}
	return nil
}

// validateRetestClosed requires no open deviation cases.
func validateRetestClosed(deviations []domain.DeviationCase) *domain.Error {
	for _, d := range deviations {
		if d.Status == domain.DeviationOpen {
			return domain.NewError(domain.CodeRetestOpen, "", false, "retest for deviation "+string(d.ID)+" is still open")
		}
	}
	return nil
}

// validateReviews requires two different qualified reviewers who both approve.
func validateReviews(reviews []domain.Review) *domain.Error {
	if len(reviews) < 2 {
		return domain.NewError(domain.CodeReviewerConflict, "", false, "fewer than two reviewers")
	}
	seen := make(map[string]bool, len(reviews))
	for _, r := range reviews {
		if !r.Qualified {
			return domain.NewError(domain.CodeUnqualifiedReviewer, "", false, "reviewer "+r.ReviewerID+" is not qualified")
		}
		if seen[r.ReviewerID] {
			return domain.NewError(domain.CodeReviewerConflict, "", false, "reviewer "+r.ReviewerID+" reviewed twice")
		}
		seen[r.ReviewerID] = true
		if r.Conclusion != domain.ReviewApprove {
			return domain.NewError(domain.CodeReviewerConflict, "", false, "reviewer "+r.ReviewerID+" did not approve")
		}
	}
	return nil
}
