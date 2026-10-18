package controller

import (
	"AtoiTalkAPI/internal/api/http/middleware"
	"AtoiTalkAPI/internal/api/http/response"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
)

type PushService interface {
	Upsert(context.Context, uuid.UUID, string, uuid.UUID, model.PushDeviceRequest) error
	Unregister(context.Context, uuid.UUID, string, uuid.UUID) error
}
type PushController struct{ service PushService }

func NewPushController(service PushService) *PushController { return &PushController{service: service} }

// RegisterDevice godoc
// @Summary Save push device
// @Description Registers an installation or updates its token and session binding using its installation secret.
// @Tags notifications
// @Accept json
// @Produce json
// @Param installationID path string true "Installation UUID"
// @Param body body model.PushDeviceRequest true "Device registration"
// @Success 200 {object} response.ResponseSuccess
// @Failure 400,401,403,409,500,503 {object} response.ResponseError
// @Security BearerAuth
// @Router /api/push/devices/{installationID} [put]
func (c *PushController) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	u, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		response.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	token, ok := r.Context().Value(middleware.TokenContextKey).(string)
	if !ok {
		response.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	installation, err := uuid.Parse(chi.URLParam(r, "installationID"))
	if err != nil || installation == uuid.Nil {
		response.WriteError(w, helper.NewBadRequestError("Invalid installation ID"))
		return
	}

	var req model.PushDeviceRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		response.WriteError(w, helper.NewBadRequestError("Invalid registration"))
		return
	}
	if err = c.service.Upsert(r.Context(), u.ID, token, installation, req); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteSuccess(w, nil)
}

// UnregisterDevice godoc
// @Summary Disable push device
// @Description Disables the installation binding belonging to the current session.
// @Tags notifications
// @Produce json
// @Param installationID path string true "Installation UUID"
// @Success 200 {object} response.ResponseSuccess
// @Failure 400,401,404,500,503 {object} response.ResponseError
// @Security BearerAuth
// @Router /api/push/devices/{installationID} [delete]
func (c *PushController) UnregisterDevice(w http.ResponseWriter, r *http.Request) {
	u, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		response.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	token, ok := r.Context().Value(middleware.TokenContextKey).(string)
	if !ok {
		response.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}
	installation, err := uuid.Parse(chi.URLParam(r, "installationID"))
	if err != nil || installation == uuid.Nil {
		response.WriteError(w, helper.NewBadRequestError("Invalid installation ID"))
		return
	}

	if err = c.service.Unregister(r.Context(), u.ID, token, installation); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteSuccess(w, nil)
}
