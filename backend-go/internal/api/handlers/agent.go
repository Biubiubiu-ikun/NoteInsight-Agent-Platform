package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"creatorinsight/backend-go/internal/agent"
	"creatorinsight/backend-go/internal/api/ctxauth"
	"creatorinsight/backend-go/internal/auth"

	"github.com/gin-gonic/gin"
)

type AgentHandler struct {
	service *agent.Service
}

func NewAgentHandler(service *agent.Service) AgentHandler {
	return AgentHandler{service: service}
}

type createAgentRunRequest struct {
	ProjectID        int64        `json:"project_id"`
	DatasetVersionID int64        `json:"dataset_version_id" binding:"required"`
	IngestionRunID   string       `json:"ingestion_run_id"`
	Query            string       `json:"query" binding:"required"`
	Mode             string       `json:"mode"`
	Budget           agent.Budget `json:"budget"`
}

func (h AgentHandler) CreateRun(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeAgentError(ctx, auth.ErrUnauthorized)
		return
	}
	var request createAgentRunRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}
	result, err := h.service.CreateRun(ctx.Request.Context(), currentUser, agent.CreateRunInput{
		ProjectID: request.ProjectID, DatasetVersionID: request.DatasetVersionID,
		IngestionRunID: request.IngestionRunID, Query: request.Query, Mode: request.Mode,
		Budget: request.Budget, IdempotencyKey: ctx.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		writeAgentError(ctx, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
		ctx.Header("Idempotent-Replayed", "true")
	}
	ctx.JSON(status, result)
}

func (h AgentHandler) GetRun(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeAgentError(ctx, auth.ErrUnauthorized)
		return
	}
	run, err := h.service.GetRun(ctx.Request.Context(), currentUser, ctx.Param("run_id"))
	if err != nil {
		writeAgentError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, run)
}

func (h AgentHandler) ListRuns(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeAgentError(ctx, auth.ErrUnauthorized)
		return
	}
	limit := 0
	if rawLimit := ctx.Query("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeAgentError(ctx, agent.ErrInvalidInput)
			return
		}
		limit = parsed
	}
	page, err := h.service.ListRuns(ctx.Request.Context(), currentUser, agent.ListRunsInput{
		Limit: limit, Cursor: ctx.Query("cursor"),
	})
	if err != nil {
		writeAgentError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}

func (h AgentHandler) CancelRun(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeAgentError(ctx, auth.ErrUnauthorized)
		return
	}
	run, err := h.service.CancelRun(ctx.Request.Context(), currentUser, ctx.Param("run_id"))
	if err != nil {
		writeAgentError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, run)
}

func writeAgentError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, agent.ErrInvalidInput):
		writeError(ctx, http.StatusBadRequest, err.Error(), err)
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(ctx, http.StatusUnauthorized, "unauthorized", err)
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, agent.ErrForbidden):
		writeError(ctx, http.StatusForbidden, "forbidden", err)
	case errors.Is(err, agent.ErrNotFound):
		writeError(ctx, http.StatusNotFound, "agent run not found", err)
	case errors.Is(err, agent.ErrConflict), errors.Is(err, agent.ErrIdempotencyConflict):
		writeError(ctx, http.StatusConflict, err.Error(), err)
	case errors.Is(err, agent.ErrScopeUnavailable):
		writeError(ctx, http.StatusServiceUnavailable, "agent retrieval scope is unavailable", err)
	default:
		writeError(ctx, http.StatusInternalServerError, "internal server error", err)
	}
}
