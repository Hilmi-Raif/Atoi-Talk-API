package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type UserController struct {
	userService *service.UserService
}

func NewUserController(userService *service.UserService) *UserController {
	return &UserController{
		userService: userService,
	}
}

// GetCurrentUser godoc
// @Summary      Get Current User
// @Description  Get the currently logged-in user's profile.
// @Tags         user
// @Accept       json
// @Produce      json
// @Success      200  {object}  helper.ResponseSuccess{data=model.UserDTO}
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/user/current [get]
func (c *UserController) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	resp, err := c.userService.GetCurrentUser(r.Context(), userContext.ID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// GetUserProfile godoc
// @Summary      Get User Profile
// @Description  Get another user's profile by ID.
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        id path string true "User ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess{data=model.UserDTO}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/users/{id} [get]
func (c *UserController) GetUserProfile(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	idStr := chi.URLParam(r, "id")
	targetUserID, err := uuid.Parse(idStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid User ID"))
		return
	}

	resp, err := c.userService.GetUserProfile(r.Context(), userContext.ID, targetUserID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// UpdateProfile godoc
// @Summary      Update User Profile
// @Description  Update user's full name, bio, and avatar.
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        request body model.UpdateProfileRequest true "Profile update"
// @Success      200  {object}  helper.ResponseSuccess{data=model.UserDTO}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/user/profile [put]
func (c *UserController) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Error decoding profile update request", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.userService.UpdateProfile(r.Context(), userContext.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// SearchUsers godoc
// @Summary      Search Users
// @Description  Search users by name or email with cursor-based pagination.
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        query query string false "Search query (name or email)"
// @Param        cursor query string false "Pagination cursor"
// @Param        limit query int false "Number of items per page (default 10, max 50)"
// @Param        include_chat_id query boolean false "Include private chat ID if exists"
// @Param        exclude_chat_id query string false "Exclude users who are members of this group chat ID"
// @Success      200  {object}  helper.ResponseWithPagination{data=[]model.UserDTO}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/users [get]
func (c *UserController) SearchUsers(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	query := r.URL.Query().Get("query")
	cursor := r.URL.Query().Get("cursor")
	limitStr := r.URL.Query().Get("limit")
	includeChatIDStr := r.URL.Query().Get("include_chat_id")
	excludeChatIDStr := r.URL.Query().Get("exclude_chat_id")

	limit := 10
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
		limit = l
	}

	includeChatID := false
	if includeChatIDStr != "" {
		b, err := strconv.ParseBool(includeChatIDStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid include_chat_id"))
			return
		}
		includeChatID = b
	}

	var excludeChatID *uuid.UUID
	if excludeChatIDStr != "" {
		if id, err := uuid.Parse(excludeChatIDStr); err == nil {
			excludeChatID = &id
		} else {
			helper.WriteError(w, helper.NewBadRequestError("Invalid exclude_chat_id"))
			return
		}
	}

	req := model.SearchUserRequest{
		Query:         query,
		Cursor:        cursor,
		Limit:         limit,
		IncludeChatID: includeChatID,
		ExcludeChatID: excludeChatID,
	}

	users, nextCursor, hasNext, err := c.userService.SearchUsers(r.Context(), userContext.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPagination(w, users, nextCursor, hasNext)
}

// GetBlockedUsers godoc
// @Summary      Get Blocked Users
// @Description  Get a list of users blocked by the current user.
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        query query string false "Search query (name or username)"
// @Param        cursor query string false "Pagination cursor"
// @Param        limit query int false "Number of items per page (default 10, max 50)"
// @Success      200  {object}  helper.ResponseWithPagination{data=[]model.UserDTO}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/users/blocked [get]
func (c *UserController) GetBlockedUsers(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	query := r.URL.Query().Get("query")
	cursor := r.URL.Query().Get("cursor")
	limitStr := r.URL.Query().Get("limit")

	limit := 10
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
		limit = l
	}

	req := model.GetBlockedUsersRequest{
		Query:  query,
		Cursor: cursor,
		Limit:  limit,
	}

	users, nextCursor, hasNext, err := c.userService.GetBlockedUsers(r.Context(), userContext.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPagination(w, users, nextCursor, hasNext)
}

// BlockUser godoc
// @Summary      Block a User
// @Description  Block a user by their ID.
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        id path string true "User ID to block (UUID)"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/users/{id}/block [post]
func (c *UserController) BlockUser(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	idStr := chi.URLParam(r, "id")
	blockedID, err := uuid.Parse(idStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid User ID"))
		return
	}

	if err := c.userService.BlockUser(r.Context(), userContext.ID, blockedID); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// UnblockUser godoc
// @Summary      Unblock a User
// @Description  Unblock a user by their ID.
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        id path string true "User ID to unblock (UUID)"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/users/{id}/unblock [post]
func (c *UserController) UnblockUser(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	idStr := chi.URLParam(r, "id")
	blockedID, err := uuid.Parse(idStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid User ID"))
		return
	}

	if err := c.userService.UnblockUser(r.Context(), userContext.ID, blockedID); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}
