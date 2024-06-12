package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func HealthCheck(ctx *gin.Context) {
	returnFormattedData(ctx, http.StatusOK, "Price Server is healthy")
}
