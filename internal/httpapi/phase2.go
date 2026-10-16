package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"flyway-manager/internal/core"
	"flyway-manager/internal/database"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type actorKey struct{}
type phase2 struct {
	database *database.DB
	s        *core.Service
	logger   *slog.Logger
}

func actor(r *http.Request) core.Actor { a, _ := r.Context().Value(actorKey{}).(core.Actor); return a }
func trace(r *http.Request) core.Trace {
	return core.Trace{RequestID: middleware.GetReqID(r.Context()), Actor: actor(r)}
}
func page(r *http.Request) core.Page {
	l, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	o, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	return core.Page{Limit: l, Offset: o, Query: r.URL.Query().Get("q"), Environment: r.URL.Query().Get("environment"), Action: r.URL.Query().Get("action")}
}
func pathID(r *http.Request) string { return chi.URLParam(r, "id") }
func (h phase2) fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *core.Error
	if !errors.As(err, &e) {
		h.logger.Error("API operation failed", "request_id", middleware.GetReqID(r.Context()))
		writeError(w, 500, "INTERNAL_ERROR", "An unexpected error occurred.")
		return
	}
	if e.Status == 401 || e.Status == 403 || e.Status == 409 || (mutating(r.Method) && e.Status >= 400 && e.Status < 500) {
		// Rejected sensitive requests are durable events; payloads and URL queries are never logged.
		if h.s != nil {
			if auditErr := h.s.Event(r.Context(), trace(r), "request.denied", requestPath(r), "failure"); auditErr != nil {
				writeError(w, 500, "INTERNAL_ERROR", "Security event could not be recorded.")
				return
			}
		}
	}
	writeJSON(w, e.Status, errorEnvelope{Error: apiError{Code: e.Code, Message: e.Message, Field: e.Field}})
}
func (h phase2) ready(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.s == nil {
			writeError(w, 503, "NOT_READY", "Identity service is unavailable.")
			return
		}
		w.Header().Set("X-Request-ID", middleware.GetReqID(r.Context()))
		w.Header().Set("Cache-Control", "no-store")
		budget := 20 * time.Second
		if r.URL.Path == "/api/v1/migration-preflight" || r.URL.Path == "/api/v1/runtime" || strings.HasSuffix(r.URL.Path, "/flyway-info/refresh") {
			budget = 50 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (h phase2) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a core.Actor
		var err error
		auth := r.Header.Get("Authorization")
		cookie, cookieErr := r.Cookie("fm_session")
		if auth != "" {
			if cookieErr == nil {
				h.fail(w, r, &core.Error{Status: 400, Code: "AMBIGUOUS_AUTHENTICATION", Message: "Use one authentication mechanism per request."})
				return
			}
			if !strings.HasPrefix(auth, "Bearer ") {
				h.fail(w, r, &core.Error{Status: 401, Code: "UNAUTHENTICATED", Message: "Authentication required."})
				return
			}
			a, err = h.s.AuthenticateToken(r.Context(), strings.TrimPrefix(auth, "Bearer "), middleware.GetReqID(r.Context()))
		} else if cookieErr == nil {
			a, err = h.s.AuthenticateSession(r.Context(), cookie.Value)
		} else {
			err = &core.Error{Status: 401, Code: "UNAUTHENTICATED", Message: "Authentication required."}
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, a))
		if a.Kind == "user" && mutating(r.Method) {
			if !h.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(a.CSRF)) != 1 {
				h.fail(w, r, &core.Error{Status: 403, Code: "CSRF_REJECTED", Message: "Request origin or CSRF token is invalid."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func mutating(m string) bool { return m != "GET" && m != "HEAD" && m != "OPTIONS" }
func (h phase2) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	expected := "http://" + r.Host
	if r.TLS != nil {
		expected = "https://" + r.Host
	}
	if h.s.PublicURL != "" {
		expected = h.s.PublicURL
	}
	origin, e := url.Parse(r.Header.Get("Origin"))
	if e != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	return strings.EqualFold(origin.Scheme+"://"+origin.Host, strings.TrimRight(expected, "/"))
}
func (h phase2) origin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.sameOrigin(r) {
			h.fail(w, r, &core.Error{Status: 403, Code: "CSRF_REJECTED", Message: "Request origin is invalid."})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (h phase2) rate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !h.s.RateAllow("address:" + host) {
			if err := h.s.Event(r.Context(), trace(r), "auth.rate_limit", "login", "failure"); err != nil {
				h.fail(w, r, err)
				return
			}
			w.Header().Set("Retry-After", "60")
			writeError(w, 429, "RATE_LIMITED", "Too many attempts. Try again in one minute.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (h phase2) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !actor(r).Admin() {
			h.fail(w, r, &core.Error{Status: 403, Code: "FORBIDDEN", Message: "Administrator access required."})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func readBody(w http.ResponseWriter, r *http.Request, out any) error {
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		return &core.Error{Status: 415, Code: "UNSUPPORTED_MEDIA_TYPE", Message: "Use application/json."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err = dec.Decode(out); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			return &core.Error{Status: 413, Code: "REQUEST_TOO_LARGE", Message: "Request exceeds 1 MiB."}
		}
		return &core.Error{Status: 400, Code: "INVALID_JSON", Message: "Invalid JSON request or unknown field."}
	}
	if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &core.Error{Status: 400, Code: "INVALID_JSON", Message: "Expected a single JSON object."}
	}
	return nil
}
func result[T any](h phase2, fn func(*http.Request) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fn(r)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writeJSON(w, 200, envelope[T]{Data: data})
	}
}
func bodyResult[I, O any](h phase2, status int, fn func(*http.Request, I) (O, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in I
		if err := readBody(w, r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		data, err := fn(r, in)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writeJSON(w, status, envelope[O]{Data: data})
	}
}
func empty(h phase2, fn func(*http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(r); err != nil {
			h.fail(w, r, err)
			return
		}
		writeJSON(w, 200, envelope[map[string]bool]{Data: map[string]bool{"ok": true}})
	}
}
func (h phase2) setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, seconds int) {
	secure := r.TLS != nil || strings.HasPrefix(h.s.PublicURL, "https://")
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, MaxAge: seconds, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}
func (h phase2) register(r chi.Router) {
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(h.ready)
		r.Get("/auth/status", result(h, func(r *http.Request) (map[string]any, error) {
			required, e := h.s.BootstrapRequired(r.Context())
			if e != nil {
				return nil, e
			}
			providers, e := h.s.Providers(r.Context())
			if e != nil {
				return nil, e
			}
			v := map[string]any{"bootstrap_required": required, "providers": providers}
			if required {
				v["bootstrap_token_file"] = h.s.SetupTokenFile
			}
			return v, nil
		}))
		r.Group(func(r chi.Router) {
			r.Use(h.rate, h.origin)
			r.Post("/auth/bootstrap", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Username    string `json:"username"`
					DisplayName string `json:"display_name"`
					Password    string `json:"password"`
					SetupToken  string `json:"setup_token"`
				}
				if e := readBody(w, r, &in); e != nil {
					h.fail(w, r, e)
					return
				}
				data, raw, e := h.s.Bootstrap(r.Context(), trace(r), core.UserInput{Username: in.Username, DisplayName: in.DisplayName, Password: in.Password}, in.SetupToken)
				if e != nil {
					h.fail(w, r, e)
					return
				}
				h.setCookie(w, r, "fm_session", raw, "/", 12*3600)
				writeJSON(w, 201, envelope[core.SessionResult]{Data: data})
			})
			r.Post("/auth/login", func(w http.ResponseWriter, r *http.Request) {
				var in core.LoginInput
				if e := readBody(w, r, &in); e != nil {
					h.fail(w, r, e)
					return
				}
				if !h.s.RateAllow("account:" + strings.ToLower(strings.TrimSpace(in.Username))) {
					if err := h.s.Event(r.Context(), trace(r), "auth.rate_limit", "login", "failure"); err != nil {
						h.fail(w, r, err)
						return
					}
					w.Header().Set("Retry-After", "60")
					writeError(w, 429, "RATE_LIMITED", "Too many attempts. Try again in one minute.")
					return
				}
				data, raw, e := h.s.Login(r.Context(), trace(r), in)
				if e != nil {
					h.fail(w, r, e)
					return
				}
				h.setCookie(w, r, "fm_session", raw, "/", 12*3600)
				writeJSON(w, 200, envelope[core.SessionResult]{Data: data})
			})
		})
		r.With(h.rate).Get("/auth/oidc/start", func(w http.ResponseWriter, r *http.Request) {
			loc, cookie, e := h.s.OIDCStart(r.Context())
			if e != nil {
				h.fail(w, r, e)
				return
			}
			h.setCookie(w, r, "fm_oidc", cookie, "/api/v1/auth/oidc", 300)
			http.Redirect(w, r, loc, http.StatusFound)
		})
		r.With(h.rate).Get("/auth/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
			c, _ := r.Cookie("fm_oidc")
			cookie := ""
			if c != nil {
				cookie = c.Value
			}
			h.setCookie(w, r, "fm_oidc", "", "/api/v1/auth/oidc", -1)
			data, raw, e := h.s.OIDCCallback(r.Context(), trace(r), r.URL.Query().Get("state"), cookie, r.URL.Query().Get("code"))
			if e != nil {
				h.fail(w, r, e)
				return
			}
			_ = data
			h.setCookie(w, r, "fm_session", raw, "/", 12*3600)
			http.Redirect(w, r, "/runs", http.StatusFound)
		})
		r.Group(func(r chi.Router) {
			r.Use(h.authenticated)
			r.Get("/system", result(h, func(r *http.Request) (map[string]any, error) {
				return map[string]any{"installation_id": installationID(h.database), "schema_version": schemaVersion(h.database), "capabilities": map[string]bool{"authentication": true, "registry": true, "migration_execution": h.s.ExecutionEnabled()}}, nil
			}))
			r.Get("/auth/me", result(h, func(r *http.Request) (core.SessionResult, error) { return h.s.Me(r.Context(), actor(r)) }))
			r.Post("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
				if actor(r).Kind != "user" {
					h.fail(w, r, &core.Error{Status: 403, Code: "FORBIDDEN", Message: "Web session required."})
					return
				}
				if e := h.s.Logout(r.Context(), trace(r)); e != nil {
					h.fail(w, r, e)
					return
				}
				h.setCookie(w, r, "fm_session", "", "/", -1)
				writeJSON(w, 200, envelope[bool]{Data: true})
			})
			r.Get("/auth/sessions", result(h, func(r *http.Request) ([]core.SessionInfo, error) { return h.s.Sessions(r.Context(), actor(r)) }))
			r.Delete("/auth/sessions/{id}", empty(h, func(r *http.Request) error { return h.s.RevokeSession(r.Context(), trace(r), pathID(r)) }))
			r.Post("/auth/password", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Current string `json:"current_password"`
					New     string `json:"new_password"`
				}
				if e := readBody(w, r, &in); e != nil {
					h.fail(w, r, e)
					return
				}
				if e := h.s.ChangePassword(r.Context(), trace(r), in.Current, in.New); e != nil {
					h.fail(w, r, e)
					return
				}
				h.setCookie(w, r, "fm_session", "", "/", -1)
				writeJSON(w, 200, envelope[bool]{Data: true})
			})
			r.Get("/database-targets", result(h, func(r *http.Request) ([]core.Target, error) { return h.s.Targets(r.Context(), actor(r), page(r)) }))
			r.Get("/database-targets/{id}", result(h, func(r *http.Request) (core.Target, error) { return h.s.Target(r.Context(), actor(r), pathID(r)) }))
			r.Get("/database-targets/{id}/schemas", result(h, func(r *http.Request) ([]core.Schema, error) {
				return h.s.Schemas(r.Context(), actor(r), pathID(r), page(r))
			}))
			r.Get("/database-targets/{id}/schemas/{schemaID}", result(h, func(r *http.Request) (core.Schema, error) {
				return h.s.Schema(r.Context(), actor(r), pathID(r), chi.URLParam(r, "schemaID"))
			}))
			h.registerExecution(r)
			h.registerFlywayInfo(r)
			h.registerProjects(r)
			h.registerConnections(r)

			r.Group(func(r chi.Router) {
				r.Use(h.admin)
				r.Post("/database-targets", bodyResult(h, 201, func(r *http.Request, in core.TargetInput) (core.Target, error) {
					return h.s.SaveTarget(r.Context(), trace(r), "", in)
				}))
				r.Put("/database-targets/{id}", bodyResult(h, 200, func(r *http.Request, in core.TargetInput) (core.Target, error) {
					return h.s.SaveTarget(r.Context(), trace(r), pathID(r), in)
				}))
				r.Post("/database-targets/{id}/schemas", bodyResult(h, 201, func(r *http.Request, in core.SchemaInput) (core.Schema, error) {
					return h.s.SaveSchema(r.Context(), trace(r), pathID(r), "", in)
				}))
				r.Put("/database-targets/{id}/schemas/{schemaID}", bodyResult(h, 200, func(r *http.Request, in core.SchemaInput) (core.Schema, error) {
					return h.s.SaveSchema(r.Context(), trace(r), pathID(r), chi.URLParam(r, "schemaID"), in)
				}))
				r.Get("/access/users", result(h, func(r *http.Request) ([]core.User, error) { return h.s.Users(r.Context(), actor(r), page(r)) }))
				r.Post("/access/users", bodyResult(h, 201, func(r *http.Request, in core.UserInput) (core.User, error) {
					return h.s.CreateUser(r.Context(), trace(r), in)
				}))
				r.Put("/access/users/{id}", bodyResult(h, 200, func(r *http.Request, in core.UserUpdate) (core.User, error) {
					return h.s.UpdateUser(r.Context(), trace(r), pathID(r), in)
				}))
				r.Get("/access/service-accounts", result(h, func(r *http.Request) ([]core.ServiceAccount, error) {
					return h.s.ServiceAccounts(r.Context(), actor(r), page(r))
				}))
				r.Post("/access/service-accounts", bodyResult(h, 201, func(r *http.Request, in core.ServiceInput) (core.ServiceAccount, error) {
					return h.s.SaveService(r.Context(), trace(r), "", in)
				}))
				r.Put("/access/service-accounts/{id}", bodyResult(h, 200, func(r *http.Request, in core.ServiceInput) (core.ServiceAccount, error) {
					return h.s.SaveService(r.Context(), trace(r), pathID(r), in)
				}))
				for _, kind := range []string{"user", "service"} {
					kind := kind
					resource := "users"
					if kind == "service" {
						resource = "service-accounts"
					}
					r.Get("/access/"+resource+"/{id}/grants", result(h, func(r *http.Request) ([]core.Grant, error) {
						if kind == "user" {
							if _, e := h.s.User(r.Context(), pathID(r)); e != nil {
								return nil, e
							}
						} else {
							if _, e := h.s.ServiceAccount(r.Context(), actor(r), pathID(r)); e != nil {
								return nil, e
							}
						}
						return h.s.Grants(r.Context(), kind, pathID(r))
					}))
					r.Put("/access/"+resource+"/{id}/grants", bodyResult(h, 200, func(r *http.Request, in struct {
						Grants []core.Grant `json:"grants"`
					}) ([]core.Grant, error) {
						if e := h.s.SetGrants(r.Context(), trace(r), kind, pathID(r), in.Grants); e != nil {
							return nil, e
						}
						return h.s.Grants(r.Context(), kind, pathID(r))
					}))
				}
				r.Get("/access/service-accounts/{id}/tokens", result(h, func(r *http.Request) ([]core.Token, error) { return h.s.Tokens(r.Context(), actor(r), pathID(r)) }))
				r.Post("/access/service-accounts/{id}/tokens", bodyResult(h, 201, func(r *http.Request, in core.TokenInput) (core.IssuedToken, error) {
					return h.s.IssueToken(r.Context(), trace(r), pathID(r), "", in)
				}))
				r.Post("/access/service-accounts/{id}/tokens/{tokenID}/rotate", bodyResult(h, 201, func(r *http.Request, in core.TokenInput) (core.IssuedToken, error) {
					return h.s.IssueToken(r.Context(), trace(r), pathID(r), chi.URLParam(r, "tokenID"), in)
				}))
				r.Delete("/access/service-accounts/{id}/tokens/{tokenID}", empty(h, func(r *http.Request) error {
					return h.s.RevokeToken(r.Context(), trace(r), pathID(r), chi.URLParam(r, "tokenID"))
				}))
				r.Get("/execution-accounts", result(h, func(r *http.Request) ([]core.ExecutionAccount, error) {
					return h.s.Accounts(r.Context(), actor(r), r.URL.Query().Get("target_id"), page(r))
				}))
				r.Get("/execution-accounts/{id}", result(h, func(r *http.Request) (core.ExecutionAccount, error) {
					return h.s.Account(r.Context(), actor(r), pathID(r))
				}))
				r.Post("/execution-accounts", bodyResult(h, 201, func(r *http.Request, in core.AccountInput) (core.ExecutionAccount, error) {
					return h.s.SaveAccount(r.Context(), trace(r), "", in)
				}))
				r.Put("/execution-accounts/{id}", bodyResult(h, 200, func(r *http.Request, in core.AccountInput) (core.ExecutionAccount, error) {
					return h.s.SaveAccount(r.Context(), trace(r), pathID(r), in)
				}))
				r.Put("/execution-accounts/{id}/credential", bodyResult(h, 200, func(r *http.Request, in struct {
					Password string `json:"password"`
				}) (core.CredentialMetadata, error) {
					return h.s.ReplaceCredential(r.Context(), trace(r), pathID(r), in.Password, false)
				}))
				r.Delete("/execution-accounts/{id}/credential", result(h, func(r *http.Request) (core.CredentialMetadata, error) {
					return h.s.ReplaceCredential(r.Context(), trace(r), pathID(r), "", true)
				}))
				r.Get("/settings/authentication", result(h, func(r *http.Request) (core.AuthSettings, error) { return h.s.PublicSettings(r.Context(), actor(r)) }))
				r.Put("/settings/authentication", bodyResult(h, 200, func(r *http.Request, in core.AuthSettings) (core.AuthSettings, error) {
					return h.s.UpdateSettings(r.Context(), trace(r), in)
				}))
				r.Get("/access/audit", result(h, func(r *http.Request) ([]core.AuditEvent, error) { return h.s.Audit(r.Context(), actor(r), page(r)) }))
			})
		})
	})
}
