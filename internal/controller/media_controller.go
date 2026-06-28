package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type MediaController struct {
	mediaService *service.MediaService
}

func NewMediaController(mediaService *service.MediaService) *MediaController {
	return &MediaController{
		mediaService: mediaService,
	}
}

// UploadMedia godoc
// @Summary      Create Media Upload URL
// @Description  Create a pending media row and upload URL for direct storage upload.
// @Tags         media
// @Accept       json
// @Produce      json
// @Param        request body model.UploadMediaRequest true "Upload request"
// @Success      200  {object}  helper.ResponseSuccess{data=model.UploadMediaResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/media/upload [post]
func (c *MediaController) UploadMedia(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.UploadMediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.mediaService.UploadMedia(r.Context(), user.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// CompleteUpload godoc
// @Summary      Complete Media Upload
// @Description  Verify the uploaded object exists and mark pending media as completed.
// @Tags         media
// @Accept       json
// @Produce      json
// @Param        mediaID path string true "Media ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess{data=model.MediaDTO}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/media/{mediaID}/complete [post]
func (c *MediaController) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	mediaIDStr := chi.URLParam(r, "mediaID")
	mediaID, err := uuid.Parse(mediaIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Media ID"))
		return
	}

	resp, err := c.mediaService.CompleteUpload(r.Context(), user.ID, mediaID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// GetMediaURL godoc
// @Summary      Refresh Media URL
// @Description  Get a new presigned URL for a media file if the previous one has expired.
// @Tags         media
// @Accept       json
// @Produce      json
// @Param        mediaID path string true "Media ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess{data=model.MediaURLResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/media/{mediaID}/url [get]
func (c *MediaController) GetMediaURL(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	mediaIDStr := chi.URLParam(r, "mediaID")
	mediaID, err := uuid.Parse(mediaIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Media ID"))
		return
	}

	resp, err := c.mediaService.GetMediaURL(r.Context(), user.ID, mediaID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}
