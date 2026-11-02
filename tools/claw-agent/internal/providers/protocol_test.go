package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestProviderProtocolPersistsResolvesAndControlsCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db)
	for _, api := range []model.APIProtocol{model.APIChatCompletions, model.APIResponses, model.APIAnthropic, model.APIGemini} {
		t.Run(string(api), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/custom/models" || r.Method != "GET" || r.URL.Query().Get("key") != "" {
					t.Error("wrong catalog request")
				}
				switch api {
				case model.APIAnthropic:
					if r.Header.Get("x-api-key") != "fixture" || r.Header.Get("anthropic-version") == "" {
						t.Error("Claude catalog auth")
					}
				case model.APIGemini:
					if r.Header.Get("x-goog-api-key") != "fixture" {
						t.Error("Gemini catalog auth")
					}
				default:
					if r.Header.Get("Authorization") != "Bearer fixture" {
						t.Error("OpenAI catalog auth")
					}
				}
				if api == model.APIGemini {
					fmt.Fprint(w, `{"models":[{"name":"models/native"}]}`)
				} else {
					fmt.Fprint(w, `{"data":[{"id":"native"}]}`)
				}
			}))
			defer srv.Close()
			p, err := store.Create(ctx, protocol.ProviderCreateParams{Name: "fixture", Type: "openai-compatible", APIProtocol: api, BaseURL: srv.URL + "/custom", APIKey: "fixture", Models: []string{"manual"}})
			if err != nil {
				t.Fatal(err)
			}
			config := protocol.ModelConfig{ProviderID: p.ID, BaseURL: "https://wrong.invalid", APIProtocol: model.APIResponses, Model: "native"}
			key, err := store.Resolve(ctx, &config)
			if err != nil || key != "fixture" || config.APIProtocol != api || config.BaseURL != srv.URL+"/custom" {
				t.Fatal("saved provider did not own protocol/address", config, err)
			}
			names, err := store.FetchModels(ctx, p.ID)
			if err != nil || !reflect.DeepEqual(names, []string{"native"}) {
				t.Fatal("native catalog parsing", names, err)
			}
			bad := model.APIProtocol("unknown")
			if _, err = store.Update(ctx, protocol.ProviderUpdateParams{ID: p.ID, APIProtocol: &bad}); err == nil {
				t.Fatal("unknown protocol accepted")
			}
			saved, _ := db.GetProvider(ctx, p.ID)
			if saved.APIProtocol != api || saved.APIKey != "fixture" {
				t.Fatal("rejected update changed provider")
			}
			next := model.APIResponses
			changed, err := store.Update(ctx, protocol.ProviderUpdateParams{ID: p.ID, APIProtocol: &next})
			if err != nil || changed.APIProtocol != next || changed.BaseURL != p.BaseURL || !reflect.DeepEqual(changed.Models, p.Models) || !changed.APIKeySet {
				t.Fatal("protocol change modified unrelated fields", changed, err)
			}
		})
	}
	for _, tc := range []struct {
		api model.APIProtocol
		url string
	}{{model.APIAnthropic, "https://api.anthropic.com/v1"}, {model.APIGemini, "https://generativelanguage.googleapis.com/v1beta"}} {
		p, err := store.Create(ctx, protocol.ProviderCreateParams{Name: "native default", APIProtocol: tc.api, APIKey: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		config := protocol.ModelConfig{ProviderID: p.ID}
		_, err = store.Resolve(ctx, &config)
		if err != nil || config.BaseURL != tc.url {
			t.Fatal("protocol default base URL missing", config, err)
		}
	}
}
