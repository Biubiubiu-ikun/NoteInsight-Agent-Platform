package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"creatorinsight/backend-go/internal/api/ctxauth"
	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/note"

	"github.com/gin-gonic/gin"
)

type NoteHandler struct {
	service *note.Service
}

func NewNoteHandler(service *note.Service) NoteHandler {
	return NoteHandler{service: service}
}

type createNoteRequest struct {
	ProjectID       int64                    `json:"project_id"`
	AuthorID        int64                    `json:"author_id"`
	Title           string                   `json:"title" binding:"required"`
	Body            string                   `json:"body" binding:"required"`
	Category        string                   `json:"category" binding:"required"`
	Topics          []string                 `json:"topics"`
	Tags            []string                 `json:"tags"`
	Location        map[string]any           `json:"location"`
	ProductEntities []string                 `json:"product_entities"`
	Media           []createNoteMediaRequest `json:"media"`
}

type createNoteMediaRequest struct {
	MediaType string         `json:"media_type"`
	URL       string         `json:"url"`
	Caption   string         `json:"caption"`
	OCRText   string         `json:"ocr_text"`
	Position  int            `json:"position"`
	Metadata  map[string]any `json:"metadata"`
}

type updateNoteRequest struct {
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	Category *string `json:"category"`
}

type createNoteCommentRequest struct {
	ParentID int64  `json:"parent_id"`
	Content  string `json:"content" binding:"required"`
	Intent   string `json:"intent"`
}

type collectNoteRequest struct {
	CollectionName string `json:"collection_name"`
}

type shareNoteRequest struct {
	Channel string `json:"channel"`
}

func (h NoteHandler) CreateNote(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	var req createNoteRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	media := make([]note.CreateNoteMediaInput, 0, len(req.Media))
	for _, item := range req.Media {
		media = append(media, note.CreateNoteMediaInput{
			MediaType: item.MediaType,
			URL:       item.URL,
			Caption:   item.Caption,
			OCRText:   item.OCRText,
			Position:  item.Position,
			Metadata:  item.Metadata,
		})
	}

	created, err := h.service.CreateNote(ctx.Request.Context(), note.CreateNoteInput{
		ProjectID:       req.ProjectID,
		AuthorID:        currentUser.ID,
		Title:           req.Title,
		Body:            req.Body,
		Category:        req.Category,
		Topics:          req.Topics,
		Tags:            req.Tags,
		Location:        req.Location,
		ProductEntities: req.ProductEntities,
		Media:           media,
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, created)
}

func (h NoteHandler) UpdateNote(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	var req updateNoteRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	updated, err := h.service.UpdateNote(ctx.Request.Context(), currentUser, note.UpdateNoteInput{
		NoteID:   ctx.Param("note_id"),
		Title:    req.Title,
		Body:     req.Body,
		Category: req.Category,
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, updated)
}

func (h NoteHandler) DeleteNote(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	if err := h.service.DeleteNote(ctx.Request.Context(), currentUser, ctx.Param("note_id")); err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h NoteHandler) GetNote(ctx *gin.Context) {
	found, err := h.service.GetNote(ctx.Request.Context(), ctx.Param("note_id"))
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, found)
}

func (h NoteHandler) ListNotes(ctx *gin.Context) {
	page, err := h.service.ListNotes(ctx.Request.Context(), note.ListNotesInput{
		Category: ctx.Query("category"),
		Limit:    parseQueryInt(ctx, "limit", 0),
		Cursor:   ctx.Query("cursor"),
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, page)
}

func (h NoteHandler) CreateComment(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	var req createNoteCommentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	comment, err := h.service.CreateComment(ctx.Request.Context(), note.CreateCommentInput{
		NoteID:   ctx.Param("note_id"),
		UserID:   currentUser.ID,
		ParentID: req.ParentID,
		Content:  req.Content,
		Intent:   req.Intent,
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, comment)
}

func (h NoteHandler) DeleteComment(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	if err := h.service.DeleteComment(ctx.Request.Context(), currentUser, ctx.Param("comment_id")); err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h NoteHandler) ListComments(ctx *gin.Context) {
	page, err := h.service.ListComments(ctx.Request.Context(), note.ListCommentsInput{
		NoteID: ctx.Param("note_id"),
		Limit:  parseQueryInt(ctx, "limit", 0),
		Cursor: ctx.Query("cursor"),
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, page)
}

func (h NoteHandler) ListHotNotes(ctx *gin.Context) {
	page, err := h.service.ListHotNotes(ctx.Request.Context(), ctx.Query("category"), parseQueryInt(ctx, "limit", 50))
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, page)
}

func (h NoteHandler) LikeNote(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	result, err := h.service.LikeNote(ctx.Request.Context(), note.UserActionInput{
		ResourceID: ctx.Param("note_id"),
		UserID:     currentUser.ID,
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func (h NoteHandler) CollectNote(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	var req collectNoteRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := h.service.CollectNote(ctx.Request.Context(), note.CollectNoteInput{
		NoteID:         ctx.Param("note_id"),
		UserID:         currentUser.ID,
		CollectionName: req.CollectionName,
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func (h NoteHandler) ShareNote(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	var req shareNoteRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeError(ctx, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := h.service.ShareNote(ctx.Request.Context(), note.ShareNoteInput{
		NoteID:  ctx.Param("note_id"),
		UserID:  currentUser.ID,
		Channel: req.Channel,
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func (h NoteHandler) LikeComment(ctx *gin.Context) {
	currentUser, ok := ctxauth.CurrentUser(ctx)
	if !ok {
		writeNoteError(ctx, auth.ErrUnauthorized)
		return
	}

	result, err := h.service.LikeComment(ctx.Request.Context(), note.UserActionInput{
		ResourceID: ctx.Param("comment_id"),
		UserID:     currentUser.ID,
	})
	if err != nil {
		writeNoteError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func parseQueryInt(ctx *gin.Context, key string, fallback int) int {
	raw := ctx.Query(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func writeNoteError(ctx *gin.Context, err error) {
	var validationErr note.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(ctx, http.StatusBadRequest, validationErr.Error(), err)
	case errors.Is(err, note.ErrInvalidInput):
		writeError(ctx, http.StatusBadRequest, "invalid input", err)
	case errors.Is(err, note.ErrNotFound):
		writeError(ctx, http.StatusNotFound, "resource not found", err)
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(ctx, http.StatusUnauthorized, "unauthorized", err)
	case errors.Is(err, auth.ErrForbidden):
		writeError(ctx, http.StatusForbidden, "forbidden", err)
	default:
		writeError(ctx, http.StatusInternalServerError, "internal server error", err)
	}
}

func writeError(ctx *gin.Context, status int, message string, err error) {
	response := gin.H{"error": message}
	if gin.Mode() != gin.ReleaseMode && err != nil {
		response["detail"] = err.Error()
	}
	ctx.JSON(status, response)
}
