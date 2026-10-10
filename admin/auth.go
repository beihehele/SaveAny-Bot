package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/adminauth"
)

const cookieName = "saveany_admin_session"

type adminSession struct {
	CSRF    string    `json:"csrf_token"`
	Expires time.Time `json:"expires_at"`
}

type loginBucket struct {
	Until time.Time
	Count int
}

type authentication struct {
	mu       sync.Mutex
	verifier *adminauth.Verifier
	ttl      time.Duration
	sessions map[[32]byte]adminSession
	attempts map[string]loginBucket
	kdfSlots chan struct{}
}

func newAuthentication(verifier *adminauth.Verifier, ttl time.Duration) *authentication {
	return &authentication{verifier: verifier, ttl: ttl, sessions: make(map[[32]byte]adminSession),
		attempts: make(map[string]loginBucket), kdfSlots: make(chan struct{}, 2)}
}

func randomToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

func (a *authentication) allowed(ip string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, bucket := range a.attempts {
		if !now.Before(bucket.Until) {
			delete(a.attempts, key)
		}
	}
	bucket, exists := a.attempts[ip]
	if !exists {
		if len(a.attempts) >= 1024 {
			return false
		}
		bucket.Until = now.Add(time.Minute)
	}
	if bucket.Count >= 5 {
		return false
	}
	bucket.Count++
	a.attempts[ip] = bucket
	return true
}

func (a *authentication) issue(now time.Time) (string, adminSession, error) {
	token, err := randomToken()
	if err != nil {
		return "", adminSession{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", adminSession{}, err
	}
	entry := adminSession{CSRF: csrf, Expires: now.Add(a.ttl)}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) >= 16 {
		var oldest [32]byte
		var expiry time.Time
		for id, session := range a.sessions {
			if expiry.IsZero() || session.Expires.Before(expiry) {
				oldest, expiry = id, session.Expires
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[sha256.Sum256([]byte(token))] = entry
	return token, entry, nil
}

func (a *authentication) find(r *http.Request, now time.Time) (adminSession, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || len(cookie.Value) != 64 {
		return adminSession{}, false
	}
	id := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.sessions[id]
	if ok && !now.Before(entry.Expires) {
		delete(a.sessions, id)
		return adminSession{}, false
	}
	return entry, ok
}

func (a *authentication) revoke(r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(cookie.Value)))
		a.mu.Unlock()
	}
}

func (a *authentication) cleanup(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, entry := range a.sessions {
		if !now.Before(entry.Expires) {
			delete(a.sessions, id)
		}
	}
	for ip, bucket := range a.attempts {
		if !now.Before(bucket.Until) {
			delete(a.attempts, ip)
		}
	}
}

func (a *authentication) clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	clear(a.sessions)
	clear(a.attempts)
}

func (s *Server) sameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil || s.cfg.SecureCookie {
		scheme = "https"
	}
	return origin.Scheme == scheme && origin.Host == r.Host
}

func (s *Server) route(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		entry, ok := s.auth.find(r, time.Now())
		if !ok {
			s.fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			s.fail(w, http.StatusForbidden, "csrf_invalid")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !s.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(entry.CSRF)) != 1 {
				s.fail(w, http.StatusForbidden, "csrf_invalid")
				return
			}
		}
		handler(w, r)
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		s.fail(w, http.StatusForbidden, "csrf_invalid")
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !s.auth.allowed(ip, time.Now()) {
		w.Header().Set("Retry-After", "60")
		s.fail(w, http.StatusTooManyRequests, "login_limited")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &input, 16<<10); err != nil || len(input.Password) > 1024 {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	select {
	case s.auth.kdfSlots <- struct{}{}:
		defer func() { <-s.auth.kdfSlots }()
	default:
		s.fail(w, http.StatusTooManyRequests, "login_limited")
		return
	}
	passwordOK := s.auth.verifier.Verify([]byte(input.Password))
	if !passwordOK || subtle.ConstantTimeCompare([]byte(input.Username), []byte("admin")) != 1 {
		s.audit.add("login", "", "failed")
		s.fail(w, http.StatusUnauthorized, "login_failed")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	token, entry, err := s.auth.issue(time.Now())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "internal_error")
		return
	}
	// Rotate rather than retain a pre-existing login session.
	s.auth.revoke(r)
	s.setCookie(w, r, token, entry.Expires, int(s.cfg.SessionTTL.Seconds()))
	s.audit.add("login", "", "ok")
	s.write(w, http.StatusOK, entry)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.auth.find(r, time.Now())
	if !ok {
		s.fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.write(w, http.StatusOK, entry)
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, value string, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/admin/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookie || r.TLS != nil, Expires: expires, MaxAge: maxAge})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.auth.revoke(r)
	s.setCookie(w, r, "", time.Unix(1, 0), -1)
	s.audit.add("logout", "", "ok")
	s.write(w, http.StatusOK, map[string]bool{"ok": true})
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	return nil
}
