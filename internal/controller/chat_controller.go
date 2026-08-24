package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ChatController struct {
	chatService chatService
}

type chatService = service.ChatServicePort

func NewChatController(chatService chatService) *ChatController {
	return &ChatController{
		chatService: chatService,
	}
}

// GetChats godoc
// @Summary      Get Chat List
// @Description  Get a paginated list of user's chats, sorted by last message time. Can be searched.
// @Tags         chat
// @Accept       json
// @Produce      json
// @Param        query query string false "Search query for chat name"
// @Param        cursor query string false "Pagination cursor"
// @Param        limit query int false "Number of items per page (default 20, max 50)"
// @Success      200  {object}  helper.ResponseWithPagination{data=[]model.ChatListResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/chats [get]
func (c *ChatController) GetChats(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
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

	req := model.GetChatsRequest{
		Query:  query,
		Cursor: cursor,
		Limit:  limit,
	}

	chats, nextCursor, hasNext, err := c.chatService.GetChats(r.Context(), userContext.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPagination(w, chats, nextCursor, hasNext)
}

// GetChat godoc
// @Summary      Get Chat by ID
// @Description  Get detailed information about a single chat.
// @Tags         chat
// @Accept       json
// @Produce      json
// @Param        id path string true "Chat ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess{data=model.ChatListResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/chats/{id} [get]
func (c *ChatController) GetChat(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	chatIDStr := chi.URLParam(r, "id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Chat ID"))
		return
	}

	chat, err := c.chatService.GetChatByID(r.Context(), userContext.ID, chatID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, chat)
}

// MarkAsRead godoc
// @Summary      Mark Chat as Read
// @Description  Mark all messages in a chat as read for the current user.
// @Tags         chat
// @Accept       json
// @Produce      json
// @Param        id path string true "Chat ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/chats/{id}/read [post]
func (c *ChatController) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	chatIDStr := chi.URLParam(r, "id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Chat ID"))
		return
	}

	err = c.chatService.MarkAsRead(r.Context(), userContext.ID, chatID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}

// HideChat godoc
// @Summary      Hide Chat
// @Description  Hide a private chat from the chat list. It will reappear if a new message is sent or received.
// @Tags         chat
// @Accept       json
// @Produce      json
// @Param        id path string true "Chat ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/chats/{id}/hide [post]
func (c *ChatController) HideChat(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	chatIDStr := chi.URLParam(r, "id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Chat ID"))
		return
	}

	err = c.chatService.HideChat(r.Context(), userContext.ID, chatID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}
