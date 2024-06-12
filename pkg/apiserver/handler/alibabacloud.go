package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"k8s.io/klog"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/client"
)

func ListAlibabaCloudAllRegionECSPrice(ctx *gin.Context) {
	klog.V(4).Infof("Start to list aws all regions ecs price...")
	alibabaCloudClient, err := getAlibabaCloudPriceClient(ctx)
	if err != nil {
		klog.Errorf("failed to get alibabacloud price client: %v", err)
		abortWithFormattedData(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	data := alibabaCloudClient.ListRegionsInstancesPrice()
	returnFormattedData(ctx, http.StatusOK, data)
}

func ListAlibabaCloudECSPrice(ctx *gin.Context) {
	klog.V(4).Infof("Start to list alibabacloud ecs price...")
	alibabaCloudClient, err := getAlibabaCloudPriceClient(ctx)
	if err != nil {
		klog.Errorf("failed to get alibbacloud price client: %v", err)
		abortWithFormattedData(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	region := ctx.Param("region")
	data := alibabaCloudClient.ListInstancesPrice(region)
	returnFormattedData(ctx, http.StatusOK, data)
}

func GetAlibabaCloudECSPrice(ctx *gin.Context) {
	klog.V(4).Infof("Start to get alibbacloud ecs price...")
	alibabaCloudClient, err := getAlibabaCloudPriceClient(ctx)
	if err != nil {
		klog.Errorf("failed to get alibabcloud price client: %v", err)
		abortWithFormattedData(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	region := ctx.Param("region")
	instanceType := ctx.Param("instance_type")
	data := alibabaCloudClient.GetInstancePrice(region, instanceType)
	returnFormattedData(ctx, http.StatusOK, data)
}

func getAlibabaCloudPriceClient(ctx *gin.Context) (*client.AlibabaCloudPriceClient, error) {
	clientUntyped, ok := ctx.Get(apis.AlibabaCloudClientContextKey)
	if !ok {
		return nil, fmt.Errorf("failed to get clientUntyped from context")
	}
	clientTyped, ok := clientUntyped.(*client.AlibabaCloudPriceClient)
	if !ok {
		return nil, fmt.Errorf("failed to convert client")
	}
	return clientTyped, nil
}
