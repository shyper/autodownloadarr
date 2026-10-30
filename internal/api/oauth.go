package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shyper/autodownloadarr/internal/control"
	"github.com/shyper/autodownloadarr/internal/oauth"
)

func (s *server) integrationReady(w http.ResponseWriter) bool {
	if s.PublicURL == "" {
		writeError(w, 503, "not_configured", "set AUTODOWNLOADARR_PUBLIC_URL to enable integrations")
		return false
	}
	return true
}
func (s *server) oauthMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.integrationReady(w) {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	base := strings.TrimRight(s.PublicURL, "/")
	if strings.Contains(r.URL.Path, "oauth-protected-resource") {
		writeJSON(w, 200, map[string]any{"resource": s.oauth.Resource, "authorization_servers": []string{base}, "scopes_supported": oauth.Scopes, "bearer_methods_supported": []string{"header"}})
		return
	}
	writeJSON(w, 200, map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "revocation_endpoint": base + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "client_id_metadata_document_supported": true, "scopes_supported": append(append([]string{}, oauth.Scopes...), "offline_access")})
}
func authRequest(q url.Values) oauth.Request {
	return oauth.Request{ClientID: q.Get("client_id"), Redirect: q.Get("redirect_uri"), Challenge: q.Get("code_challenge"), Scope: q.Get("scope"), State: q.Get("state"), Resource: q.Get("resource")}
}
func (s *server) validateAuthorization(w http.ResponseWriter, r *http.Request) (oauth.Request, bool) {
	if !s.integrationReady(w) {
		return oauth.Request{}, false
	}
	q := r.URL.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
		writeError(w, 400, "invalid_request", "authorization code with S256 PKCE required")
		return oauth.Request{}, false
	}
	req := authRequest(q)
	if _, err := s.oauth.Validate(r.Context(), req); err != nil {
		writeError(w, 400, "invalid_request", "invalid authorization request")
		return req, false
	}
	return req, true
}
func (s *server) handleAuthorization(w http.ResponseWriter, r *http.Request) {
	req, ok := s.validateAuthorization(w, r)
	if !ok {
		return
	}
	c, err := s.oauth.Client(r.Context(), req.ClientID)
	if err != nil {
		writeError(w, 400, "invalid_request", "invalid client")
		return
	}
	writeJSON(w, 200, map[string]any{"clientName": c.Name, "clientId": c.ID, "redirectUri": req.Redirect, "scopes": strings.Fields(req.Scope)})
}
func (s *server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	req, ok := s.validateAuthorization(w, r)
	if !ok {
		return
	}
	var in struct {
		Allow bool   `json:"allow"`
		Scope string `json:"scope"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	dest, _ := url.Parse(req.Redirect)
	q := dest.Query()
	if req.State != "" {
		q.Set("state", req.State)
	}
	q.Set("iss", strings.TrimRight(s.PublicURL, "/"))
	if !in.Allow {
		q.Set("error", "access_denied")
	} else {
		u, _, _ := currentUser(r.Context())
		code, err := s.oauth.Authorize(r.Context(), u.ID, req, in.Scope)
		if err != nil {
			writeError(w, 400, "invalid_request", "invalid authorization")
			return
		}
		q.Set("code", code)
	}
	dest.RawQuery = q.Encode()
	writeJSON(w, 200, map[string]string{"redirect": dest.String()})
}
func (s *server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if !s.integrationReady(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil || (r.PostForm.Get("resource") != "" && r.PostForm.Get("resource") != s.oauth.Resource) {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	var t oauth.Tokens
	var err error
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		t, err = s.oauth.Exchange(r.Context(), r.PostForm.Get("client_id"), r.PostForm.Get("code"), r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
	case "refresh_token":
		t, err = s.oauth.Refresh(r.Context(), r.PostForm.Get("client_id"), r.PostForm.Get("refresh_token"))
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	writeJSON(w, 200, t)
}
func (s *server) handleOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	if err := s.oauth.RevokeToken(r.Context(), r.PostForm.Get("client_id"), r.PostForm.Get("token")); err != nil {
		s.internalError(w, "revoke token", err)
		return
	}
	w.WriteHeader(200)
}
func (s *server) handleGrants(w http.ResponseWriter, r *http.Request) {
	u, _, _ := currentUser(r.Context())
	grants, err := s.oauth.Grants(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, "read grants", err)
		return
	}
	writeJSON(w, 200, map[string]any{"grants": grants, "mcpUrl": s.oauth.Resource, "enabled": s.PublicURL != ""})
}
func (s *server) handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	u, _, _ := currentUser(r.Context())
	var in struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.oauth.Revoke(r.Context(), u.ID, in.ID); err != nil {
		s.internalError(w, "revoke grant", err)
		return
	}
	w.WriteHeader(204)
}
func (s *server) handleCreateOAuthClient(w http.ResponseWriter, r *http.Request) {
	var in oauth.Client
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := s.oauth.SaveClient(r.Context(), in)
	if err != nil {
		writeError(w, 400, "invalid_input", "invalid client metadata")
		return
	}
	writeJSON(w, 201, c)
}
func (s *server) mcpHandler() http.Handler {
	transport := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		identity, err := s.oauth.ValidateToken(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if err != nil {
			return nil
		}
		user, err := s.Auth.User(r.Context(), identity.UserID)
		if err != nil {
			return nil
		}
		server := mcp.NewServer(&mcp.Implementation{Name: "AutoDownloadarr", Version: s.Version}, nil)
		for _, op := range control.Operations {
			if !oauth.Has(identity.Scope, op.Scope) {
				continue
			}
			mcp.AddTool(server, &mcp.Tool{Name: op.Name, Description: op.Description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: op.Scope == "media:read", DestructiveHint: new(op.Scope == "media:delete" || op.Scope == "media:manage")}}, func(ctx context.Context, _ *mcp.CallToolRequest, in control.Input) (*mcp.CallToolResult, any, error) {
				// Re-read grants and roles for every invocation, including batched requests.
				fresh, e := s.oauth.ValidateToken(ctx, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				if e != nil || !oauth.Has(fresh.Scope, op.Scope) {
					return nil, nil, fmt.Errorf("unauthorized")
				}
				u, e := s.Auth.User(ctx, user.ID)
				if e != nil {
					return nil, nil, fmt.Errorf("unauthorized")
				}
				result, e := s.control.Execute(ctx, u, op.Name, in)
				if e != nil {
					s.Log.Warn("MCP operation failed", "operation", op.Name, "user", u.ID, "err", e)
					return nil, nil, fmt.Errorf("%s", control.ErrorCode(e))
				}
				s.Log.Info("MCP operation", "operation", op.Name, "user", u.ID)
				return nil, map[string]any{"data": result}, nil
			})
		}
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.integrationReady(w) {
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != strings.TrimRight(s.PublicURL, "/") {
			writeError(w, 403, "forbidden", "origin rejected")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		_, err := s.oauth.ValidateToken(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || err != nil {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, strings.TrimRight(s.PublicURL, "/")+"/.well-known/oauth-protected-resource/mcp"))
			writeError(w, 401, "unauthorized", "valid access token required")
			return
		}
		transport.ServeHTTP(w, r)
	})
}
