package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"monitorDNS/internal/store"
)

const sessionCookieName = "monitorDNS_session"

type Server struct {
	store *store.Store
	tpl   *template.Template
}

func NewServer(st *store.Store) *Server {
	tpl := template.Must(template.New("base").Funcs(template.FuncMap{
		"since": func(t time.Time) string { return time.Since(t).Truncate(time.Second).String() },
	}).ParseFS(assets, "templates/*.html"))

	return &Server{store: st, tpl: tpl}
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	mux := http.NewServeMux()

	// static
	sub, _ := fs.Sub(assets, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))

	// auth
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.requireAuth(s.handleLogout))

	// pages
	mux.HandleFunc("/", s.requireAuth(s.handleIndex))
	mux.HandleFunc("/domains", s.requireAuth(s.handleDomainsPost))
	mux.HandleFunc("/domains/", s.requireAuth(s.handleDomainSubroutes))

	// api
	mux.HandleFunc("/api/domains/", s.requireAuth(s.handleAPIDomain))

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv.ListenAndServe()
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := s.currentUser(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) currentUser(r *http.Request) (*store.User, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return nil, store.ErrNotFound
	}
	return s.store.GetUserBySession(r.Context(), c.Value)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_ = s.tpl.ExecuteTemplate(w, "login.html", map[string]any{
			"Error": "",
		})
	case http.MethodPost:
		_ = r.ParseForm()
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")

		u, err := s.store.Authenticate(r.Context(), username, password)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = s.tpl.ExecuteTemplate(w, "login.html", map[string]any{
				"Error": "用户名或密码错误",
			})
			return
		}

		token, expires, err := s.store.CreateSession(r.Context(), u.ID, 24*time.Hour)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    token,
			Path:     "/",
			Expires:  expires,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusFound)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	c, err := r.Cookie(sessionCookieName)
	if err == nil && strings.TrimSpace(c.Value) != "" {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	u, _ := s.currentUser(r)
	domains, err := s.store.ListDomains(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.tpl.ExecuteTemplate(w, "index.html", map[string]any{
		"User":    u,
		"Domains": domains,
	})
}

func (s *Server) handleDomainsPost(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/domains" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	domain := strings.TrimSpace(r.FormValue("domain"))
	remark := strings.TrimSpace(r.FormValue("remark"))
	recordType := strings.TrimSpace(r.FormValue("record_type"))
	intervalStr := strings.TrimSpace(r.FormValue("interval"))
	interval, _ := strconv.Atoi(intervalStr)
	// allow user-defined interval: 1s ~ 86400s
	if interval < 1 {
		interval = 1
	}
	if interval > 86400 {
		interval = 86400
	}
	if domain == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if recordType != "A" && recordType != "CNAME" {
		recordType = "A"
	}
	_, err := s.store.CreateDomain(r.Context(), domain, recordType, interval, remark)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleDomainSubroutes(w http.ResponseWriter, r *http.Request) {
	// /domains/{id}
	// /domains/{id}/toggle
	// /domains/{id}/delete
	path := strings.TrimPrefix(r.URL.Path, "/domains/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.handleDomainDetail(w, r, id)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "toggle":
			s.handleDomainToggle(w, r, id)
			return
		case "delete":
			s.handleDomainDelete(w, r, id)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) handleDomainDetail(w http.ResponseWriter, r *http.Request, id int64) {
	u, _ := s.currentUser(r)
	d, err := s.store.GetDomain(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stats, _ := s.store.GetStats(r.Context(), id)
	_ = s.tpl.ExecuteTemplate(w, "domain.html", map[string]any{
		"User":  u,
		"Dom":   d,
		"Stats": stats,
	})
}

func (s *Server) handleDomainToggle(w http.ResponseWriter, r *http.Request, id int64) {
	d, err := s.store.GetDomain(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = s.store.SetDomainEnabled(r.Context(), id, !d.Enabled)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleDomainDelete(w http.ResponseWriter, r *http.Request, id int64) {
	_ = s.store.DeleteDomain(r.Context(), id)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleAPIDomain(w http.ResponseWriter, r *http.Request) {
	// /api/domains/{id}/stats|checks|changes
	path := strings.TrimPrefix(r.URL.Path, "/api/domains/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	resource := parts[1]

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch resource {
	case "stats":
		st, err := s.store.GetStats(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(st)
	case "checks":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		items, total, err := s.store.ListChecksPage(r.Context(), id, page, pageSize)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if page <= 0 {
			page = 1
		}
		if pageSize <= 0 || pageSize > 500 {
			pageSize = 100
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"page":      page,
			"page_size": pageSize,
			"total":     total,
			"items":     items,
		})
	case "changes":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		out, err := s.store.ListChanges(r.Context(), id, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		http.NotFound(w, r)
	}
}
