package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"encoding/json"
	"net/http"
)

type PrivateChatController struct {
	privateChatService service.PrivateChatServicePort
}

func NewPrivateChatController(privateChatService service.PrivateChatServicePort) *PrivateChatController {
	return &PrivateChatController{
		privateChatService: privateChatService,
	}
}

// CreatePrivateChat godoc
// @Summary      Create Private Chat
// @Description  Create a new private chat with another user. If it already exists, returns the existing chat.
// @Tags         chat
// @Accept       json
// @Produce      json
// @Param        request body model.CreatePrivateChatRequest true "Create Private Chat Request"
// @Success      200  {object}  helper.ResponseSuccess{data=model.ChatResponse}
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/chats/private [post]
func (c *PrivateChatController) CreatePrivateChat(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.CreatePrivateChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	resp, err := c.privateChatService.CreatePrivateChat(r.Context(), userContext.ID, req)
	if err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, resp)
}
