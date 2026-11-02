package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestCatalogCacheRejectsChangedConfigurationSnapshot(t *testing.T) {
	for _, change := range []string{"endpoint", "key", "protocol", "delete"} {
		t.Run(change, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer original-fixture" || r.URL.Path != "/models" {
					t.Error("request did not use the captured snapshot")
				}
				close(entered)
				<-release
				w.Write([]byte(`{"data":[{"id":"stale-model","supported_parameters":[]}]}`))
			}))
			defer srv.Close()
			db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := New(db)
			ctx := context.Background()
			p, err := store.Create(ctx, protocol.ProviderCreateParams{Name: "gateway", Type: "openrouter", BaseURL: srv.URL, APIKey: "original-fixture"})
			if err != nil {
				t.Fatal(err)
			}
			sentinel := model.JSON(`{"previous":"untouched"}`)
			if err = db.UpdateProvider(ctx, p.ID, model.UpdateProviderReq{ModelMetadata: sentinel}); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				names, err := store.FetchModels(ctx, p.ID)
				if names != nil {
					t.Error("stale names returned for merge", names)
				}
				result <- err
			}()
			<-entered
			switch change {
			case "endpoint":
				next := srv.URL + "/changed"
				_, err = store.Update(ctx, protocol.ProviderUpdateParams{ID: p.ID, BaseURL: &next})
			case "key":
				next := "replacement-fixture"
				_, err = store.Update(ctx, protocol.ProviderUpdateParams{ID: p.ID, APIKey: &next})
			case "protocol":
				next := model.APIResponses
				_, err = store.Update(ctx, protocol.ProviderUpdateParams{ID: p.ID, APIProtocol: &next})
			case "delete":
				err = store.Delete(ctx, p.ID)
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err = <-result; err == nil || !strings.Contains(err.Error(), "configuration changed") {
				t.Fatal("stale catalog accepted", err)
			}
			if change != "delete" {
				saved, err := db.GetProvider(ctx, p.ID)
				if err != nil || string(saved.ModelMetadata) != string(sentinel) {
					t.Fatal("stale cache overwrote current data", err)
				}
			}
		})
	}
}
