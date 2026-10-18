package controller

import (
	pushapp "AtoiTalkAPI/internal/api/application/push"
	"AtoiTalkAPI/internal/api/http/middleware"
	"AtoiTalkAPI/internal/api/http/response"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"strconv"
)

type NotificationController struct{ service *pushapp.Service }

func NewNotificationController(s *pushapp.Service) *NotificationController {
	return &NotificationController{s}
}

// Settings godoc
// @Summary Read or update account notification preferences
// @Description GET returns message_notifications_enabled. PUT requires that boolean field and preserves chat mutes.
// @Tags notifications
// @Accept json
// @Produce json
// @Param body body pushapp.Preferences false "Required for PUT only"
// @Success 200 {object} response.ResponseSuccess{data=pushapp.Preferences}
// @Failure 400,401,500 {object} response.ResponseError
// @Security BearerAuth
// @Router /api/notifications [get]
// @Router /api/notifications [put]
func (c *NotificationController) Settings(w http.ResponseWriter, r *http.Request) {
	u, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		response.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	if r.Method == http.MethodGet {
		v, err := c.service.Preferences(r.Context(), u.ID)
		if err != nil {
			response.WriteError(w, err)
			return
		}
		response.WriteSuccess(w, v)
		return
	}
	var req struct {
		Enabled *bool `json:"message_notifications_enabled"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || req.Enabled == nil {
		response.WriteError(w, helper.NewBadRequestError("Provide message_notifications_enabled"))
		return
	}
	if err := c.service.SetGlobal(r.Context(), u.ID, *req.Enabled); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteSuccess(w, nil)
}

// Chat godoc
// @Summary Mute or unmute a conversation
// @Description Set muted_forever or a future muted_until within 365 days; an empty object unmutes. Requires current membership.
// @Tags notifications
// @Accept json
// @Produce json
// @Param chatID path string true "Chat UUID"
// @Param body body pushapp.MuteRequest true "Mute preference"
// @Success 200 {object} response.ResponseSuccess
// @Failure 400,401,403,404,500 {object} response.ResponseError
// @Security BearerAuth
// @Router /api/chats/{chatID}/notifications [put]
func (c *NotificationController) Chat(w http.ResponseWriter, r *http.Request) {
	u, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		response.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "chatID"))
	if err != nil || id == uuid.Nil {
		response.WriteError(w, helper.NewBadRequestError("Invalid chat"))
		return
	}
	var req pushapp.MuteRequest
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil {
		response.WriteError(w, helper.NewBadRequestError("Invalid mute preference"))
		return
	}
	if err = c.service.SetChatMute(r.Context(), u.ID, id, req); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteSuccess(w, nil)
}

// MutedChats godoc
// @Summary List active muted conversations
// @Tags notifications
// @Produce json
// @Param cursor query string false "Pagination cursor"
// @Param limit query int false "Page size (default 20, maximum 50)"
// @Success 200 {object} response.ResponseWithPagination{data=[]pushapp.ChatPreference}
// @Failure 400,401,500 {object} response.ResponseError
// @Security BearerAuth
// @Router /api/notifications/muted-chats [get]
func (c *NotificationController) MutedChats(w http.ResponseWriter, r *http.Request) {
	u, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		response.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil {
			response.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
	}
	rows, next, more, err := c.service.MutedChats(r.Context(), u.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteSuccessWithPagination(w, rows, next, more)
}
