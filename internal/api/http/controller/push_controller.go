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

// Device godoc
// @Summary Register or disable a push device
// @Description PUT registers a device with an installation secret; DELETE disables the current session binding.
// @Tags notifications
// @Accept json
// @Produce json
// @Param installationID path string true "Installation UUID"
// @Param body body model.PushDeviceRequest false "Required for PUT only"
// @Success 200 {object} response.ResponseSuccess
// @Failure 400,401,403,404,409,503 {object} response.ResponseError
// @Security BearerAuth
// @Router /api/push/devices/{installationID} [put]
// @Router /api/push/devices/{installationID} [delete]
func (c *PushController) Device(w http.ResponseWriter, r *http.Request) {
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
	if r.Method == http.MethodDelete {
		err = c.service.Unregister(r.Context(), u.ID, token, installation)
	} else {
		var req model.PushDeviceRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&req); decodeErr != nil {
			response.WriteError(w, helper.NewBadRequestError("Invalid registration"))
			return
		}
		err = c.service.Upsert(r.Context(), u.ID, token, installation, req)
	}
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteSuccess(w, nil)
}
