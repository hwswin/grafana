// Package contexthandler contains the ContextHandler service.
package contexthandler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/components/apikeygen"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/remotecache"
	"github.com/grafana/grafana/pkg/middleware"
	authproxy "github.com/grafana/grafana/pkg/middleware/auth_proxy"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/registry"
	"github.com/grafana/grafana/pkg/services/login"
	"github.com/grafana/grafana/pkg/services/rendering"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/util"
	"gopkg.in/macaron.v1"
)

const (
	errStringInvalidUsernamePassword = "Invalid username or password"
	errStringInvalidAPIKey           = "Invalid API key"
)

var getTime = time.Now

func init() {
	registry.Register(&registry.Descriptor{
		Name:         "ContextHandler",
		Instance:     &ContextHandler{},
		InitPriority: registry.High,
	})
}

// ContextHandler is a middleware.
type ContextHandler struct {
	Cfg                *setting.Cfg             `inject:""`
	AuthTokenService   models.UserTokenService  `inject:""`
	RemoteCacheService *remotecache.RemoteCache `inject:""`
	RenderService      rendering.Service        `inject:""`
}

// Init initializes the service.
func (h *ContextHandler) Init() error {
	return nil
}

// Middleware provides a middleware to initialize the Macaron context.
func (h *ContextHandler) Middleware(c *macaron.Context) {
	ctx := &models.ReqContext{
		Context:        c,
		SignedInUser:   &models.SignedInUser{},
		IsSignedIn:     false,
		AllowAnonymous: false,
		SkipCache:      false,
		Logger:         log.New("context"),
	}

	const headerName = "X-Grafana-Org-Id"
	orgID := int64(0)
	orgIDHeader := ctx.Req.Header.Get(headerName)
	if orgIDHeader != "" {
		id, err := strconv.ParseInt(orgIDHeader, 10, 64)
		if err == nil {
			orgID = id
		} else {
			ctx.Logger.Debug("Received invalid header", "header", headerName, "value", orgIDHeader)
		}
	}

	// the order in which these are tested are important
	// look for api key in Authorization header first
	// then init session and look for userId in session
	// then look for api key in session (special case for render calls via api)
	// then test if anonymous access is enabled
	switch {
	case h.initContextWithRenderAuth(ctx):
	case h.initContextWithApiKey(ctx):
	case h.initContextWithBasicAuth(ctx, orgID):
	case h.initContextWithAuthProxy(ctx, orgID):
	case h.initContextWithToken(ctx, orgID):
	case h.initContextWithAnonymousUser(ctx):
	}

	ctx.Logger = log.New("context", "userId", ctx.UserId, "orgId", ctx.OrgId, "uname", ctx.Login)
	ctx.Data["ctx"] = ctx

	c.Map(ctx)

	// update last seen every 5min
	if ctx.ShouldUpdateLastSeenAt() {
		ctx.Logger.Debug("Updating last user_seen_at", "user_id", ctx.UserId)
		if err := bus.Dispatch(&models.UpdateUserLastSeenAtCommand{UserId: ctx.UserId}); err != nil {
			ctx.Logger.Error("Failed to update last_seen_at", "error", err)
		}
	}
}

func (h *ContextHandler) initContextWithAnonymousUser(ctx *models.ReqContext) bool {
	if !h.Cfg.AnonymousEnabled {
		return false
	}

	orgQuery := models.GetOrgByNameQuery{Name: h.Cfg.AnonymousOrgName}
	if err := bus.Dispatch(&orgQuery); err != nil {
		log.Errorf(3, "Anonymous access organization error: '%s': %s", h.Cfg.AnonymousOrgName, err)
		return false
	}

	ctx.IsSignedIn = false
	ctx.AllowAnonymous = true
	ctx.SignedInUser = &models.SignedInUser{IsAnonymous: true}
	ctx.OrgRole = models.RoleType(h.Cfg.AnonymousOrgRole)
	ctx.OrgId = orgQuery.Result.Id
	ctx.OrgName = orgQuery.Result.Name
	return true
}

func (h *ContextHandler) initContextWithApiKey(ctx *models.ReqContext) bool {
	header := ctx.Req.Header.Get("Authorization")
	parts := strings.SplitN(header, " ", 2)
	var keyString string
	if len(parts) == 2 && parts[0] == "Bearer" {
		keyString = parts[1]
	} else {
		username, password, err := util.DecodeBasicAuthHeader(header)
		if err == nil && username == "api_key" {
			keyString = password
		}
	}

	if keyString == "" {
		return false
	}

	// base64 decode key
	decoded, err := apikeygen.Decode(keyString)
	if err != nil {
		ctx.JsonApiErr(401, errStringInvalidAPIKey, err)
		return true
	}

	// fetch key
	keyQuery := models.GetApiKeyByNameQuery{KeyName: decoded.Name, OrgId: decoded.OrgId}
	if err := bus.Dispatch(&keyQuery); err != nil {
		ctx.JsonApiErr(401, errStringInvalidAPIKey, err)
		return true
	}

	apikey := keyQuery.Result

	// validate api key
	isValid, err := apikeygen.IsValid(decoded, apikey.Key)
	if err != nil {
		ctx.JsonApiErr(500, "Validating API key failed", err)
		return true
	}
	if !isValid {
		ctx.JsonApiErr(401, errStringInvalidAPIKey, err)
		return true
	}

	// check for expiration
	if apikey.Expires != nil && *apikey.Expires <= getTime().Unix() {
		ctx.JsonApiErr(401, "Expired API key", err)
		return true
	}

	ctx.IsSignedIn = true
	ctx.SignedInUser = &models.SignedInUser{}
	ctx.OrgRole = apikey.Role
	ctx.ApiKeyId = apikey.Id
	ctx.OrgId = apikey.OrgId
	return true
}

func (h *ContextHandler) initContextWithBasicAuth(ctx *models.ReqContext, orgID int64) bool {
	if !h.Cfg.BasicAuthEnabled {
		return false
	}

	header := ctx.Req.Header.Get("Authorization")
	if header == "" {
		return false
	}

	username, password, err := util.DecodeBasicAuthHeader(header)
	if err != nil {
		ctx.JsonApiErr(401, "Invalid Basic Auth Header", err)
		return true
	}

	authQuery := models.LoginUserQuery{
		Username: username,
		Password: password,
	}
	if err := bus.Dispatch(&authQuery); err != nil {
		ctx.Logger.Debug(
			"Failed to authorize the user",
			"username", username,
			"err", err,
		)

		if err == models.ErrUserNotFound {
			err = login.ErrInvalidCredentials
		}
		ctx.JsonApiErr(401, errStringInvalidUsernamePassword, err)
		return true
	}

	user := authQuery.User

	query := models.GetSignedInUserQuery{UserId: user.Id, OrgId: orgID}
	if err := bus.Dispatch(&query); err != nil {
		ctx.Logger.Error(
			"Failed at user signed in",
			"id", user.Id,
			"org", orgID,
		)
		ctx.JsonApiErr(401, errStringInvalidUsernamePassword, err)
		return true
	}

	ctx.SignedInUser = query.Result
	ctx.IsSignedIn = true
	return true
}

func (h *ContextHandler) initContextWithToken(ctx *models.ReqContext, orgID int64) bool {
	if h.Cfg.LoginCookieName == "" {
		return false
	}

	rawToken := ctx.GetCookie(h.Cfg.LoginCookieName)
	if rawToken == "" {
		return false
	}

	token, err := h.AuthTokenService.LookupToken(ctx.Req.Context(), rawToken)
	if err != nil {
		ctx.Logger.Error("Failed to look up user based on cookie", "error", err)
		middleware.WriteSessionCookie(ctx, h.Cfg, "", -1)
		return false
	}

	query := models.GetSignedInUserQuery{UserId: token.UserId, OrgId: orgID}
	if err := bus.Dispatch(&query); err != nil {
		ctx.Logger.Error("Failed to get user with id", "userId", token.UserId, "error", err)
		return false
	}

	ctx.SignedInUser = query.Result
	ctx.IsSignedIn = true
	ctx.UserToken = token

	// Rotate the token just before we write response headers to ensure there is no delay between
	// the new token being generated and the client receiving it.
	ctx.Resp.Before(h.rotateEndOfRequestFunc(ctx, h.AuthTokenService, token))

	return true
}

func (h *ContextHandler) rotateEndOfRequestFunc(ctx *models.ReqContext, authTokenService models.UserTokenService,
	token *models.UserToken) macaron.BeforeFunc {
	return func(w macaron.ResponseWriter) {
		// if response has already been written, skip.
		if w.Written() {
			return
		}

		// if the request is cancelled by the client we should not try
		// to rotate the token since the client would not accept any result.
		if ctx.Context.Req.Context().Err() == context.Canceled {
			return
		}

		rotated, err := authTokenService.TryRotateToken(ctx.Req.Context(), token, ctx.RemoteAddr(), ctx.Req.UserAgent())
		if err != nil {
			ctx.Logger.Error("Failed to rotate token", "error", err)
			return
		}

		if rotated {
			middleware.WriteSessionCookie(ctx, h.Cfg, token.UnhashedToken, h.Cfg.LoginMaxLifetime)
		}
	}
}

func (h *ContextHandler) initContextWithRenderAuth(ctx *models.ReqContext) bool {
	key := ctx.GetCookie("renderKey")
	if key == "" {
		return false
	}

	renderUser, exists := h.RenderService.GetRenderUser(key)
	if !exists {
		ctx.JsonApiErr(401, "Invalid Render Key", nil)
		return true
	}

	ctx.IsSignedIn = true
	ctx.SignedInUser = &models.SignedInUser{
		OrgId:   renderUser.OrgID,
		UserId:  renderUser.UserID,
		OrgRole: models.RoleType(renderUser.OrgRole),
	}
	ctx.IsRenderCall = true
	ctx.LastSeenAt = time.Now()
	return true
}

func logUserIn(auth *authproxy.AuthProxy, username string, logger log.Logger, ignoreCache bool) (int64, *authproxy.Error) {
	logger.Debug("Trying to log user in", "username", username, "ignoreCache", ignoreCache)
	// Try to log in user via various providers
	id, err := auth.Login(logger, ignoreCache)
	if err != nil {
		logger.Error("Failed to login", "username", username, "message", err.Error(), "error", err.DetailsError,
			"ignoreCache", ignoreCache)
		return 0, err
	}
	return id, nil
}

func (h *ContextHandler) initContextWithAuthProxy(ctx *models.ReqContext, orgID int64) bool {
	username := ctx.Req.Header.Get(h.Cfg.AuthProxyHeaderName)
	auth := authproxy.New(&authproxy.Options{
		Store: h.RemoteCacheService,
		Ctx:   ctx,
		OrgID: orgID,
	})

	logger := log.New("auth.proxy")

	// Bail if auth proxy is not enabled
	if !auth.IsEnabled() {
		return false
	}

	// If there is no header - we can't move forward
	if !auth.HasHeader() {
		return false
	}

	// Check if allowed to continue with this IP
	if result, err := auth.IsAllowedIP(); !result {
		logger.Error(
			"Failed to check whitelisted IP addresses",
			"message", err.Error(),
			"error", err.DetailsError,
		)
		ctx.Handle(407, err.Error(), err.DetailsError)
		return true
	}

	id, err := logUserIn(auth, username, logger, false)
	if err != nil {
		ctx.Handle(407, err.Error(), err.DetailsError)
		return true
	}

	logger.Debug("Got user ID, getting full user info", "userID", id)

	user, err := auth.GetSignedUser(id)
	if err != nil {
		// The reason we couldn't find the user corresponding to the ID might be that the ID was found from a stale
		// cache entry. For example, if a user is deleted via the API, corresponding cache entries aren't invalidated
		// because cache keys are computed from request header values and not just the user ID. Meaning that
		// we can't easily derive cache keys to invalidate when deleting a user. To work around this, we try to
		// log the user in again without the cache.
		logger.Debug("Failed to get user info given ID, retrying without cache", "userID", id)
		if err := auth.RemoveUserFromCache(logger); err != nil {
			if !errors.Is(err, remotecache.ErrCacheItemNotFound) {
				logger.Error("Got unexpected error when removing user from auth cache", "error", err)
			}
		}
		id, err = logUserIn(auth, username, logger, true)
		if err != nil {
			ctx.Handle(407, err.Error(), err.DetailsError)
			return true
		}

		user, err = auth.GetSignedUser(id)
		if err != nil {
			ctx.Handle(407, err.Error(), err.DetailsError)
			return true
		}
	}

	logger.Debug("Successfully got user info", "userID", user.UserId, "username", user.Login)

	// Add user info to context
	ctx.SignedInUser = user
	ctx.IsSignedIn = true

	// Remember user data in cache
	if err := auth.Remember(id); err != nil {
		logger.Error(
			"Failed to store user in cache",
			"username", username,
			"message", err.Error(),
			"error", err.DetailsError,
		)
		ctx.Handle(500, err.Error(), err.DetailsError)
		return true
	}

	return true
}
