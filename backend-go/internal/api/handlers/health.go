package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

type HealthDeps struct {
	DB    *sqlx.DB
	Redis *redis.Client
}

type HealthHandler struct {
	db    *sqlx.DB
	redis *redis.Client
}

func NewHealthHandler(deps HealthDeps) HealthHandler {
	return HealthHandler{
		db:    deps.DB,
		redis: deps.Redis,
	}
}

func (h HealthHandler) Health(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

func (h HealthHandler) Ready(ctx *gin.Context) {
	checkCtx, cancel := context.WithTimeout(ctx.Request.Context(), 2*time.Second)
	defer cancel()

	checks := gin.H{
		"postgres": "ok",
		"redis":    "ok",
	}
	status := http.StatusOK

	if err := h.db.PingContext(checkCtx); err != nil {
		status = http.StatusServiceUnavailable
		checks["postgres"] = "unavailable"
	}
	if err := h.redis.Ping(checkCtx).Err(); err != nil {
		status = http.StatusServiceUnavailable
		checks["redis"] = "unavailable"
	}

	ctx.JSON(status, gin.H{
		"status": checks,
	})
}
