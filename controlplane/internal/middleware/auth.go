package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/sessions"
	"golang.org/x/oauth2"
)

type IndikoConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string

	AuthorizationEndpoint string
	TokenEndpoint          string
	IntrospectEndpoint    string
	UserInfoEndpoint       string
	JWKSEndpoint           string
}

func LoadIndikoConfigFromEnv() *IndikoConfig {
	cfg := &IndikoConfig{
		Issuer:       os.Getenv("INDIKO_ISSUER"),
		ClientID:     os.Getenv("INDIKO_CLIENT_ID"),
		ClientSecret: os.Getenv("INDIKO_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("INDIKO_REDIRECT_URL"),
		Scopes:       []string{"openid", "profile", "email"},
	}

	if cfg.RedirectURL == "" {
		cfg.RedirectURL = "http://localhost:8080/auth/callback"
	}

	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}

	cfg.AuthorizationEndpoint = cfg.Issuer + "/auth/authorize"
	cfg.TokenEndpoint = cfg.Issuer + "/auth/token"
	cfg.IntrospectEndpoint = cfg.Issuer + "/auth/token/introspect"
	cfg.UserInfoEndpoint = cfg.Issuer + "/userinfo"
	cfg.JWKSEndpoint = cfg.Issuer + "/jwks"

	return cfg
}

func (c *IndikoConfig) OAuth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		Scopes:       c.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  c.AuthorizationEndpoint,
			TokenURL: c.TokenEndpoint,
		},
	}
}

type PKCEState struct {
	Verifier  string
	State     string
	CreatedAt time.Time
}

type PKCEManager struct {
	mu     sync.Mutex
	states map[string]*PKCEState
}

func NewPKCEManager() *PKCEManager {
	return &PKCEManager{
		states: make(map[string]*PKCEState),
	}
}

func (p *PKCEManager) Generate() (*PKCEState, string, error) {
	verifier, err := randomString(64)
	if err != nil {
		return nil, "", err
	}

	state, err := randomString(32)
	if err != nil {
		return nil, "", err
	}

	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	pkce := &PKCEState{
		Verifier:  verifier,
		State:     state,
		CreatedAt: time.Now(),
	}

	p.mu.Lock()
	p.states[state] = pkce
	p.mu.Unlock()

	go p.cleanup()

	return pkce, challenge, nil
}

func (p *PKCEManager) Verify(state string) (*PKCEState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pkce, ok := p.states[state]
	if !ok {
		return nil, false
	}

	if time.Since(pkce.CreatedAt) > 10*time.Minute {
		delete(p.states, state)
		return nil, false
	}

	delete(p.states, state)
	return pkce, true
}

func (p *PKCEManager) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for k, v := range p.states {
		if now.Sub(v.CreatedAt) > 10*time.Minute {
			delete(p.states, k)
		}
	}
}

type UserInfo struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Photo  string `json:"photo,omitempty"`
	URL    string `json:"url,omitempty"`
	Me     string `json:"me,omitempty"`
	Role   string `json:"role,omitempty"`
}

type Auth struct {
	config      *IndikoConfig
	oauth2      *oauth2.Config
	pkce        *PKCEManager
	store       sessions.Store
	sessionName string
}

func NewAuth(cfg *IndikoConfig, sessionSecret string) *Auth {
	if sessionSecret == "" {
		sessionSecret = "controlplane-default-secret-change-me"
	}

	cookieStore := sessions.NewCookieStore([]byte(sessionSecret))
	cookieStore.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	return &Auth{
		config:      cfg,
		oauth2:      cfg.OAuth2Config(),
		pkce:        NewPKCEManager(),
		store:       cookieStore,
		sessionName: "controlplane-session",
	}
}

func (a *Auth) LoginHandler(w http.ResponseWriter, r *http.Request) {
	pkce, challenge, err := a.pkce.Generate()
	if err != nil {
		http.Error(w, "failed to generate PKCE", http.StatusInternalServerError)
		return
	}

	url := a.oauth2.AuthCodeURL(pkce.State,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	session, _ := a.store.Get(r, a.sessionName)
	session.Values["oauth_state"] = pkce.State
	session.Save(r, w)

	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (a *Auth) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	session, _ := a.store.Get(r, a.sessionName)

	savedState, ok := session.Values["oauth_state"].(string)
	if !ok {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}

	receivedState := r.URL.Query().Get("state")
	if receivedState == "" || receivedState != savedState {
		http.Error(w, "invalid state parameter", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	pkce, valid := a.pkce.Verify(receivedState)
	if !valid {
		http.Error(w, "invalid or expired PKCE state", http.StatusBadRequest)
		return
	}

	token, err := a.oauth2.Exchange(r.Context(), code,
		oauth2.SetAuthURLParam("code_verifier", pkce.Verifier),
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("token exchange failed: %v", err), http.StatusInternalServerError)
		return
	}

	userInfo, err := a.fetchUserInfo(token.AccessToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get user info: %v", err), http.StatusInternalServerError)
		return
	}

	session.Values["access_token"] = token.AccessToken
	session.Values["refresh_token"] = token.RefreshToken
	session.Values["authenticated"] = true
	session.Values["user_name"] = userInfo.Name
	session.Values["user_email"] = userInfo.Email
	session.Values["user_photo"] = userInfo.Photo
	session.Values["user_role"] = userInfo.Role
	session.Save(r, w)

	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *Auth) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	session, _ := a.store.Get(r, a.sessionName)

	accessToken, _ := session.Values["access_token"].(string)
	if accessToken != "" {
		a.revokeToken(accessToken)
	}

	session.Values = make(map[interface{}]interface{})
	session.Options.MaxAge = -1
	session.Save(r, w)

	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *Auth) fetchUserInfo(accessToken string) (*UserInfo, error) {
	req, err := http.NewRequest("GET", a.config.UserInfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo returned status %d", resp.StatusCode)
	}

	var info UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}

func (a *Auth) revokeToken(token string) {
	body := fmt.Sprintf(`{"token":"%s"}`, token)
	req, err := http.NewRequest("POST", a.config.IntrospectEndpoint[:strings.LastIndex(a.config.IntrospectEndpoint, "/")]+"/revoke", strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	http.DefaultClient.Do(req)
}

func (a *Auth) IntrospectToken(accessToken string) (*TokenIntrospection, error) {
	body := fmt.Sprintf(`{"token":"%s"}`, accessToken)
	req, err := http.NewRequest("POST", a.config.IntrospectEndpoint, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var intro TokenIntrospection
	if err := json.NewDecoder(resp.Body).Decode(&intro); err != nil {
		return nil, err
	}

	return &intro, nil
}

type TokenIntrospection struct {
	Active   bool   `json:"active"`
	Me       string `json:"me,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Exp      int64  `json:"exp,omitempty"`
	Iat      int64  `json:"iat,omitempty"`
}

func (a *Auth) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.isBrowserRequest(r) {
			if !a.isAuthenticated(r) {
				http.Redirect(w, r, "/auth/login", http.StatusTemporaryRedirect)
				return
			}
		} else {
			if !a.validateBearerToken(r) && !a.isAuthenticated(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (a *Auth) isAuthenticated(r *http.Request) bool {
	session, _ := a.store.Get(r, a.sessionName)
	auth, ok := session.Values["authenticated"].(bool)
	return ok && auth
}

func (a *Auth) validateBearerToken(r *http.Request) bool {
	if a.config == nil {
		return false
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return false
	}

	intro, err := a.IntrospectToken(parts[1])
	if err != nil {
		return false
	}

	return intro.Active
}

func (a *Auth) isBrowserRequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

func (a *Auth) GetSessionInfo(r *http.Request) map[string]interface{} {
	session, _ := a.store.Get(r, a.sessionName)
	info := make(map[string]interface{})
	for k, v := range session.Values {
		if key, ok := k.(string); ok {
			info[key] = v
		}
	}
	return info
}

func randomString(length int) (string, error) {
	b := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:length], nil
}
