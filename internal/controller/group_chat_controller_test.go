package controller

import (
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

func groupControllerRequest(method, target string, params map[string]string, withUser bool, body string) *http.Request {
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

func TestGroupChatControllerRejectsUnauthorizedRequests(t *testing.T) {
	controller := NewGroupChatController(nil)
	handlers := map[string]http.HandlerFunc{
		"create":         controller.CreateGroupChat,
		"update":         controller.UpdateGroupChat,
		"search members": controller.SearchGroupMembers,
		"add member":     controller.AddMember,
		"leave":          controller.LeaveGroup,
		"kick":           controller.KickMember,
		"role":           controller.UpdateMemberRole,
		"transfer":       controller.TransferOwnership,
		"delete":         controller.DeleteGroup,
		"public search":  controller.SearchPublicGroups,
		"join public":    controller.JoinPublicGroup,
		"join invite":    controller.JoinGroupByInvite,
		"reset invite":   controller.ResetInviteCode,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler(rr, groupControllerRequest(http.MethodPost, "/", nil, false, "{"))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestGroupChatControllerRejectsInvalidInput(t *testing.T) {
	controller := NewGroupChatController(nil)
	user := map[string]string{"chatID": uuid.NewString(), "userID": uuid.NewString()}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{"create body", controller.CreateGroupChat, groupControllerRequest(http.MethodPost, "/", nil, true, "{")},
		{"update id", controller.UpdateGroupChat, groupControllerRequest(http.MethodPut, "/", map[string]string{"chatID": "bad"}, true, "{}")},
		{"update body", controller.UpdateGroupChat, groupControllerRequest(http.MethodPut, "/", user, true, "{")},
		{"search member id", controller.SearchGroupMembers, groupControllerRequest(http.MethodGet, "/", map[string]string{"chatID": "bad"}, true, "")},
		{"search member limit", controller.SearchGroupMembers, groupControllerRequest(http.MethodGet, "/?limit=bad", map[string]string{"chatID": uuid.NewString()}, true, "")},
		{"add member id", controller.AddMember, groupControllerRequest(http.MethodPost, "/", map[string]string{"chatID": "bad"}, true, "{}")},
		{"add member body", controller.AddMember, groupControllerRequest(http.MethodPost, "/", user, true, "{")},
		{"leave id", controller.LeaveGroup, groupControllerRequest(http.MethodPost, "/", map[string]string{"chatID": "bad"}, true, "")},
		{"kick chat id", controller.KickMember, groupControllerRequest(http.MethodPost, "/", map[string]string{"chatID": "bad", "userID": uuid.NewString()}, true, "")},
		{"kick user id", controller.KickMember, groupControllerRequest(http.MethodPost, "/", map[string]string{"chatID": uuid.NewString(), "userID": "bad"}, true, "")},
		{"role body", controller.UpdateMemberRole, groupControllerRequest(http.MethodPut, "/", user, true, "{")},
		{"transfer body", controller.TransferOwnership, groupControllerRequest(http.MethodPost, "/", map[string]string{"chatID": uuid.NewString()}, true, "{")},
		{"delete id", controller.DeleteGroup, groupControllerRequest(http.MethodDelete, "/", map[string]string{"chatID": "bad"}, true, "")},
		{"public search limit", controller.SearchPublicGroups, groupControllerRequest(http.MethodGet, "/?limit=bad", nil, true, "")},
		{"join public id", controller.JoinPublicGroup, groupControllerRequest(http.MethodPost, "/", map[string]string{"chatID": "bad"}, true, "")},
		{"join invite body", controller.JoinGroupByInvite, groupControllerRequest(http.MethodPost, "/", nil, true, "{")},
		{"invite code missing", controller.GetGroupByInviteCode, groupControllerRequest(http.MethodGet, "/", map[string]string{"inviteCode": ""}, false, "")},
		{"reset invite id", controller.ResetInviteCode, groupControllerRequest(http.MethodPut, "/", map[string]string{"chatID": "bad"}, true, "")},
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

func TestGroupChatControllerSuccessAndErrorResponses(t *testing.T) {
	mockGroup := servicemocks.NewMockGroupChatServicePort(t)
	controller := NewGroupChatController(mockGroup)

	userID := uuid.New()
	chatID := uuid.New()
	targetID := uuid.New()
	user := &model.UserDTO{ID: userID}

	mockGroup.EXPECT().CreateGroupChat(mock.Anything, userID, mock.Anything).
		Return(&model.ChatListResponse{ID: chatID}, nil).Once()
	req := httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(`{"name":"group","member_ids":["`+targetID.String()+`"]}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.CreateGroupChat(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().CreateGroupChat(mock.Anything, userID, mock.Anything).
		Return(nil, helper.NewBadRequestError("bad group")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(`{"name":"group","member_ids":["`+targetID.String()+`"]}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.CreateGroupChat(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockGroup.EXPECT().UpdateGroupChat(mock.Anything, userID, chatID, mock.Anything, false).
		Return(&model.ChatListResponse{ID: chatID}, nil).Once()
	req = groupControllerRequest(http.MethodPut, "/api/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, `{"name":"renamed"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UpdateGroupChat(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().UpdateGroupChat(mock.Anything, userID, chatID, mock.Anything, false).
		Return(nil, helper.NewForbiddenError("not admin")).Once()
	req = groupControllerRequest(http.MethodPut, "/api/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, `{"name":"renamed"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UpdateGroupChat(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockGroup.EXPECT().SearchGroupMembers(mock.Anything, userID, mock.Anything, false).
		Return([]model.GroupMemberDTO{{UserID: targetID}}, "", false, nil).Once()
	req = groupControllerRequest(http.MethodGet, "/api/groups/"+chatID.String()+"/members?query=a&limit=20", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.SearchGroupMembers(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().SearchGroupMembers(mock.Anything, userID, mock.Anything, false).
		Return(nil, "", false, helper.NewForbiddenError("not member")).Once()
	req = groupControllerRequest(http.MethodGet, "/api/groups/"+chatID.String()+"/members", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.SearchGroupMembers(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockGroup.EXPECT().AddMember(mock.Anything, userID, chatID, mock.Anything).
		Return([]*model.MessageResponse{{ID: uuid.New()}}, nil).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/members", map[string]string{"chatID": chatID.String()}, true, `{"user_ids":["`+targetID.String()+`"]}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.AddMember(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().AddMember(mock.Anything, userID, chatID, mock.Anything).
		Return(nil, helper.NewForbiddenError("not admin")).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/members", map[string]string{"chatID": chatID.String()}, true, `{"user_ids":["`+targetID.String()+`"]}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.AddMember(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockGroup.EXPECT().LeaveGroup(mock.Anything, userID, chatID).
		Return(&model.MessageResponse{ID: uuid.New()}, nil).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/leave", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.LeaveGroup(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().LeaveGroup(mock.Anything, userID, chatID).
		Return(nil, helper.NewBadRequestError("owner cannot leave")).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/leave", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.LeaveGroup(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockGroup.EXPECT().KickMember(mock.Anything, userID, chatID, targetID).
		Return(&model.MessageResponse{ID: uuid.New()}, nil).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/members/"+targetID.String()+"/kick", map[string]string{"chatID": chatID.String(), "userID": targetID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.KickMember(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().KickMember(mock.Anything, userID, chatID, targetID).
		Return(nil, helper.NewForbiddenError("not admin")).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/members/"+targetID.String()+"/kick", map[string]string{"chatID": chatID.String(), "userID": targetID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.KickMember(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockGroup.EXPECT().UpdateMemberRole(mock.Anything, userID, chatID, targetID, mock.Anything).
		Return(&model.MessageResponse{ID: uuid.New()}, nil).Once()
	req = groupControllerRequest(http.MethodPut, "/api/groups/"+chatID.String()+"/members/"+targetID.String()+"/role", map[string]string{"chatID": chatID.String(), "userID": targetID.String()}, true, `{"role":"admin"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UpdateMemberRole(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().UpdateMemberRole(mock.Anything, userID, chatID, targetID, mock.Anything).
		Return(nil, helper.NewForbiddenError("only owner")).Once()
	req = groupControllerRequest(http.MethodPut, "/api/groups/"+chatID.String()+"/members/"+targetID.String()+"/role", map[string]string{"chatID": chatID.String(), "userID": targetID.String()}, true, `{"role":"admin"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UpdateMemberRole(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockGroup.EXPECT().TransferOwnership(mock.Anything, userID, chatID, mock.Anything).
		Return(&model.MessageResponse{ID: uuid.New()}, nil).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/transfer-ownership", map[string]string{"chatID": chatID.String()}, true, `{"new_owner_id":"`+targetID.String()+`"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.TransferOwnership(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().TransferOwnership(mock.Anything, userID, chatID, mock.Anything).
		Return(nil, helper.NewForbiddenError("only owner")).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/transfer-ownership", map[string]string{"chatID": chatID.String()}, true, `{"new_owner_id":"`+targetID.String()+`"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.TransferOwnership(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockGroup.EXPECT().DeleteGroup(mock.Anything, userID, chatID, false).
		Return(nil).Once()
	req = groupControllerRequest(http.MethodDelete, "/api/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteGroup(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().DeleteGroup(mock.Anything, userID, chatID, false).
		Return(helper.NewForbiddenError("only owner")).Once()
	req = groupControllerRequest(http.MethodDelete, "/api/groups/"+chatID.String(), map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteGroup(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockGroup.EXPECT().SearchPublicGroups(mock.Anything, userID, mock.Anything).
		Return([]model.PublicGroupDTO{{ID: chatID}}, "next", true, nil).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/groups/public?query=test&limit=20&cursor=cur", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.SearchPublicGroups(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().SearchPublicGroups(mock.Anything, userID, mock.Anything).
		Return(nil, "", false, helper.NewInternalServerError("search failure")).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/groups/public", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.SearchPublicGroups(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockGroup.EXPECT().JoinPublicGroup(mock.Anything, userID, chatID).
		Return(&model.ChatListResponse{ID: chatID}, nil).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/join", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.JoinPublicGroup(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().JoinPublicGroup(mock.Anything, userID, chatID).
		Return(nil, helper.NewConflictError("already member")).Once()
	req = groupControllerRequest(http.MethodPost, "/api/groups/"+chatID.String()+"/join", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.JoinPublicGroup(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code)

	mockGroup.EXPECT().JoinGroupByInvite(mock.Anything, userID, "abc123456789").
		Return(&model.ChatListResponse{ID: chatID}, nil).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/groups/join/invite", strings.NewReader(`{"invite_code":"abc123456789"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.JoinGroupByInvite(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().JoinGroupByInvite(mock.Anything, userID, "abc123456789").
		Return(nil, helper.NewNotFoundError("invalid code")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/groups/join/invite", strings.NewReader(`{"invite_code":"abc123456789"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.JoinGroupByInvite(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockGroup.EXPECT().GetGroupByInviteCode(mock.Anything, "abc123456789").
		Return(&model.GroupPreviewDTO{ID: chatID}, nil).Once()
	req = groupControllerRequest(http.MethodGet, "/api/groups/preview/abc123456789", map[string]string{"inviteCode": "abc123456789"}, false, "")
	rr = httptest.NewRecorder()
	controller.GetGroupByInviteCode(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().GetGroupByInviteCode(mock.Anything, "abc123456789").
		Return(nil, helper.NewNotFoundError("not found")).Once()
	req = groupControllerRequest(http.MethodGet, "/api/groups/preview/abc123456789", map[string]string{"inviteCode": "abc123456789"}, false, "")
	rr = httptest.NewRecorder()
	controller.GetGroupByInviteCode(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockGroup.EXPECT().ResetInviteCode(mock.Anything, userID, chatID).
		Return(&model.GroupInviteResponse{InviteCode: "new123456789"}, nil).Once()
	req = groupControllerRequest(http.MethodPut, "/api/groups/"+chatID.String()+"/reset-invite", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResetInviteCode(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockGroup.EXPECT().ResetInviteCode(mock.Anything, userID, chatID).
		Return(nil, helper.NewForbiddenError("only owner")).Once()
	req = groupControllerRequest(http.MethodPut, "/api/groups/"+chatID.String()+"/reset-invite", map[string]string{"chatID": chatID.String()}, true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ResetInviteCode(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}
