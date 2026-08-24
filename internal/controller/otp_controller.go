package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
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
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      409  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Router       /api/otp/send [post]
func (c *OTPController) SendOTP(w http.ResponseWriter, r *http.Request) {
	var req model.SendOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Invalid request body", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	err := c.otpService.SendOTP(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}
