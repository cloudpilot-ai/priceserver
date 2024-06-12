package main

import (
	"encoding/json"
	"os"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/client"
	"github.com/cloudpilot-ai/priceserver/pkg/tools"
)

func handleAWSData() error {
	globalAK := os.Getenv(apis.AWSGlobalAKEnv)
	globalSK := os.Getenv(apis.AWSGlobalSKEnv)
	cnAK := os.Getenv(apis.AWSCNAKEnv)
	cnSK := os.Getenv(apis.AWSCNSKEnv)

	awsPriceClient, err := client.NewAWSPriceClient(globalAK, globalSK, cnAK, cnSK, false)
	if err != nil {
		return err
	}
	awsPriceClient.RefreshOnDemandPrice("", "")
	awsPriceClient.RefreshSavingsPlanPrice("", "")

	data := awsPriceClient.ListRegionsInstancesPrice()
	marshalData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	err = os.WriteFile("pkg/client/builtin-data/aws_price.json", marshalData, 0644)
	if err != nil {
		return err
	}

	return nil
}

func handleAlibabaCloudData() error {
	alibabaCloudAKSKPool := tools.ExtractAlibabaCloudAKSKPool()

	alibabaCloudClient, err := client.NewAlibabaCloudPriceClient(alibabaCloudAKSKPool, false)
	if err != nil {
		return err
	}

	alibabaCloudClient.RefreshOnDemandPrice()

	data := alibabaCloudClient.ListRegionsInstancesPrice()
	marshalData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	err = os.WriteFile("pkg/client/builtin-data/alibabacloud_price.json", marshalData, 0644)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	if err := handleAWSData(); err != nil {
		panic(err)
	}

	if err := handleAlibabaCloudData(); err != nil {
		panic(err)
	}
}
