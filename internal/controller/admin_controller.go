package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type AdminController struct {
	adminService     service.AdminServicePort
	groupChatService service.GroupChatServicePort
	validator        *validator.Validate
}

func NewAdminController(adminService service.AdminServicePort, groupChatService service.GroupChatServicePort, validator *validator.Validate) *AdminController {
	return &AdminController{
		adminService:     adminService,
		groupChatService: groupChatService,
		validator:        validator,
	}
}

// BanUser godoc
// @Summary      Ban User
// @Description  Ban a user permanently or temporarily. Requires Admin privileges.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        request body model.BanUserRequest true "Ban Request"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/users/ban [post]
func (c *AdminController) BanUser(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.BanUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	if err := c.adminService.BanUser(r.Context(), userContext.ID, req); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// UnbanUser godoc
// @Summary      Unban User
// @Description  Lift a ban from a user. Requires Admin privileges.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        userID path string true "User ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/users/{userID}/unban [post]
func (c *AdminController) UnbanUser(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	userIDStr := chi.URLParam(r, "userID")
	targetUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid User ID"))
		return
	}

	if err := c.adminService.UnbanUser(r.Context(), userContext.ID, targetUserID); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// GetReports godoc
// @Summary      Get Reports
// @Description  Get a list of reports. Requires Admin privileges.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        query query string false "Search query (reason, reporter name/username)"
// @Param        status query string false "Filter by status (pending, reviewed, resolved, rejected)"
// @Param        limit query int false "Limit (default 20)"
// @Param        cursor query string false "Cursor for pagination"
// @Success      200  {object}  helper.ResponseWithPagination{data=[]model.ReportListResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/reports [get]
func (c *AdminController) GetReports(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	query := r.URL.Query().Get("query")
	status := r.URL.Query().Get("status")
	cursor := r.URL.Query().Get("cursor")
	limitStr := r.URL.Query().Get("limit")

	limit := 20
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
		limit = l
	}

	req := model.GetReportsRequest{
		Query:  query,
		Status: status,
		Cursor: cursor,
		Limit:  limit,
	}

	reports, nextCursor, hasNext, err := c.adminService.GetReports(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPagination(w, reports, nextCursor, hasNext)
}

// GetReportDetail godoc
// @Summary      Get Report Detail
// @Description  Get detailed information about a report including evidence snapshot. Requires Admin privileges.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        reportID path string true "Report ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess{data=model.ReportDetailResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/reports/{reportID} [get]
func (c *AdminController) GetReportDetail(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	reportIDStr := chi.URLParam(r, "reportID")
	reportID, err := uuid.Parse(reportIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Report ID"))
		return
	}

	report, err := c.adminService.GetReportDetail(r.Context(), reportID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, report)
}

// ResolveReport godoc
// @Summary      Resolve Report
// @Description  Update the status of a report (e.g., to resolved or rejected). Requires Admin privileges.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        reportID path string true "Report ID (UUID)"
// @Param        request body model.ResolveReportRequest true "Resolve Request"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/reports/{reportID}/resolve [put]
func (c *AdminController) ResolveReport(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	reportIDStr := chi.URLParam(r, "reportID")
	reportID, err := uuid.Parse(reportIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Report ID"))
		return
	}

	var req model.ResolveReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	if err := c.adminService.ResolveReport(r.Context(), userContext.ID, reportID, req); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// DeleteReport godoc
// @Summary      Delete Report
// @Description  Delete a report permanently. Requires Admin privileges.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        reportID path string true "Report ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/reports/{reportID} [delete]
func (c *AdminController) DeleteReport(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	reportIDStr := chi.URLParam(r, "reportID")
	reportID, err := uuid.Parse(reportIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Report ID"))
		return
	}

	if err := c.adminService.DeleteReport(r.Context(), reportID); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// GetDashboardStats godoc
// @Summary      Get Dashboard Stats
// @Description  Get high-level statistics for the admin dashboard. Requires Admin privileges.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Success      200  {object}  helper.ResponseSuccess{data=model.DashboardStatsResponse}
// @Failure      401  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/dashboard [get]
func (c *AdminController) GetDashboardStats(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	stats, err := c.adminService.GetDashboardStats(r.Context())
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, stats)
}

// GetUsers godoc
// @Summary      Get Users List
// @Description  Get paginated list of users with optional filtering. Requires Admin.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        query   query     string  false  "Search query (username, email, or full name)"
// @Param        role    query     string  false  "Filter by role (user, admin)"
// @Param        limit   query     int     false  "Limit (default 20)"
// @Success      200     {object}  helper.ResponseWithPagination{data=[]model.AdminUserListResponse}
// @Failure      403     {object}  helper.ResponseError
// @Failure      500     {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/users [get]
func (c *AdminController) GetUsers(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.AdminGetUserListRequest
	req.Query = r.URL.Query().Get("query")
	req.Role = r.URL.Query().Get("role")
	req.Cursor = r.URL.Query().Get("cursor")

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
		req.Limit = limit
	}

	data, nextCursor, hasNext, err := c.adminService.GetUsers(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPagination(w, data, nextCursor, hasNext)
}

// GetUserDetail godoc
// @Summary      Get User Detail
// @Description  Get detailed user info including stats. Requires Admin.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        userID  path      string  true  "User ID"
// @Success      200     {object}  helper.ResponseSuccess{data=model.AdminUserDetailResponse}
// @Failure      400     {object}  helper.ResponseError
// @Failure      404     {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/users/{userID} [get]
func (c *AdminController) GetUserDetail(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	userIDStr := chi.URLParam(r, "userID")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid user ID"))
		return
	}

	resp, err := c.adminService.GetUserDetail(r.Context(), userID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// ResetUserInfo godoc
// @Summary      Reset User Info
// @Description  Reset user's avatar, bio, or name. Requires Admin.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        userID  path      string                      true  "User ID"
// @Param        req     body      model.ResetUserInfoRequest  true  "Reset Request"
// @Success      200     {object}  helper.ResponseSuccess
// @Failure      400     {object}  helper.ResponseError
// @Failure      500     {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/users/{userID}/reset [post]
func (c *AdminController) ResetUserInfo(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.ResetUserInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid request body"))
		return
	}

	userIDStr := chi.URLParam(r, "userID")
	targetID, err := uuid.Parse(userIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid user ID URL"))
		return
	}
	req.TargetUserID = targetID

	if err := c.validator.Struct(req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(err.Error()))
		return
	}

	if err := c.adminService.ResetUserInfo(r.Context(), req); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// GetGroups godoc
// @Summary      List Groups
// @Description  List and search groups with pagination. Requires Admin.
// @Tags         admin
// @Produce      json
// @Param        query   query     string  false  "Search by name"
// @Param        limit   query     int     false  "Limit"
// @Param        cursor  query     string  false  "Pagination cursor"
// @Success      200     {object}  helper.ResponseWithPagination
// @Failure      403     {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/groups [get]
func (c *AdminController) GetGroups(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.AdminGetGroupListRequest
	req.Query = r.URL.Query().Get("query")
	req.Cursor = r.URL.Query().Get("cursor")
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
		req.Limit = limit
	}

	data, nextCursor, hasNext, err := c.adminService.GetGroups(r.Context(), req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPagination(w, data, nextCursor, hasNext)
}

// GetGroupDetail godoc
// @Summary      Get Group Detail
// @Description  Get detailed group information. Requires Admin.
// @Tags         admin
// @Produce      json
// @Param        chatID  path      string  true  "Group Chat ID (chat_id)"
// @Success      200      {object}  helper.ResponseSuccess
// @Failure      404      {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/groups/{chatID} [get]
func (c *AdminController) GetGroupDetail(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	chatIDStr := chi.URLParam(r, "chatID")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Chat ID"))
		return
	}

	resp, err := c.adminService.GetGroupDetail(r.Context(), chatID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// DissolveGroup godoc
// @Summary      Dissolve Group
// @Description  Soft delete a group. Requires Admin.
// @Tags         admin
// @Produce      json
// @Param        chatID  path      string  true  "Group Chat ID (chat_id)"
// @Success      200      {object}  helper.ResponseSuccess
// @Failure      404      {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/groups/{chatID} [delete]
func (c *AdminController) DissolveGroup(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	chatIDStr := chi.URLParam(r, "chatID")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Chat ID"))
		return
	}

	if err := c.groupChatService.DeleteGroup(r.Context(), userContext.ID, chatID, true); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// ResetGroupInfo godoc
// @Summary      Reset Group Info
// @Description  Reset group's avatar, description, or name. Requires Admin.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        chatID  path      string                      true  "Group Chat ID (chat_id)"
// @Param        req      body      model.ResetGroupInfoRequest true  "Reset Request"
// @Success      200      {object}  helper.ResponseSuccess
// @Failure      400      {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/groups/{chatID}/reset [post]
func (c *AdminController) ResetGroupInfo(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	chatIDStr := chi.URLParam(r, "chatID")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Chat ID"))
		return
	}

	var req model.ResetGroupInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid request body"))
		return
	}

	updateReq := model.UpdateGroupChatRequest{}

	if req.ResetName {
		defaultName := "Group " + chatID.String()[:8]
		updateReq.Name = &defaultName
	}
	if req.ResetDescription {
		emptyDesc := ""
		updateReq.Description = &emptyDesc
	}
	if req.ResetAvatar {
		updateReq.DeleteAvatar = true
	}

	if updateReq.Name != nil || updateReq.Description != nil || updateReq.DeleteAvatar {
		if _, err := c.groupChatService.UpdateGroupChat(r.Context(), userContext.ID, chatID, updateReq, true); err != nil {
			helper.WriteError(w, err)
			return
		}
	}

	helper.WriteSuccess(w, nil)
}

// GetGroupMembers godoc
// @Summary      Get Group Members
// @Description  Get list of members in a group. Requires Admin.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        chatID path string true "Group Chat ID (chat_id UUID)"
// @Param        query query string false "Search query"
// @Param        cursor query string false "Pagination cursor"
// @Param        limit query int false "Number of items per page (default 20, max 50)"
// @Success      200  {object}  helper.ResponseWithPagination{data=[]model.GroupMemberDTO}
// @Failure      400  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/admin/groups/{chatID}/members [get]
func (c *AdminController) GetGroupMembers(w http.ResponseWriter, r *http.Request) {
	requestorID, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	chatIDStr := chi.URLParam(r, "chatID")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Chat ID"))
		return
	}

	query := r.URL.Query().Get("query")
	cursor := r.URL.Query().Get("cursor")
	limitStr := r.URL.Query().Get("limit")

	limit := 20
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
		limit = l
	}

	req := model.SearchGroupMembersRequest{
		GroupID: chatID,
		Query:   query,
		Cursor:  cursor,
		Limit:   limit,
	}

	members, nextCursor, hasNext, err := c.groupChatService.SearchGroupMembers(r.Context(), requestorID.ID, req, true)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPagination(w, members, nextCursor, hasNext)
}
