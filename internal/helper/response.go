package helper

import (
	"encoding/json"
	"net/http"
)

type ResponseSuccess struct {
	Data interface{} `json:"data"`
}

type ResponseError struct {
	Error string `json:"error"`
}

type PaginationMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasNext    bool   `json:"has_next"`
	PrevCursor string `json:"prev_cursor,omitempty"`
	HasPrev    bool   `json:"has_prev"`
}

type ResponseWithPagination struct {
	Data interface{}    `json:"data"`
	Meta PaginationMeta `json:"meta"`
}

func WriteJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteSuccess(w http.ResponseWriter, data interface{}) {
	if data == nil {
		data = ""
	}
	WriteJSON(w, http.StatusOK, ResponseSuccess{
		Data: data,
	})
}

func WriteSuccessWithPagination(w http.ResponseWriter, data interface{}, nextCursor string, hasNext bool) {
	WriteJSON(w, http.StatusOK, ResponseWithPagination{
		Data: data,
		Meta: PaginationMeta{
			NextCursor: nextCursor,
			HasNext:    hasNext,
		},
	})
}

func WriteSuccessWithPaginationBidirectional(w http.ResponseWriter, data interface{}, nextCursor string, hasNext bool, prevCursor string, hasPrev bool) {
	WriteJSON(w, http.StatusOK, ResponseWithPagination{
		Data: data,
		Meta: PaginationMeta{
			NextCursor: nextCursor,
			HasNext:    hasNext,
			PrevCursor: prevCursor,
			HasPrev:    hasPrev,
		},
	})
}

func WriteError(w http.ResponseWriter, err error) {
	appErr, ok := err.(*AppError)
	if !ok {
		appErr = NewInternalServerError("Internal Server Error")
	}

	WriteJSON(w, appErr.Code, ResponseError{
		Error: appErr.Message,
	})
}
