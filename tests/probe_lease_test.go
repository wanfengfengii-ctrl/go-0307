package tests

import (
	"context"
	"sync"
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

func registerProbes(t *testing.T, st *store.Store, svc *probe.Service, probes ...domain.ProbeLineage) {
	t.Helper()
	for _, p := range probes {
		if err := svc.Register(context.Background(), p); err != nil {
			t.Fatalf("register probe %s: %v", p.ID, err)
		}
	}
}

func TestBindCertificateExpired(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := probe.NewService(st)

	p := temperatureProbe("probe-1", "batch-a")
	p.ValidUntil = 100 // expires long before the binding interval
	registerProbes(t, st, svc, p)

	err := svc.Bind(ctx, probe.BindRequest{
		OperationID: "op-bind-expired",
		ProbeID:     "probe-1",
		PositionID:  "p1",
		Generation:  1,
		ValidFrom:   0,
		ValidUntil:  1000,
		RangeMin:    121000,
		RangeMax:    122000,
	})
	assertCode(t, err, domain.CodeCertificateExpired)
}

func TestBindRangeInsufficient(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := probe.NewService(st)

	p := temperatureProbe("probe-1", "batch-a")
	p.RangeMax = 110000 // cannot reach the required 121 °C
	registerProbes(t, st, svc, p)

	err := svc.Bind(ctx, probe.BindRequest{
		OperationID: "op-bind-range",
		ProbeID:     "probe-1",
		PositionID:  "p1",
		Generation:  1,
		ValidFrom:   0,
		ValidUntil:  1000,
		RangeMin:    121000,
		RangeMax:    122000,
	})
	assertCode(t, err, domain.CodeRangeInsufficient)
}

func TestBindIdempotentAndConflict(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := probe.NewService(st)
	registerProbes(t, st, svc, temperatureProbe("probe-1", "batch-a"))

	req := probe.BindRequest{
		OperationID: "op-bind-1",
		ProbeID:     "probe-1",
		PositionID:  "p1",
		Generation:  1,
		ValidFrom:   0,
		ValidUntil:  1000,
		RangeMin:    121000,
		RangeMax:    122000,
	}
	if err := svc.Bind(ctx, req); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Identical retry is idempotent.
	if err := svc.Bind(ctx, req); err != nil {
		t.Fatalf("idempotent bind: %v", err)
	}
	// Same operation with different content conflicts.
	req.PositionID = "p2"
	err := svc.Bind(ctx, req)
	assertCode(t, err, domain.CodeIdempotencyConflict)
}

func TestConcurrentLeaseSingleWinner(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := probe.NewService(st)

	const resource = domain.ResourceID("channel:p1")
	acquire := func(op domain.OperationID, from, until domain.LogicalTime) (domain.TokenID, error) {
		return svc.Acquire(ctx, probe.LeaseRequest{
			OperationID: op,
			ResourceID:  resource,
			Generation:  1,
			ValidFrom:   from,
			ValidUntil:  until,
		})
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var token1, token2 domain.TokenID
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		token1, err1 = acquire("op-lease-1", 0, 1000)
	}()
	go func() {
		defer wg.Done()
		<-start
		token2, err2 = acquire("op-lease-2", 0, 1000)
	}()
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range []error{err1, err2} {
		if err == nil {
			successes++
		} else {
			assertCode(t, err, domain.CodeLeaseConflict)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one lease winner, got %d", successes)
	}
	if successes == 1 && token1 == "" && token2 == "" {
		t.Fatalf("winning lease has an empty token")
	}
}

func TestLeaseRenewAndRelease(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := probe.NewService(st)

	token, err := svc.Acquire(ctx, probe.LeaseRequest{
		OperationID: "op-lease-r",
		ResourceID:  "channel:p1",
		Generation:  1,
		ValidFrom:   0,
		ValidUntil:  1000,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := svc.Renew(ctx, token, 2000); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := svc.Release(ctx, token); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Releasing again fails with NOT_FOUND.
	err = svc.Release(ctx, token)
	assertCode(t, err, domain.CodeNotFound)
}
