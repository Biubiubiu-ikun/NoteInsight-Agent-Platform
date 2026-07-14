package api

import (
	"log/slog"

	"creatorinsight/backend-go/internal/api/handlers"
	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/note"
	"creatorinsight/backend-go/internal/platform/ratelimit"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

type RouterDeps struct {
	Config config.Config
	Logger *slog.Logger
	DB     *sqlx.DB
	Redis  *redis.Client
}

func NewRouter(deps RouterDeps) *gin.Engine {
	switch deps.Config.App.Env {
	case "prod":
		gin.SetMode(gin.ReleaseMode)
	case "test":
		gin.SetMode(gin.TestMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(RequestLogger(deps.Logger))

	authService := auth.NewService(auth.NewRepository(deps.DB), deps.Config.Auth, deps.Config.App.Env)
	noteService := note.NewService(note.NewRepository(deps.DB), deps.Redis)
	writeLimiter := ratelimit.New(deps.Redis)
	contentWritePolicy := ratelimit.Policy{
		Name:   "content_write",
		Limit:  deps.Config.RateLimit.ContentWrite.Limit,
		Window: deps.Config.RateLimit.ContentWrite.Window,
	}
	commentWritePolicy := ratelimit.Policy{
		Name:   "comment_write",
		Limit:  deps.Config.RateLimit.CommentWrite.Limit,
		Window: deps.Config.RateLimit.CommentWrite.Window,
	}
	interactionWritePolicy := ratelimit.Policy{
		Name:   "interaction_write",
		Limit:  deps.Config.RateLimit.InteractionWrite.Limit,
		Window: deps.Config.RateLimit.InteractionWrite.Window,
	}
	rateLimitEnabled := deps.Config.RateLimit.Enabled

	router.Use(AuthMiddleware(authService))

	healthHandler := handlers.NewHealthHandler(handlers.HealthDeps{
		DB:    deps.DB,
		Redis: deps.Redis,
	})
	authHandler := handlers.NewAuthHandler(authService)
	noteHandler := handlers.NewNoteHandler(noteService)

	router.GET("/health", healthHandler.Health)
	router.GET("/ready", healthHandler.Ready)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	v1 := router.Group("/api/v1")
	v1.GET("/health", healthHandler.Health)
	v1.GET("/ready", healthHandler.Ready)

	v1.POST("/auth/register", authHandler.Register)
	v1.POST("/auth/login", authHandler.Login)
	v1.POST("/auth/refresh", authHandler.Refresh)
	v1.POST("/auth/logout", RequireAuth(), authHandler.Logout)
	v1.GET("/me", RequireAuth(), authHandler.Me)
	v1.PATCH("/me", RequireAuth(), RequireActiveUser(), authHandler.UpdateMe)

	v1.GET("/notes", noteHandler.ListNotes)
	v1.GET("/notes/:note_id", noteHandler.GetNote)
	v1.GET("/notes/:note_id/comments", noteHandler.ListComments)
	v1.GET("/rankings/notes/daily", noteHandler.ListHotNotes)
	v1.POST("/notes", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, contentWritePolicy), noteHandler.CreateNote)
	v1.PATCH("/notes/:note_id", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, contentWritePolicy), RequireOwnerOrAdmin(func(ctx *gin.Context, currentUser auth.CurrentUser) (bool, error) {
		return noteService.CanModifyNote(ctx.Request.Context(), ctx.Param("note_id"), currentUser)
	}), noteHandler.UpdateNote)
	v1.DELETE("/notes/:note_id", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, contentWritePolicy), RequireOwnerOrAdmin(func(ctx *gin.Context, currentUser auth.CurrentUser) (bool, error) {
		return noteService.CanModifyNote(ctx.Request.Context(), ctx.Param("note_id"), currentUser)
	}), noteHandler.DeleteNote)
	v1.POST("/notes/:note_id/comments", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, commentWritePolicy), noteHandler.CreateComment)
	v1.DELETE("/comments/:comment_id", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, commentWritePolicy), RequireOwnerOrAdmin(func(ctx *gin.Context, currentUser auth.CurrentUser) (bool, error) {
		return noteService.CanModifyComment(ctx.Request.Context(), ctx.Param("comment_id"), currentUser)
	}), noteHandler.DeleteComment)
	v1.POST("/notes/:note_id/like", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.LikeNote)
	v1.POST("/notes/:note_id/collect", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.CollectNote)
	v1.POST("/notes/:note_id/share", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.ShareNote)
	v1.POST("/comments/:comment_id/like", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.LikeComment)

	return router
}
