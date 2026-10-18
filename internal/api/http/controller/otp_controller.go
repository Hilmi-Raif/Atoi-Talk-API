package controller

import (
	service "AtoiTalkAPI/internal/api/application"
	"AtoiTalkAPI/internal/api/http/response"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"encoding/json"
	"log/slog"
	"net/http"
)

type OTPController struct {
	otpService service.OTPServicePort
}

func NewOTPController(otpService service.OTPServicePort) *OTPController {
	return &OTPController{
		otpService: otpService,
	}
}

// SendOTP godoc
// @Summary      Send OTP
// @Description  Sends an OTP code to the user's email for registration or password reset.
// @Tags         otp
// @Accept       json
// @Produce      json
// @Param        request body model.SendOTPRequest true "Send OTP Request"
// @Success      200  {object}  response.ResponseSuccess
// @Failure      400  {object}  response.ResponseError
// @Failure      404  {object}  response.ResponseError
// @Failure      409  {object}  response.ResponseError
// @Failure      429  {object}  response.ResponseError
// @Failure      500  {object}  response.ResponseError
// @Router       /api/otp/send [post]
func (c *OTPController) SendOTP(w http.ResponseWriter, r *http.Request) {
	var req model.SendOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.WarnContext(r.Context(), "Invalid request body", "error", err)
		response.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	err := c.otpService.SendOTP(r.Context(), req)
	if err != nil {
		response.WriteError(w, err)
		return
	}

	response.WriteSuccess(w, nil)
}
