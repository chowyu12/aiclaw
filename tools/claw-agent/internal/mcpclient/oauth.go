package mcpclient

// OAuth uses authorization-code + PKCE, protected-resource discovery and a
// loopback callback. Tokens never enter session config or renderer state.
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/secrets"
)

type OAuthInput struct {
	URL          string `json:"url"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Scope        string `json:"scope,omitempty"`
	RedirectPort int    `json:"redirectPort,omitempty"`
}
type OAuthStatus struct {
	State            string `json:"state"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	Error            string `json:"error,omitempty"`
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
}
type oauthRecord struct {
	Resource      string `json:"resource"`
	ClientID      string `json:"clientId"`
	ClientSecret  string `json:"clientSecret,omitempty"`
	TokenEndpoint string `json:"tokenEndpoint"`
	TokenAuth     string `json:"tokenAuth"`
	AccessToken   string `json:"accessToken"`
	RefreshToken  string `json:"refreshToken"`
	ExpiresAt     int64  `json:"expiresAt"`
}
type oauthAttempt struct {
	server *http.Server
	timer  *time.Timer
	status OAuthStatus
}
type OAuthManager struct {
	mu       sync.Mutex
	cipher   *secrets.Cipher
	path     string
	records  map[string]oauthRecord
	attempts map[string]*oauthAttempt
	http     *http.Client
}

func NewOAuthManager(dir string) (*OAuthManager, error) {
	cipher, err := secrets.Load(dir)
	if err != nil {
		return nil, err
	}
	m := &OAuthManager{cipher: cipher, path: filepath.Join(dir, "mcp-oauth.enc"), records: map[string]oauthRecord{}, attempts: map[string]*oauthAttempt{}, http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	body, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if !secrets.Sealed(string(body)) {
		return nil, errors.New("MCP OAuth credential file is not encrypted")
	}
	plain, err := cipher.Open(string(body))
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(plain), &m.records); err != nil {
		return nil, err
	}
	return m, nil
}
func (m *OAuthManager) CredentialPath() string { return m.path }
func (m *OAuthManager) KeyPath() string        { return filepath.Join(filepath.Dir(m.path), secrets.KeyFile) }
func (m *OAuthManager) save() error {
	raw, err := json.Marshal(m.records)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), ".mcp-oauth-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(m.cipher.Seal(string(raw)))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, m.path)
}
func oauthURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("Invalid OAuth URL")
	}
	loopback := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("OAuth requires HTTPS (HTTP is allowed only for loopback)")
	}
	return u, nil
}
func resourceURL(raw string) (string, error) {
	u, err := oauthURL(raw)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" {
		return "", errors.New("OAuth resource URL cannot contain a query")
	}
	u.Host = strings.ToLower(u.Host)
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}
func nonce() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (m *OAuthManager) jsonRequest(ctx context.Context, method, endpoint string, body io.Reader, contentType string, out any) error {
	if _, err := oauthURL(endpoint); err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	resp, err := m.http.Do(r)
	if err != nil {
		return errors.New("OAuth endpoint connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OAuth endpoint returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

var metadataHeader = regexp.MustCompile(`(?i)resource_metadata\s*=\s*"([^"]+)"`)

type oauthMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	PKCE                  []string `json:"code_challenge_methods_supported"`
	AuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
}

func (m *OAuthManager) discover(ctx context.Context, resource string) (oauthMetadata, string, error) {
	u, _ := url.Parse(resource)
	metadataURL := u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + strings.TrimRight(u.Path, "/")
	r, _ := http.NewRequestWithContext(ctx, "GET", resource, nil)
	resp, err := m.http.Do(r)
	if err == nil {
		match := metadataHeader.FindStringSubmatch(resp.Header.Get("WWW-Authenticate"))
		resp.Body.Close()
		if len(match) > 1 {
			metadataURL = match[1]
		}
	}
	var protected struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
		Scopes   []string `json:"scopes_supported"`
	}
	if err = m.jsonRequest(ctx, "GET", metadataURL, nil, "", &protected); err != nil {
		return oauthMetadata{}, "", err
	}
	if protected.Resource != resource || len(protected.Servers) == 0 {
		return oauthMetadata{}, "", errors.New("OAuth resource metadata does not match the MCP resource")
	}
	issuer, err := oauthURL(protected.Servers[0])
	if err != nil {
		return oauthMetadata{}, "", err
	}
	issuerString := strings.TrimRight(issuer.String(), "/")
	metaURL := issuer.Scheme + "://" + issuer.Host + "/.well-known/oauth-authorization-server" + strings.TrimRight(issuer.Path, "/")
	var meta oauthMetadata
	if err = m.jsonRequest(ctx, "GET", metaURL, nil, "", &meta); err != nil {
		return meta, "", err
	}
	if strings.TrimRight(meta.Issuer, "/") != issuerString {
		return meta, "", errors.New("OAuth issuer mismatch")
	}
	for _, endpoint := range []string{meta.AuthorizationEndpoint, meta.TokenEndpoint} {
		if _, err = oauthURL(endpoint); err != nil {
			return meta, "", err
		}
	}
	supported := false
	for _, method := range meta.PKCE {
		supported = supported || method == "S256"
	}
	if !supported {
		return meta, "", errors.New("OAuth server must support PKCE S256")
	}
	return meta, strings.Join(protected.Scopes, " "), nil
}
func (m *OAuthManager) Begin(ctx context.Context, input OAuthInput) (OAuthStatus, error) {
	resource, err := resourceURL(input.URL)
	if err != nil {
		return OAuthStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a := m.attempts[resource]; a != nil && a.status.State == "authorizing" {
		return OAuthStatus{}, errors.New("OAuth login is already in progress")
	}
	meta, scope, err := m.discover(ctx, resource)
	if err != nil {
		return OAuthStatus{}, err
	}
	if input.RedirectPort < 0 || input.RedirectPort > 65535 {
		return OAuthStatus{}, errors.New("Invalid OAuth callback port")
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", input.RedirectPort))
	if err != nil {
		return OAuthStatus{}, err
	}
	redirect := "http://" + listener.Addr().String() + "/oauth/callback"
	record := oauthRecord{Resource: resource, ClientID: strings.TrimSpace(input.ClientID), ClientSecret: input.ClientSecret, TokenEndpoint: meta.TokenEndpoint}
	if record.ClientID == "" {
		if meta.RegistrationEndpoint == "" {
			listener.Close()
			return OAuthStatus{}, errors.New("This OAuth server requires a pre-registered client ID")
		}
		raw, _ := json.Marshal(map[string]any{"client_name": "AIClaw", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
		var client struct {
			ID     string `json:"client_id"`
			Secret string `json:"client_secret"`
		}
		if err = m.jsonRequest(ctx, "POST", meta.RegistrationEndpoint, strings.NewReader(string(raw)), "application/json", &client); err != nil {
			listener.Close()
			return OAuthStatus{}, err
		}
		if client.ID == "" {
			listener.Close()
			return OAuthStatus{}, errors.New("OAuth registration did not return a client ID")
		}
		record.ClientID, record.ClientSecret = client.ID, client.Secret
	}
	record.TokenAuth = "none"
	if record.ClientSecret != "" {
		record.TokenAuth = "client_secret_basic"
		for _, method := range meta.AuthMethods {
			if method == "client_secret_post" {
				record.TokenAuth = method
				break
			}
		}
	}
	verifier, state := nonce(), nonce()
	sum := sha256.Sum256([]byte(verifier))
	if input.Scope != "" {
		scope = input.Scope
	}
	authorization, _ := url.Parse(meta.AuthorizationEndpoint)
	q := authorization.Query()
	q.Set("response_type", "code")
	q.Set("client_id", record.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	q.Set("resource", resource)
	if scope != "" {
		q.Set("scope", scope)
	}
	authorization.RawQuery = q.Encode()
	a := &oauthAttempt{status: OAuthStatus{State: "authorizing", AuthorizationURL: authorization.String()}}
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	a.server = server
	m.attempts[resource] = a
	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid OAuth state", 400)
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.attempts[resource] != a || a.status.State != "authorizing" {
			http.Error(w, "OAuth attempt expired", 410)
			return
		}
		a.timer.Stop()
		defer func() {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = server.Shutdown(ctx)
			}()
		}()
		a.status = OAuthStatus{State: "needs_login"}
		code := r.URL.Query().Get("code")
		if code == "" || r.URL.Query().Get("error") != "" {
			a.status.Error = "OAuth authorization was declined"
			http.Error(w, a.status.Error, 400)
			return
		}
		callbackCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {resource}}
		if err := m.exchange(callbackCtx, &record, values); err != nil {
			a.status.Error = err.Error()
			http.Error(w, "OAuth token exchange failed; return to AIClaw", 400)
			return
		}
		old, hadOld := m.records[resource]
		m.records[resource] = record
		if err := m.save(); err != nil {
			if hadOld {
				m.records[resource] = old
			} else {
				delete(m.records, resource)
			}
			a.status.Error = "Could not save OAuth credentials"
			http.Error(w, a.status.Error, 500)
			return
		}
		a.status = OAuthStatus{State: "connected", ExpiresAt: record.ExpiresAt}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "Connected. You can close this tab and return to AIClaw.")
	})
	a.timer = time.AfterFunc(5*time.Minute, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.attempts[resource] == a && a.status.State == "authorizing" {
			a.status = OAuthStatus{State: "needs_login", Error: "OAuth login timed out"}
			server.Close()
		}
	})
	go server.Serve(listener)
	return a.status, nil
}
func (m *OAuthManager) exchange(ctx context.Context, record *oauthRecord, values url.Values) error {
	values.Set("client_id", record.ClientID)
	if record.TokenAuth == "client_secret_post" {
		values.Set("client_secret", record.ClientSecret)
	}
	r, err := http.NewRequestWithContext(ctx, "POST", record.TokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if record.TokenAuth == "client_secret_basic" {
		r.SetBasicAuth(url.QueryEscape(record.ClientID), url.QueryEscape(record.ClientSecret))
	}
	resp, err := m.http.Do(r)
	if err != nil {
		return errors.New("OAuth token endpoint connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OAuth token endpoint returned HTTP %d; log in again", resp.StatusCode)
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Type    string `json:"token_type"`
		Expires int64  `json:"expires_in"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return errors.New("Invalid OAuth token response")
	}
	if token.Expires < 0 {
		return errors.New("Invalid OAuth token lifetime")
	}
	if token.Access == "" || !strings.EqualFold(token.Type, "bearer") {
		return errors.New("OAuth server did not return a Bearer token")
	}
	record.AccessToken = token.Access
	if token.Refresh != "" {
		record.RefreshToken = token.Refresh
	}
	record.ExpiresAt = 0
	if token.Expires > 0 {
		record.ExpiresAt = time.Now().Unix() + token.Expires
	}
	return nil
}
func (m *OAuthManager) Status(raw string) (OAuthStatus, error) {
	resource, err := resourceURL(raw)
	if err != nil {
		return OAuthStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a := m.attempts[resource]; a != nil && a.status.State != "connected" {
		s := a.status
		s.AuthorizationURL = ""
		return s, nil
	}
	r, ok := m.records[resource]
	if !ok {
		return OAuthStatus{State: "needs_login"}, nil
	}
	state := "connected"
	if r.ExpiresAt > 0 && r.ExpiresAt <= time.Now().Unix() {
		state = "expired"
	}
	return OAuthStatus{State: state, ExpiresAt: r.ExpiresAt}, nil
}
func (m *OAuthManager) AccessToken(ctx context.Context, raw string) (string, error) {
	resource, err := resourceURL(raw)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a := m.attempts[resource]; a != nil && a.status.State == "needs_login" {
		return "", errors.New("MCP OAuth login required")
	}
	r, ok := m.records[resource]
	if !ok {
		return "", errors.New("MCP OAuth login required")
	}
	if r.ExpiresAt == 0 || r.ExpiresAt > time.Now().Unix()+30 {
		return r.AccessToken, nil
	}
	if r.RefreshToken == "" {
		return "", errors.New("MCP OAuth token expired; log in again")
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {r.RefreshToken}, "resource": {resource}}
	if err = m.exchange(ctx, &r, values); err != nil {
		m.attempts[resource] = &oauthAttempt{status: OAuthStatus{State: "needs_login", Error: err.Error()}}
		return "", err
	}
	m.records[resource] = r
	if err = m.save(); err != nil {
		m.attempts[resource] = &oauthAttempt{status: OAuthStatus{State: "needs_login", Error: "Could not save refreshed OAuth credentials"}}
		return "", err
	}
	delete(m.attempts, resource)
	return r.AccessToken, nil
}
func (m *OAuthManager) Logout(raw string) error {
	resource, err := resourceURL(raw)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a := m.attempts[resource]; a != nil {
		if a.timer != nil {
			a.timer.Stop()
		}
		if a.server != nil {
			a.server.Close()
		}
		delete(m.attempts, resource)
	}
	old, ok := m.records[resource]
	delete(m.records, resource)
	if err = m.save(); err != nil {
		if ok {
			m.records[resource] = old
		}
		return err
	}
	return nil
}
func (m *OAuthManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.attempts {
		if a.timer != nil {
			a.timer.Stop()
		}
		if a.server != nil {
			a.server.Close()
		}
	}
	m.attempts = map[string]*oauthAttempt{}
}
