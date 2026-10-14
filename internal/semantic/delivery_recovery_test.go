package semantic

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"semantic-crawler/internal/models"
)

type deliveryFunc func(context.Context, string, DeliveryPayload) (int, string, error)

func (f deliveryFunc) Send(ctx context.Context, endpoint string, p DeliveryPayload) (int, string, error) {
	return f(ctx, endpoint, p)
}

func TestDeliveryRecordsResponseAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "http failure", true: "cancel after acceptance"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store, err := OpenReviewStore(ctx, filepath.Join(t.TempDir(), "review.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Now()
			candidate, err := store.SaveCandidate(ctx, Candidate{Status: CandidateApproved, ApprovedAt: &now})
			if err != nil {
				t.Fatal(err)
			}
			status, body := http.StatusServiceUnavailable, "receiver unavailable"
			sendErr := errors.New("delivery returned HTTP 503")
			client := deliveryFunc(func(context.Context, string, DeliveryPayload) (int, string, error) {
				if canceled {
					cancel()
					return http.StatusAccepted, "accepted", nil
				}
				return status, body, sendErr
			})
			_, err = NewDeliveryService(store, client).Deliver(ctx, candidate.ID, "http://receiver.test/approved")
			if canceled && err != nil {
				t.Errorf("accepted delivery lost its audit on cancellation: %v", err)
			}
			if !canceled && !errors.Is(err, sendErr) {
				t.Errorf("lost send error: %v", err)
			}
			var rows []models.SemanticDelivery
			if err := store.db.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("want one delivery audit, got %d", len(rows))
			}
			if canceled {
				status, body = http.StatusAccepted, "accepted"
			}
			if rows[0].StatusCode != status || rows[0].ResponseBody != body {
				t.Errorf("lost receiver response: %+v", rows[0])
			}
		})
	}
}

func TestDeliveryReportsAuditFailureAlongsideSendFailure(t *testing.T) {
	ctx := context.Background()
	store, err := OpenReviewStore(ctx, filepath.Join(t.TempDir(), "review.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	candidate, err := store.SaveCandidate(ctx, Candidate{Status: CandidateApproved, ApprovedAt: &now})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("CREATE TRIGGER fail_delivery BEFORE INSERT ON semantic_deliveries BEGIN SELECT RAISE(ABORT, 'forced delivery audit failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	sendErr := errors.New("connection lost")
	client := deliveryFunc(func(context.Context, string, DeliveryPayload) (int, string, error) { return 0, "", sendErr })
	_, err = NewDeliveryService(store, client).Deliver(ctx, candidate.ID, "http://receiver.test/approved")
	if !errors.Is(err, sendErr) || !strings.Contains(err.Error(), "forced delivery audit failure") {
		t.Fatalf("must expose both send and audit failure: %v", err)
	}
}
