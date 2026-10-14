package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"semantic-crawler/internal/models"
	"testing"
)

func TestDocumentDeliveryMatchesExportAndAuditsWireHash(t *testing.T) {
	store, source := newExportStore(t)
	ctx := context.Background()
	var wire []byte
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wire, _ = io.ReadAll(r.Body); w.WriteHeader(202) }))
	defer receiver.Close()
	service := NewRestrictedDeliveryService(store, NewHTTPDeliveryClient(nil), []string{receiver.URL}).WithDocumentSource(source)
	result, err := service.DeliverWithSchema(ctx, 1, receiver.URL, "document-v1")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := store.ExportDocuments(ctx, source, ExportOptions{CandidateID: 1})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(docs[0])
	if string(wire) != string(want) {
		t.Fatalf("different export/delivery: %s / %s", wire, want)
	}
	hash := sha256.Sum256(wire)
	if result.PayloadHash != hex.EncodeToString(hash[:]) {
		t.Fatal("wrong hash")
	}
	var record models.SemanticDelivery
	if err := store.db.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.SchemaVersion != DocumentSchemaVersion || record.PayloadHash != result.PayloadHash {
		t.Fatal("schema/hash audit missing")
	}
	wire = nil
	if _, err = service.DeliverWithSchema(ctx, 1, receiver.URL, "unknown"); err == nil || wire != nil {
		t.Fatal("invalid schema delivered")
	}
	candidate, _ := store.Candidate(ctx, 1)
	candidate.Status = CandidatePendingReview
	store.SaveCandidate(ctx, candidate)
	if _, err = service.DeliverWithSchema(ctx, 1, receiver.URL, "document-v1"); err == nil || wire != nil {
		t.Fatal("draft delivered")
	}
}
