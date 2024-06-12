package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"k8s.io/klog"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/client"
)

func ListAWSAllRegionEC2Price(ctx *gin.Context) {
	klog.V(4).Infof("Start to list aws all regions ec2 price...")
	awsClient, err := getAWSPriceClient(ctx)
	if err != nil {
		klog.Errorf("failed to get aws price client: %v", err)
		abortWithFormattedData(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	data := awsClient.ListRegionsInstancesPrice()
	returnFormattedData(ctx, http.StatusOK, data)
}

func ListAWSEC2Price(ctx *gin.Context) {
	klog.V(4).Infof("Start to list aws ec2 price...")
	awsClient, err := getAWSPriceClient(ctx)
	if err != nil {
		klog.Errorf("failed to get aws price client: %v", err)
		abortWithFormattedData(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	region := ctx.Param("region")
	data := awsClient.ListInstancesPrice(region)
	returnFormattedData(ctx, http.StatusOK, data)
}

func GetAWSEC2Price(ctx *gin.Context) {
	klog.V(4).Infof("Start to get aws ec2 price...")
	awsClient, err := getAWSPriceClient(ctx)
	if err != nil {
		klog.Errorf("failed to get aws price client: %v", err)
		abortWithFormattedData(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	region := ctx.Param("region")
	instanceType := ctx.Param("instance_type")
	data := awsClient.GetInstancePrice(region, instanceType)
	returnFormattedData(ctx, http.StatusOK, data)
}

func getAWSPriceClient(ctx *gin.Context) (*client.AWSPriceClient, error) {
	clientUntyped, ok := ctx.Get(apis.AWSPriceClientContextKey)
	if !ok {
		return nil, fmt.Errorf("failed to get clientUntyped from context")
	}
	clientTyped, ok := clientUntyped.(*client.AWSPriceClient)
	if !ok {
		return nil, fmt.Errorf("failed to convert client")
	}
	return clientTyped, nil
}
