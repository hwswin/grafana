package middleware

import (
	"fmt"
	"strings"

	macaron "gopkg.in/macaron.v1"

	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/setting"
)

var (
	ReqGrafanaAdmin = Auth(&AuthOptions{
		ReqSignedIn:     true,
		ReqGrafanaAdmin: true,
	})
	ReqSignedIn   = Auth(&AuthOptions{ReqSignedIn: true})
	ReqEditorRole = RoleAuth(models.ROLE_EDITOR, models.ROLE_ADMIN)
	ReqOrgAdmin   = RoleAuth(models.ROLE_ADMIN)
)

func AddDefaultResponseHeaders() macaron.Handler {
	return func(ctx *macaron.Context) {
		ctx.Resp.Before(func(w macaron.ResponseWriter) {
			// if response has already been written, skip.
			if w.Written() {
				return
			}

			if !strings.HasPrefix(ctx.Req.URL.Path, "/api/datasources/proxy/") {
				AddNoCacheHeaders(ctx.Resp)
			}

			if !setting.AllowEmbedding {
				AddXFrameOptionsDenyHeader(w)
			}

			AddSecurityHeaders(w)
		})
	}
}

// AddSecurityHeaders adds various HTTP(S) response headers that enable various security protections behaviors in the client's browser.
func AddSecurityHeaders(w macaron.ResponseWriter) {
	if (setting.Protocol == setting.HTTPSScheme || setting.Protocol == setting.HTTP2Scheme) &&
		setting.StrictTransportSecurity {
		strictHeaderValues := []string{fmt.Sprintf("max-age=%v", setting.StrictTransportSecurityMaxAge)}
		if setting.StrictTransportSecurityPreload {
			strictHeaderValues = append(strictHeaderValues, "preload")
		}
		if setting.StrictTransportSecuritySubDomains {
			strictHeaderValues = append(strictHeaderValues, "includeSubDomains")
		}
		w.Header().Add("Strict-Transport-Security", strings.Join(strictHeaderValues, "; "))
	}

	if setting.ContentTypeProtectionHeader {
		w.Header().Add("X-Content-Type-Options", "nosniff")
	}

	if setting.XSSProtectionHeader {
		w.Header().Add("X-XSS-Protection", "1; mode=block")
	}
}

func AddNoCacheHeaders(w macaron.ResponseWriter) {
	w.Header().Add("Cache-Control", "no-cache")
	w.Header().Add("Pragma", "no-cache")
	w.Header().Add("Expires", "-1")
}

func AddXFrameOptionsDenyHeader(w macaron.ResponseWriter) {
	w.Header().Add("X-Frame-Options", "deny")
}
