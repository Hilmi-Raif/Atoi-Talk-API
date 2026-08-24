package controller

import (
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func adminControllerRequest(method, target string, params map[string]string, withUser bool, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if withUser {
		r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &model.UserDTO{ID: uuid.New()}))
	}
	rctx := chi.NewRouteContext()
	for name, value := range params {
		rctx.URLParams.Add(name, value)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestAdminControllerRejectsUnauthorizedRequests(t *testing.T) {
	controller := NewAdminController(nil, nil, nil)
	handlers := map[string]http.HandlerFunc{
		"ban":           controller.BanUser,
		"unban":         controller.UnbanUser,
		"reports":       controller.GetReports,
		"report detail": controller.GetReportDetail,
		"resolve":       controller.ResolveReport,
		"delete report": controller.DeleteReport,
		"dashboard":     controller.GetDashboardStats,
		"users":         controller.GetUsers,
		"user detail":   controller.GetUserDetail,
		"reset user":    controller.ResetUserInfo,
		"groups":        controller.GetGroups,
		"group detail":  controller.GetGroupDetail,
		"dissolve":      controller.DissolveGroup,
		"reset group":   controller.ResetGroupInfo,
		"members":       controller.GetGroupMembers,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler(rr, adminControllerRequest(http.MethodGet, "/", nil, false, "{"))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestAdminControllerRejectsInvalidInput(t *testing.T) {
	controller := NewAdminController(nil, nil, nil)
	validID := uuid.NewString()
	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{"ban body", controller.BanUser, adminControllerRequest(http.MethodPost, "/", nil, true, "{")},
		{"unban id", controller.UnbanUser, adminControllerRequest(http.MethodPost, "/", map[string]string{"userID": "bad"}, true, "")},
		{"reports limit", controller.GetReports, adminControllerRequest(http.MethodGet, "/?limit=bad", nil, true, "")},
		{"report detail id", controller.GetReportDetail, adminControllerRequest(http.MethodGet, "/", map[string]string{"reportID": "bad"}, true, "")},
		{"resolve id", controller.ResolveReport, adminControllerRequest(http.MethodPut, "/", map[string]string{"reportID": "bad"}, true, "{}")},
		{"resolve body", controller.ResolveReport, adminControllerRequest(http.MethodPut, "/", map[string]string{"reportID": validID}, true, "{")},
		{"delete report id", controller.DeleteReport, adminControllerRequest(http.MethodDelete, "/", map[string]string{"reportID": "bad"}, true, "")},
		{"users limit", controller.GetUsers, adminControllerRequest(http.MethodGet, "/?limit=bad", nil, true, "")},
		{"user detail id", controller.GetUserDetail, adminControllerRequest(http.MethodGet, "/", map[string]string{"userID": "bad"}, true, "")},
		{"reset user body", controller.ResetUserInfo, adminControllerRequest(http.MethodPost, "/", map[string]string{"userID": validID}, true, "{")},
		{"reset user id", controller.ResetUserInfo, adminControllerRequest(http.MethodPost, "/", map[string]string{"userID": "bad"}, true, "{}")},
		{"groups limit", controller.GetGroups, adminControllerRequest(http.MethodGet, "/?limit=bad", nil, true, "")},
		{"group detail id", controller.GetGroupDetail, adminControllerRequest(http.MethodGet, "/", map[string]string{"chatID": "bad"}, true, "")},
		{"dissolve id", controller.DissolveGroup, adminControllerRequest(http.MethodDelete, "/", map[string]string{"chatID": "bad"}, true, "")},
		{"reset group body", controller.ResetGroupInfo, adminControllerRequest(http.MethodPost, "/", map[string]string{"chatID": validID}, true, "{")},
		{"reset group id", controller.ResetGroupInfo, adminControllerRequest(http.MethodPost, "/", map[string]string{"chatID": "bad"}, true, "{}")},
		{"members id", controller.GetGroupMembers, adminControllerRequest(http.MethodGet, "/", map[string]string{"chatID": "bad"}, true, "")},
		{"members limit", controller.GetGroupMembers, adminControllerRequest(http.MethodGet, "/?limit=bad", map[string]string{"chatID": validID}, true, "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tt.handler(rr, tt.request)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAdminControllerSuccessAndErrorResponses(t *testing.T) {
	mockAdmin := servicemocks.NewMockAdminServicePort(t)
	mockGroup := servicemocks.NewMockGroupChatServicePort(t)
	validator := config.NewValidator()
	controller := NewAdminController(mockAdmin, mockGroup, validator)

	adminID := uuid.New()
	targetID := uuid.New()
	chatID := uuid.New()
	reportID := uuid.New()
	user := &model.UserDTO{ID: adminID}

	mockAdmin.EXPECT().BanUser(mock.Anything, adminID, mock.Anything).
		Return(nil).Once()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/ban", strings.NewReader(`{"target_user_id":"`+targetID.String()+`","reason":"spam"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.BanUser(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().BanUser(mock.Anything, adminID, mock.Anything).
		Return(helper.NewBadRequestError("cannot ban admin")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/users/ban", strings.NewReader(`{"target_user_id":"`+targetID.String()+`","reason":"spam"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.BanUser(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockAdmin.EXPECT().UnbanUser(mock.Anything, adminID, targetID).
		Return(nil).Once()
	req = adminControllerRequest(http.MethodPost, "/api/admin/users/"+targetID.String()+"/unban", map[string]string{"userID": targetID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UnbanUser(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().UnbanUser(mock.Anything, adminID, targetID).
		Return(helper.NewNotFoundError("user not found")).Once()
	req = adminControllerRequest(http.MethodPost, "/api/admin/users/"+targetID.String()+"/unban", map[string]string{"userID": targetID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UnbanUser(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockAdmin.EXPECT().GetReports(mock.Anything, mock.Anything).
		Return([]model.ReportListResponse{{ID: reportID}}, "next", true, nil).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/reports?status=pending&limit=20", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetReports(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().GetReports(mock.Anything, mock.Anything).
		Return(nil, "", false, helper.NewInternalServerError("db error")).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/reports", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetReports(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockAdmin.EXPECT().GetReportDetail(mock.Anything, reportID).
		Return(&model.ReportDetailResponse{ID: reportID}, nil).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/reports/"+reportID.String(), map[string]string{"reportID": reportID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetReportDetail(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().GetReportDetail(mock.Anything, reportID).
		Return(nil, helper.NewNotFoundError("report not found")).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/reports/"+reportID.String(), map[string]string{"reportID": reportID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetReportDetail(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockAdmin.EXPECT().ResolveReport(mock.Anything, adminID, reportID, mock.Anything).
		Return(nil).Once()
	req = adminControllerRequest(http.MethodPut, "/api/admin/reports/"+reportID.String()+"/resolve", map[string]string{"reportID": reportID.String()}, true, `{"status":"resolved","action_notes":"done"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResolveReport(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().ResolveReport(mock.Anything, adminID, reportID, mock.Anything).
		Return(helper.NewBadRequestError("invalid status")).Once()
	req = adminControllerRequest(http.MethodPut, "/api/admin/reports/"+reportID.String()+"/resolve", map[string]string{"reportID": reportID.String()}, true, `{"status":"invalid"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResolveReport(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockAdmin.EXPECT().DeleteReport(mock.Anything, reportID).
		Return(nil).Once()
	req = adminControllerRequest(http.MethodDelete, "/api/admin/reports/"+reportID.String(), map[string]string{"reportID": reportID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteReport(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().DeleteReport(mock.Anything, reportID).
		Return(helper.NewBadRequestError("cannot delete pending")).Once()
	req = adminControllerRequest(http.MethodDelete, "/api/admin/reports/"+reportID.String(), map[string]string{"reportID": reportID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteReport(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockAdmin.EXPECT().GetDashboardStats(mock.Anything).
		Return(&model.DashboardStatsResponse{TotalUsers: 10}, nil).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/dashboard/stats", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetDashboardStats(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().GetDashboardStats(mock.Anything).
		Return(nil, helper.NewInternalServerError("db failure")).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/dashboard/stats", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetDashboardStats(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockAdmin.EXPECT().GetUsers(mock.Anything, mock.Anything).
		Return([]model.AdminUserListResponse{{ID: targetID}}, "next", true, nil).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/users?query=test&limit=20", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetUsers(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().GetUsers(mock.Anything, mock.Anything).
		Return(nil, "", false, helper.NewInternalServerError("db failure")).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetUsers(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockAdmin.EXPECT().GetUserDetail(mock.Anything, targetID).
		Return(&model.AdminUserDetailResponse{ID: targetID}, nil).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/users/"+targetID.String(), map[string]string{"userID": targetID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetUserDetail(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().GetUserDetail(mock.Anything, targetID).
		Return(nil, helper.NewNotFoundError("user not found")).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/users/"+targetID.String(), map[string]string{"userID": targetID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetUserDetail(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockAdmin.EXPECT().ResetUserInfo(mock.Anything, mock.Anything).
		Return(nil).Once()
	req = adminControllerRequest(http.MethodPost, "/api/admin/users/"+targetID.String()+"/reset", map[string]string{"userID": targetID.String()}, true, `{"reset_bio":true}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResetUserInfo(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().ResetUserInfo(mock.Anything, mock.Anything).
		Return(helper.NewBadRequestError("cannot reset admin")).Once()
	req = adminControllerRequest(http.MethodPost, "/api/admin/users/"+targetID.String()+"/reset", map[string]string{"userID": targetID.String()}, true, `{"reset_bio":true}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResetUserInfo(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockAdmin.EXPECT().GetGroups(mock.Anything, mock.Anything).
		Return([]model.AdminGroupListResponse{{ID: chatID}}, "next", true, nil).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/groups?query=g&limit=20", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetGroups(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().GetGroups(mock.Anything, mock.Anything).
		Return(nil, "", false, helper.NewInternalServerError("db failure")).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/groups", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetGroups(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockAdmin.EXPECT().GetGroupDetail(mock.Anything, chatID).
		Return(&model.AdminGroupDetailResponse{ID: chatID}, nil).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetGroupDetail(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAdmin.EXPECT().GetGroupDetail(mock.Anything, chatID).
		Return(nil, helper.NewNotFoundError("group not found")).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetGroupDetail(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockGroup.EXPECT().DeleteGroup(mock.Anything, adminID, chatID, true).
		Return(nil).Once()
	req = adminControllerRequest(http.MethodDelete, "/api/admin/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DissolveGroup(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().DeleteGroup(mock.Anything, adminID, chatID, true).
		Return(helper.NewNotFoundError("group not found")).Once()
	req = adminControllerRequest(http.MethodDelete, "/api/admin/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DissolveGroup(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockGroup.EXPECT().UpdateGroupChat(mock.Anything, adminID, chatID, mock.Anything, true).
		Return(&model.ChatListResponse{ID: chatID}, nil).Once()
	req = adminControllerRequest(http.MethodPost, "/api/admin/groups/"+chatID.String()+"/reset", map[string]string{"chatID": chatID.String()}, true, `{"reset_description":true}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResetGroupInfo(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().UpdateGroupChat(mock.Anything, adminID, chatID, mock.Anything, true).
		Return(nil, helper.NewBadRequestError("cannot reset")).Once()
	req = adminControllerRequest(http.MethodPost, "/api/admin/groups/"+chatID.String()+"/reset", map[string]string{"chatID": chatID.String()}, true, `{"reset_description":true}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResetGroupInfo(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockGroup.EXPECT().SearchGroupMembers(mock.Anything, adminID, mock.Anything, true).
		Return([]model.GroupMemberDTO{{UserID: targetID}}, "", false, nil).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/groups/"+chatID.String()+"/members?query=a&limit=20", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetGroupMembers(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().SearchGroupMembers(mock.Anything, adminID, mock.Anything, true).
		Return(nil, "", false, helper.NewInternalServerError("db error")).Once()
	req = adminControllerRequest(http.MethodGet, "/api/admin/groups/"+chatID.String()+"/members", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetGroupMembers(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}
