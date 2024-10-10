package router

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/apiserver/handler"
	"github.com/cloudpilot-ai/priceserver/pkg/client"
)

func NewPriceServerRouter(awsPriceClient *client.AWSPriceClient, alibabaCloudClient *client.AlibabaCloudPriceClient) *gin.Engine {
	router := gin.Default()

	config := cors.DefaultConfig()
	config.AllowOrigins = []string{"*"}
	config.AllowHeaders = []string{"*"}
	corsHandler := cors.New(config)
	router.Use(corsHandler)

	router.Use(gzip.Gzip(gzip.DefaultCompression))

	router.Use(func(context *gin.Context) {
		context.Set(apis.AWSPriceClientContextKey, awsPriceClient)
		context.Set(apis.AlibabaCloudClientContextKey, alibabaCloudClient)
		context.Next()
	})
	initAWSPriceRouter(router)
	initAlibabaCloudPriceRouter(router)
	initHealthRouter(router)

	return router
}

func initAWSPriceRouter(router *gin.Engine) {
	group := router.Group("/api/v1/aws")
	group.GET("/ec2/price", handler.ListAWSAllRegionEC2Price)
	group.GET("/ec2/regions/:region/price", handler.ListAWSEC2Price)
	group.GET("/ec2/regions/:region/types/:instance_type/price", handler.GetAWSEC2Price)
}

func initAlibabaCloudPriceRouter(router *gin.Engine) {
	group := router.Group("/api/v1/alibabacloud")
	group.GET("/ecs/price", handler.ListAlibabaCloudAllRegionECSPrice)
	group.GET("/ecs/regions/:region/price", handler.ListAlibabaCloudECSPrice)
	group.GET("/ecs/regions/:region/types/:instance_type/price", handler.GetAlibabaCloudECSPrice)
}

func initHealthRouter(router *gin.Engine) {
	group := router.Group("/")
	group.GET("/healthz", handler.HealthCheck)
}
