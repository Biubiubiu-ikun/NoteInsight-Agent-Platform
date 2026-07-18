package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"creatorinsight/backend-go/internal/api/ctxauth"
	"creatorinsight/backend-go/internal/retrieval"

	"github.com/gin-gonic/gin"
)

type RetrievalHandler struct {
	service      *retrieval.Service
	queryTimeout time.Duration
}

func NewRetrievalHandler(service *retrieval.Service, queryTimeout time.Duration) RetrievalHandler {
	return RetrievalHandler{service: service, queryTimeout: queryTimeout}
}

func (h RetrievalHandler) Search(ctx *gin.Context) {
	var request retrieval.SearchInput
	if err := ctx.ShouldBindJSON(&request); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}
	principal := retrieval.Principal{}
	if currentUser, ok := ctxauth.CurrentUser(ctx); ok {
		principal.UserID = currentUser.ID
	}
	requestContext := ctx.Request.Context()
	if h.queryTimeout > 0 {
		var cancel context.CancelFunc
		requestContext, cancel = context.WithTimeout(requestContext, h.queryTimeout)
		defer cancel()
	}
	response, err := h.service.Search(requestContext, principal, request)
	if err != nil {
		writeRetrievalError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, response)
}

func writeRetrievalError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, retrieval.ErrInvalidInput), errors.Is(err, retrieval.ErrUnsupportedMode):
		writeError(ctx, http.StatusBadRequest, err.Error(), err)
	case errors.Is(err, retrieval.ErrScopeNotFound):
		writeError(ctx, http.StatusNotFound, "retrieval scope not found", err)
	case errors.Is(err, retrieval.ErrIndexNotReady), errors.Is(err, retrieval.ErrIndexVersionMismatch):
		writeError(ctx, http.StatusServiceUnavailable, "retrieval index is not ready", err)
	case errors.Is(err, retrieval.ErrDependencyUnavailable):
		writeError(ctx, http.StatusServiceUnavailable, "retrieval dependency is unavailable", err)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(ctx, http.StatusGatewayTimeout, "retrieval query timed out", err)
	default:
		writeError(ctx, http.StatusInternalServerError, "internal server error", err)
	}
}
