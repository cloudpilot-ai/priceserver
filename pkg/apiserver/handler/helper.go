package handler

import "github.com/gin-gonic/gin"

func returnFormattedData(ctx *gin.Context, code int, data interface{}) {
	ctx.AsciiJSON(code, data)
}

func abortWithFormattedData(ctx *gin.Context, code int, data interface{}) {
	returnFormattedData(ctx, code, data)
	ctx.Abort()
}
