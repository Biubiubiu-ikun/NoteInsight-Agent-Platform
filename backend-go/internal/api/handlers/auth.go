package handlers

import (
	"errors"
	"net/http"

	"creatorinsight/backend-go/internal/api/ctxauth"
	"creatorinsight/backend-go/internal/auth"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	service *auth.Service
}

func NewAuthHandler(service *auth.Service) AuthHandler {
	return AuthHandler{service: service}
}

type registerRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Nickname string `json:"nickname"`
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type updateMeRequest struct {
	Nickname  *string `json:"nickname"`
	AvatarURL *string `json:"avatar_url"`
	Bio       *string `json:"bio"`
}

func (h AuthHandler) Register(ctx *gin.Context) {
	var req registerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := h.service.Register(ctx.Request.Context(), auth.RegisterInput{
		Username:  req.Username,
		Password:  req.Password,
		Nickname:  req.Nickname,
		UserAgent: ctx.Request.UserAgent(),
		IPAddress: ctx.ClientIP(),
	})
	if err != nil {
		writeAuthError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, result)
}

func (h AuthHandler) Login(ctx *gin.Context) {
	var req loginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := h.service.Login(ctx.Request.Context(), auth.LoginInput{
		Username:  req.Username,
		Password:  req.Password,
		UserAgent: ctx.Request.UserAgent(),
		IPAddress: ctx.ClientIP(),
	})
	if err != nil {
		writeAuthError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func (h AuthHandler) Refresh(ctx *gin.Context) {
	var req refreshRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := h.service.Refresh(ctx.Request.Context(), auth.RefreshInput{
		RefreshToken: req.RefreshToken,
		UserAgent:    ctx.Request.UserAgent(),
		IPAddress:    ctx.ClientIP(),
	})
	if err != nil {
		writeAuthError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func (h AuthHandler) Logout(ctx *gin.Context) {
	var req logoutRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if err := h.service.Logout(ctx.Request.Context(), auth.LogoutInput{RefreshToken: req.RefreshToken}); err != nil {
		writeAuthError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h AuthHandler) Me(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeAuthError(ctx, auth.ErrUnauthorized)
		return
	}

	user, err := h.service.GetUser(ctx.Request.Context(), currentUser.ID)
	if err != nil {
		writeAuthError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, user)
}

func (h AuthHandler) UpdateMe(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeAuthError(ctx, auth.ErrUnauthorized)
		return
	}

	var req updateMeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	user, err := h.service.UpdateProfile(ctx.Request.Context(), currentUser, auth.UpdateProfileInput{
		Nickname:  req.Nickname,
		AvatarURL: req.AvatarURL,
		Bio:       req.Bio,
	})
	if err != nil {
		writeAuthError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, user)
}

func writeAuthError(ctx *gin.Context, err error) {
	var validationErr auth.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(ctx, http.StatusBadRequest, validationErr.Error(), err)
	case errors.Is(err, auth.ErrUsernameExists):
		writeError(ctx, http.StatusConflict, "username already exists", err)
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(ctx, http.StatusUnauthorized, "unauthorized", err)
	case errors.Is(err, auth.ErrForbidden):
		writeError(ctx, http.StatusForbidden, "forbidden", err)
	case errors.Is(err, auth.ErrNotFound):
		writeError(ctx, http.StatusNotFound, "resource not found", err)
	default:
		writeError(ctx, http.StatusInternalServerError, "internal server error", err)
	}
}
