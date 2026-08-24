package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"encoding/json"
	"net/http"
)

type AccountController struct {
	accountService service.AccountServicePort
}

func NewAccountController(accountService service.AccountServicePort) *AccountController {
	return &AccountController{
		accountService: accountService,
	}
}

// ChangePassword godoc
// @Summary      Change Password
// @Description  Change user password. Requires old password if one is set.
// @Tags         account
// @Accept       json
// @Produce      json
// @Param        request body model.ChangePasswordRequest true "Change Password Request"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/account/password [put]
func (c *AccountController) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	if err := c.accountService.ChangePassword(r.Context(), userContext.ID, req); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// ChangeEmail godoc
// @Summary      Change Email
// @Description  Change user email. Requires OTP verification.
// @Tags         account
// @Accept       json
// @Produce      json
// @Param        request body model.ChangeEmailRequest true "Change Email Request"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      409  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/account/email [put]
func (c *AccountController) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.ChangeEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	if err := c.accountService.ChangeEmail(r.Context(), userContext.ID, req); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// DeleteAccount godoc
// @Summary      Delete Account
// @Description  Soft delete user account. Requires password confirmation if set. User must transfer ownership of groups first.
// @Tags         account
// @Accept       json
// @Produce      json
// @Param        request body model.DeleteAccountRequest true "Delete Account Request"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/account [delete]
func (c *AccountController) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.DeleteAccountRequest

	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			helper.WriteError(w, helper.NewBadRequestError(""))
			return
		}
	}

	if err := c.accountService.DeleteAccount(r.Context(), userContext.ID, req); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}
