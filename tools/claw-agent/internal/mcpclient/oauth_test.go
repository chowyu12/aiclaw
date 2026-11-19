package mcpclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthLoginRefreshEncryptedRestartAndLogout(t *testing.T) {
	var endpoint, redirect, challenge string
	var refreshes atomic.Int32
	var toolCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q],"scopes_supported":["tools"]}`, endpoint+"/mcp", endpoint)
		case "/.well-known/oauth-authorization-server":
			fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"registration_endpoint":%q,"code_challenge_methods_supported":["S256"]}`, endpoint, endpoint+"/authorize", endpoint+"/token", endpoint+"/register")
		case "/register":
			var input struct {
				Redirects []string `json:"redirect_uris"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Redirects) != 1 {
				t.Error("registration redirect missing")
			}
			redirect = input.Redirects[0]
			fmt.Fprint(w, `{"client_id":"client"}`)
		case "/token":
			r.ParseForm()
			if r.Form.Get("resource") != endpoint+"/mcp" || r.Form.Get("client_id") != "client" {
				t.Error("missing token audience/client")
			}
			if r.Form.Get("grant_type") == "authorization_code" {
				hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("redirect_uri") != redirect || r.Form.Get("code") != "code" {
					t.Error("PKCE/callback binding mismatch")
				}
				fmt.Fprint(w, `{"access_token":"secret-access","refresh_token":"secret-refresh","token_type":"Bearer","expires_in":1}`)
			} else {
				if r.Form.Get("refresh_token") != "secret-refresh" {
					t.Error("refresh credential lost")
				}
				refreshes.Add(1)
				fmt.Fprint(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`)
			}
		case "/mcp":
			if r.Method == "GET" {
				if r.Header.Get("Authorization") == "Bearer rotated-access" {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+endpoint+`/.well-known/oauth-protected-resource/mcp"`)
				w.WriteHeader(401)
				return
			}
			if r.Header.Get("Authorization") != "Bearer rotated-access" {
				t.Error("request did not use refreshed credential")
			}
			var rpc struct {
				ID     *int   `json:"id"`
				Method string `json:"method"`
			}
			json.NewDecoder(r.Body).Decode(&rpc)
			if rpc.Method == "tools/call" {
				toolCalls.Add(1)
				w.WriteHeader(401)
				return
			}
			if rpc.ID == nil {
				w.WriteHeader(202)
				return
			}
			result := `{}`
			if rpc.Method == "tools/list" {
				result = `{"tools":[{"name":"test","inputSchema":{"type":"object"}}]}`
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *rpc.ID, result)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	endpoint = upstream.URL
	dir := t.TempDir()
	m, err := NewOAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, err := m.Begin(ctx, OAuthInput{URL: endpoint + "/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := url.Parse(started.AuthorizationURL)
	challenge = auth.Query().Get("code_challenge")
	if auth.Query().Get("resource") != endpoint+"/mcp" || auth.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("authorization missing audience/PKCE")
	}
	callback, _ := url.Parse(redirect)
	q := url.Values{"state": {"wrong"}, "code": {"code"}}
	callback.RawQuery = q.Encode()
	response, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("accepted invalid state")
	}
	q.Set("state", auth.Query().Get("state"))
	callback.RawQuery = q.Encode()
	response, err = http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("login failed: %d", response.StatusCode)
	}
	if status, err := m.Status(endpoint + "/mcp"); err != nil || status.State != "connected" {
		t.Fatalf("status=%+v %v", status, err)
	}
	body, err := os.ReadFile(m.CredentialPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "enc:v1:") || strings.Contains(string(body), "secret-access") || strings.Contains(string(body), "secret-refresh") {
		t.Fatal("credentials stored in plaintext")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := m.AccessToken(ctx, endpoint+"/mcp")
			if err != nil || token != "rotated-access" {
				t.Errorf("refresh=%q %v", token, err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("concurrent refreshes=%d", refreshes.Load())
	}
	restarted, err := NewOAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	client, err := Start(ctx, Config{URL: endpoint + "/mcp", AccessToken: func(ctx context.Context, rejected string) (string, error) {
		return restarted.Token(ctx, endpoint+"/mcp", rejected)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.CallTool(ctx, "test", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unauthorized tool did not fail")
	}
	if toolCalls.Load() != 1 {
		t.Fatal("automatically replayed unauthorized side effect")
	}
	if err := restarted.Logout(endpoint + "/mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.AccessToken(ctx, endpoint+"/mcp"); err == nil {
		t.Fatal("logout retained access")
	}
	again, err := NewOAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if status, _ := again.Status(endpoint + "/mcp"); status.State != "needs_login" {
		t.Fatal("logout not durable")
	}
}

func TestOAuthRejectsInsecureResourceAndMetadataMismatch(t *testing.T) {
	m, err := NewOAuthManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, raw := range []string{"http://example.com/mcp", "https://user:pass@example.com/mcp", "https://example.com/mcp#fragment", "https://example.com/mcp?token=secret"} {
		if _, err := m.Begin(context.Background(), OAuthInput{URL: raw}); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"resource":"https://other.example/mcp","authorization_servers":["https://auth.example"]}`)
	}))
	defer upstream.Close()
	if _, err := m.Begin(context.Background(), OAuthInput{URL: upstream.URL + "/mcp"}); err == nil {
		t.Fatal("accepted foreign resource metadata")
	}
}

func TestOAuthRefreshFailureRequiresLoginAndDoesNotExposeSecret(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(fmt.Sprintf("saveFailure=%v", failSave), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				r.ParseForm()
				if r.Form.Get("client_secret") != "private-client-secret" {
					t.Error("pre-registered client secret missing")
				}
				if !failSave {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"invalid_grant","description":"private-client-secret"}`)
					return
				}
				fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`)
			}))
			defer upstream.Close()
			m, err := NewOAuthManager(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			resource := upstream.URL + "/mcp"
			m.records[resource] = oauthRecord{Resource: resource, ClientID: "client", ClientSecret: "private-client-secret", TokenAuth: "client_secret_post", TokenEndpoint: upstream.URL, AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Unix() - 1}
			if err := m.save(); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(m.path)
			if err != nil || strings.Contains(string(body), "private-client-secret") {
				t.Fatal("client secret was not encrypted")
			}
			if failSave {
				blocked := m.path + "-directory"
				if err := os.Mkdir(blocked, 0700); err != nil {
					t.Fatal(err)
				}
				m.path = blocked
			}
			for i := 0; i < 2; i++ {
				if token, err := m.AccessToken(context.Background(), resource); err == nil || token != "" || strings.Contains(err.Error(), "private-client-secret") {
					t.Fatalf("refresh failure leaked/allowed access: %q %v", token, err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("repeated a failed refresh instead of requesting login")
			}
			if status, err := m.Status(resource); err != nil || status.State != "needs_login" {
				t.Fatalf("failure not visible: %+v %v", status, err)
			}
		})
	}
}

func TestOAuthRefreshAcrossCancellationLogoutAndRelogin(t *testing.T) {
	for _, action := range []string{"refresh", "logout", "relogin"} {
		t.Run(action, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var exchanges atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				issuer := "http://" + r.Host
				switch r.URL.Path {
				case "/mcp":
					w.WriteHeader(http.StatusUnauthorized)
					return
				case "/.well-known/oauth-protected-resource/mcp":
					fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q]}`, issuer+"/mcp", issuer)
					return
				case "/.well-known/oauth-authorization-server":
					fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"code_challenge_methods_supported":["S256"]}`, issuer, issuer+"/auth", issuer+"/token")
					return
				}
				if r.FormValue("grant_type") == "authorization_code" {
					fmt.Fprint(w, `{"access_token":"new-login","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`)
					return
				}
				if exchanges.Add(1) == 1 {
					close(started)
				}
				<-release
				fmt.Fprint(w, `{"access_token":"replacement","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`)
			}))
			defer upstream.Close()
			defer unblock()
			dir := t.TempDir()
			manager, err := NewOAuthManager(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			resource := upstream.URL + "/mcp"
			manager.records[resource] = oauthRecord{Resource: resource, TokenEndpoint: upstream.URL, AccessToken: "expired", RefreshToken: "original", ExpiresAt: time.Now().Unix() - 1}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			caller, stop := context.WithCancel(ctx)
			defer stop()
			result := make(chan error, 1)
			go func() { _, err := manager.AccessToken(caller, resource); result <- err }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			manager.mu.Lock()
			refresh := manager.refreshes[resource]
			manager.mu.Unlock()
			stop()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled caller: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("cancelled caller waited for token exchange")
			}
			switch action {
			case "logout":
				if err := manager.Logout(resource); err != nil {
					t.Fatal(err)
				}
			case "relogin":
				login, err := manager.Begin(ctx, OAuthInput{URL: resource, ClientID: "client"})
				if err != nil {
					t.Fatal(err)
				}
				auth, err := url.Parse(login.AuthorizationURL)
				if err != nil {
					t.Fatal(err)
				}
				query := auth.Query()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, query.Get("redirect_uri")+"?"+url.Values{"state": {query.Get("state")}, "code": {"new-code"}}.Encode(), nil)
				if err != nil {
					t.Fatal(err)
				}
				reply, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				reply.Body.Close()
				if reply.StatusCode != http.StatusOK {
					t.Fatalf("relogin callback returned %d", reply.StatusCode)
				}
			}
			tokens := make(chan string, 2)
			if action == "refresh" {
				for range 2 {
					go func() { token, _ := manager.Token(ctx, resource, "expired"); tokens <- token }()
				}
			}
			unblock()
			select {
			case <-refresh.done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if action == "refresh" {
				for range 2 {
					select {
					case token := <-tokens:
						if token != "replacement" {
							t.Fatalf("shared refresh returned %q", token)
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			if exchanges.Load() != 1 {
				t.Fatalf("exchanges = %d", exchanges.Load())
			}
			reopened, err := NewOAuthManager(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			record, exists := reopened.records[resource]
			if action == "logout" && exists {
				t.Fatal("refresh resurrected logged-out credentials")
			}
			if action == "relogin" && (!exists || record.AccessToken != "new-login" || record.RefreshToken != "new-refresh") {
				t.Fatal("stale refresh overwrote a new login")
			}
			if action == "refresh" && (!exists || record.AccessToken != "replacement" || record.RefreshToken != "rotated") {
				t.Fatal("rotated credentials were not persisted")
			}
		})
	}
}

func TestOAuthHTTPRetriesUnauthorizedOnlyOnceWithTheSameRequest(t *testing.T) {
	var requests atomic.Int32
	var initialBody []byte
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch requests.Add(1) {
		case 1:
			initialBody = body
			if r.Header.Get("Authorization") != "Bearer rejected" {
				t.Error("first token missing")
			}
		case 2:
			if !bytes.Equal(initialBody, body) || r.Header.Get("Authorization") != "Bearer replacement" {
				t.Error("retry changed the request or did not use its replacement token")
			}
		default:
			t.Error("401 retried more than once")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()
	transport, err := startHTTP(Config{URL: upstream.URL, AccessToken: func(_ context.Context, rejected string) (string, error) {
		switch rejected {
		case "":
			return "rejected", nil
		case "rejected":
			return "replacement", nil
		default:
			return "", fmt.Errorf("unexpected rejected token %q", rejected)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if _, err := transport.Call(t.Context(), "tools/list", nil); err == nil || !strings.Contains(err.Error(), "401") || requests.Load() != 2 {
		t.Fatalf("expected bounded 401 retry, got %d requests: %v", requests.Load(), err)
	}
}
