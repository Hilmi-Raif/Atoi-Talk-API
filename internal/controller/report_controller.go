package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/service"
	"encoding/json"
	"net/http"
)

type ReportController struct {
	reportService service.ReportServicePort
}

func NewReportController(reportService service.ReportServicePort) *ReportController {
	return &ReportController{
		reportService: reportService,
	}
}

// CreateReport godoc
// @Summary      Create Report
// @Description  Report a message, group, or user. Server will automatically snapshot the evidence.
// @Tags         report
// @Accept       json
// @Produce      json
// @Param        request body model.CreateReportRequest true "Report Request"
// @Success      200  {object}  helper.ResponseSuccess
// @Failure      400  {object}  helper.ResponseError
// @Failure      401  {object}  helper.ResponseError
// @Failure      404  {object}  helper.ResponseError
// @Failure      429  {object}  helper.ResponseError
// @Failure      500  {object}  helper.ResponseError
// @Security     BearerAuth
// @Router       /api/reports [post]
func (c *ReportController) CreateReport(w http.ResponseWriter, r *http.Request) {
	userContext, ok := r.Context().Value(middleware.UserContextKey).(*model.UserDTO)
	if !ok {
		helper.WriteError(w, helper.NewUnauthorizedError(""))
		return
	}

	var req model.CreateReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.WriteError(w, helper.NewBadRequestError(""))
		return
	}

	if err := c.reportService.CreateReport(r.Context(), userContext.ID, req); err != nil {
		helper.WriteError(w, err)
		return
	}

	helper.WriteSuccess(w, nil)
}
