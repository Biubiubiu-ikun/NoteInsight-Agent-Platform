package api

import (
	"log/slog"
	"net/http"

	"creatorinsight/backend-go/internal/api/handlers"
	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/note"
	"creatorinsight/backend-go/internal/platform/ratelimit"
	"creatorinsight/backend-go/internal/retrieval"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
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
	if err := router.SetTrustedProxies(deps.Config.HTTP.TrustedProxies); err != nil {
		panic(err)
	}
	router.Use(ResponseCompression())
	router.Use(gin.Recovery())
	router.Use(CORS(deps.Config.HTTP.AllowedOrigins))
	router.Use(DBPoolMetrics(deps.DB))
	if deps.Config.Telemetry.Enabled {
		router.Use(otelgin.Middleware(deps.Config.App.Name, otelgin.WithFilter(func(request *http.Request) bool {
			switch request.URL.Path {
			case "/health", "/ready", "/metrics", "/api/v1/health", "/api/v1/ready":
				return false
			default:
				return true
			}
		})))
	}
	router.Use(RequestLogger(deps.Logger))

	authService := auth.NewService(auth.NewRepository(deps.DB), deps.Config.Auth, deps.Config.App.Env)
	noteService := note.NewService(note.NewRepository(deps.DB), deps.Redis)
	retrievalService := retrieval.NewService(retrieval.NewRepository(deps.DB))
	if err := retrievalService.EnableVector(
		retrieval.NewTEIEmbedder(
			deps.Config.Retrieval.EmbeddingURL, deps.Config.Retrieval.EmbeddingModel,
			deps.Config.Retrieval.EmbeddingRevision, deps.Config.Retrieval.EmbeddingDimension,
			deps.Config.Retrieval.DependencyTimeout,
		),
		retrieval.NewQdrantClient(
			deps.Config.Retrieval.QdrantURL, deps.Config.Retrieval.QdrantAPIKey,
			deps.Config.Retrieval.DependencyTimeout,
		),
	); err != nil {
		panic(err)
	}
	writeLimiter := ratelimit.New(deps.Redis)
	authPolicy := ratelimit.Policy{
		Name:   "auth",
		Limit:  deps.Config.RateLimit.Auth.Limit,
		Window: deps.Config.RateLimit.Auth.Window,
	}
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
	retrievalReadPolicy := ratelimit.Policy{
		Name:   "retrieval_read",
		Limit:  deps.Config.RateLimit.RetrievalRead.Limit,
		Window: deps.Config.RateLimit.RetrievalRead.Window,
	}
	rateLimitEnabled := deps.Config.RateLimit.Enabled

	router.Use(AuthMiddleware(authService))

	healthHandler := handlers.NewHealthHandler(handlers.HealthDeps{
		DB:    deps.DB,
		Redis: deps.Redis,
	})
	authHandler := handlers.NewAuthHandler(
		authService,
		deps.Config.App.Env == "prod",
		int(deps.Config.Auth.RefreshTokenTTL.Seconds()),
	)
	noteHandler := handlers.NewNoteHandler(noteService)
	retrievalHandler := handlers.NewRetrievalHandler(retrievalService, deps.Config.Retrieval.QueryTimeout)

	router.GET("/health", healthHandler.Health)
	router.GET("/ready", healthHandler.Ready)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	v1 := router.Group("/api/v1")
	v1.GET("/health", healthHandler.Health)
	v1.GET("/ready", healthHandler.Ready)

	v1.POST("/auth/register", IPRateLimit(writeLimiter, rateLimitEnabled, authPolicy), authHandler.Register)
	v1.POST("/auth/login", IPRateLimit(writeLimiter, rateLimitEnabled, authPolicy), authHandler.Login)
	v1.POST("/auth/refresh", IPRateLimit(writeLimiter, rateLimitEnabled, authPolicy), authHandler.Refresh)
	v1.POST("/auth/logout", IPRateLimit(writeLimiter, rateLimitEnabled, authPolicy), authHandler.Logout)
	v1.GET("/me", RequireAuth(), authHandler.Me)
	v1.PATCH("/me", RequireAuth(), RequireActiveUser(), AuditMutation(deps.Logger, "user.profile.update"), authHandler.UpdateMe)

	v1.GET("/notes", noteHandler.ListNotes)
	v1.GET("/notes/:note_id", noteHandler.GetNote)
	v1.GET("/notes/:note_id/comments", noteHandler.ListComments)
	v1.GET("/rankings/notes/daily", noteHandler.ListHotNotes)
	v1.POST("/retrieval/search", IPRateLimit(writeLimiter, rateLimitEnabled, retrievalReadPolicy), retrievalHandler.Search)
	v1.POST("/notes", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, contentWritePolicy), noteHandler.CreateNote)
	v1.PATCH("/notes/:note_id", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, contentWritePolicy), RequireOwnerOrAdmin(func(ctx *gin.Context, currentUser auth.CurrentUser) (bool, error) {
		return noteService.CanModifyNote(ctx.Request.Context(), ctx.Param("note_id"), currentUser)
	}), AuditMutation(deps.Logger, "note.update"), noteHandler.UpdateNote)
	v1.DELETE("/notes/:note_id", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, contentWritePolicy), RequireOwnerOrAdmin(func(ctx *gin.Context, currentUser auth.CurrentUser) (bool, error) {
		return noteService.CanModifyNote(ctx.Request.Context(), ctx.Param("note_id"), currentUser)
	}), AuditMutation(deps.Logger, "note.delete"), noteHandler.DeleteNote)
	v1.POST("/notes/:note_id/comments", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, commentWritePolicy), noteHandler.CreateComment)
	v1.DELETE("/comments/:comment_id", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, commentWritePolicy), RequireOwnerOrAdmin(func(ctx *gin.Context, currentUser auth.CurrentUser) (bool, error) {
		return noteService.CanModifyComment(ctx.Request.Context(), ctx.Param("comment_id"), currentUser)
	}), AuditMutation(deps.Logger, "comment.delete"), noteHandler.DeleteComment)
	v1.POST("/notes/:note_id/like", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.LikeNote)
	v1.DELETE("/notes/:note_id/like", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.UnlikeNote)
	v1.POST("/notes/:note_id/collect", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.CollectNote)
	v1.DELETE("/notes/:note_id/collect", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.UncollectNote)
	v1.POST("/notes/:note_id/share", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.ShareNote)
	v1.POST("/comments/:comment_id/like", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.LikeComment)
	v1.DELETE("/comments/:comment_id/like", RequireAuth(), RequireActiveUser(), UserRateLimit(writeLimiter, rateLimitEnabled, interactionWritePolicy), noteHandler.UnlikeComment)

	return router
}
