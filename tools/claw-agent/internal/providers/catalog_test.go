package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestCustomCatalogFetchNeverFallsBackOrMutatesConfiguredModels(t *testing.T) {
	body := `{"data":[{"id":" custom "},{"id":"custom"},{"id":""},{"id":"other"}]}`
	status := 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer upstream.Close()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db)
	p, err := store.Create(context.Background(), protocol.ProviderCreateParams{Name: "custom", BaseURL: upstream.URL, APIKey: "test", Models: []string{"manual#vision@8192"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.FetchModels(context.Background(), p.ID)
	if err != nil || !reflect.DeepEqual(got, []string{"custom", "other"}) {
		t.Fatalf("models=%v error=%v", got, err)
	}
	for _, invalid := range []string{`{}`, `{"data":null}`, `{"error":{"message":"failed"}}`, `not JSON`} {
		body = invalid
		if got, err := store.FetchModels(context.Background(), p.ID); err == nil || got != nil {
			t.Fatalf("invalid catalog reused models: %v, %v", got, err)
		}
	}
	status = 503
	if got, err := store.FetchModels(context.Background(), p.ID); err == nil || got != nil {
		t.Fatalf("failed refresh reused models: %v, %v", got, err)
	}
	status, body = 200, `{"data":[]}`
	if got, err := store.FetchModels(context.Background(), p.ID); err != nil || len(got) != 0 {
		t.Fatalf("empty catalog must stay empty: %v, %v", got, err)
	}
	saved, err := store.List(context.Background())
	if err != nil || len(saved) != 1 || !reflect.DeepEqual(saved[0].Models, p.Models) {
		t.Fatalf("configured catalog changed: %v, %v", saved, err)
	}
}
