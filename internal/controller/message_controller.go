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

type MessageController struct {
	messageService service.MessageServicePort
}

func NewMessageController(messageService service.MessageServicePort) *MessageController {
	return &MessageController{
		messageService: messageService,
	}
}

// SendMessage godoc
// @Summary      Send Message
// @Description  Send a message to a chat (private or group). Supports text and attachments (via IDs).
// @Tags         message
// @Accept       json
// @Produce      json
// @Param        request body model.SendMessageRequest true "Send Message Request"
// @Success      200  {object}  helper.ResponseSuccess{data=model.MessageResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/messages [post]
func (c *MessageController) SendMessage(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Invalid request body", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.messageService.SendMessage(r.Context(), userContext.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// EditMessage godoc
// @Summary      Edit Message
// @Description  Edit a message's content and/or attachments.
// @Tags         message
// @Accept       json
// @Produce      json
// @Param        messageID path string true "Message ID (UUID)"
// @Param        request body model.EditMessageRequest true "Edit Message Request"
// @Success      200  {object}  helper.ResponseSuccess{data=model.MessageResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/messages/{messageID} [put]
func (c *MessageController) EditMessage(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	messageIDStr := chi.URLParam(r, "messageID")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Message ID"))
		return
	}

	var req model.EditMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Invalid request body", "error", err)
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.messageService.EditMessage(r.Context(), userContext.ID, messageID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}

// GetMessages godoc
// @Summary      Get Messages
// @Description  Get a paginated list of messages from a chat. Supports bidirectional pagination and jumping to a specific message.
// @Tags         message
// @Accept       json
// @Produce      json
// @Param        chatID path string true "Chat ID (UUID)"
// @Param        cursor query string false "Pagination cursor (Base64 encoded message ID)"
// @Param        around_message_id query string false "Jump to message ID (UUID)"
// @Param        limit query int false "Number of messages to fetch (default 20, max 50)"
// @Param        direction query string false "Pagination direction: 'older' (default) or 'newer'"
// @Success      200  {object}  helper.ResponseWithPagination{data=[]model.MessageResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/chats/{chatID}/messages [get]
func (c *MessageController) GetMessages(w http.ResponseWriter, r *http.Request) {
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

	cursor := r.URL.Query().Get("cursor")
	limit := 0
	limitRaw := r.URL.Query().Get("limit")
	if limitRaw != "" {
		parsedLimit, parseErr := strconv.Atoi(limitRaw)
		if parseErr != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid limit"))
			return
		}
		limit = parsedLimit
	}
	direction := r.URL.Query().Get("direction")
	aroundMessageIDStr := r.URL.Query().Get("around_message_id")

	var aroundMessageID *uuid.UUID
	if aroundMessageIDStr != "" {
		id, err := uuid.Parse(aroundMessageIDStr)
		if err != nil {
			helper.WriteError(w, helper.NewBadRequestError("Invalid around_message_id format"))
			return
		}
		aroundMessageID = &id
	}

	req := model.GetMessagesRequest{
		ChatID:          chatID,
		Cursor:          cursor,
		AroundMessageID: aroundMessageID,
		Limit:           limit,
		Direction:       direction,
	}

	messages, nextCursor, hasNext, prevCursor, hasPrev, err := c.messageService.GetMessages(r.Context(), userContext.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccessWithPaginationBidirectional(w, messages, nextCursor, hasNext, prevCursor, hasPrev)
}

// DeleteMessage godoc
// @Summary      Delete Message
// @Description  Soft delete a message. Only the sender can delete their own message.
// @Tags         message
// @Accept       json
// @Produce      json
// @Param        messageID path string true "Message ID (UUID)"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      403  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/messages/{messageID} [delete]
func (c *MessageController) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	messageIDStr := chi.URLParam(r, "messageID")
	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		helper.WriteError(w, helper.NewBadRequestError("Invalid Message ID"))
		return
	}

	err = c.messageService.DeleteMessage(r.Context(), userContext.ID, messageID)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}
