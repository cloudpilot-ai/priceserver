package client

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecsclient "github.com/alibabacloud-go/ecs-20140526/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/samber/lo"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/tools"
)

//go:embed builtin-data/*.json
var file embed.FS

type AKSKPair struct {
	AK string
	SK string
}

func ExtractAlibabaCloudAKSKPool() []AKSKPair {
	akskPool := os.Getenv(apis.AlibabaCloudAKSKPoolEnv)
	if akskPool == "" {
		return nil
	}

	akskPair := []AKSKPair{}
	for _, aksk := range strings.Split(akskPool, ",") {
		aksk = strings.TrimSpace(aksk)
		if aksk == "" {
			continue
		}
		akskArray := strings.Split(aksk, ":")
		if len(akskArray) != 2 {
			continue
		}

		akskPair = append(akskPair, AKSKPair{AK: akskArray[0], SK: akskArray[1]})
	}

	return akskPair
}

type AlibabaCloudPriceClient struct {
	akskPool []AKSKPair

	regionList []string

	dataMutex sync.RWMutex
	priceData map[string]*apis.RegionalInstancePrice
}

func NewAlibabaCloudPriceClient(akskPool []AKSKPair, initialSpotUpdate bool) (*AlibabaCloudPriceClient, error) {
	data, err := file.ReadFile("builtin-data/alibabacloud_price.json")
	if err != nil {
		return nil, err
	}

	client := &AlibabaCloudPriceClient{
		akskPool:   akskPool,
		regionList: []string{},
		priceData:  map[string]*apis.RegionalInstancePrice{},
	}
	if err := json.Unmarshal(data, &client.priceData); err != nil {
		return nil, err
	}

	if err := client.initialRegions(); err != nil {
		return nil, err
	}

	if initialSpotUpdate {
		client.refreshSpotPrice()
	}

	return client, nil
}

func (a *AlibabaCloudPriceClient) Run(ctx context.Context) {
	odTicker := time.NewTicker(time.Hour * 24 * 7)
	defer odTicker.Stop()

	spotTicker := time.NewTicker(time.Minute * 30)
	defer spotTicker.Stop()

	for {
		select {
		case <-odTicker.C:
			a.RefreshOnDemandPrice()
		case <-spotTicker.C:
			a.refreshSpotPrice()
		case <-ctx.Done():
			return
		}
	}
}

func getSpotPrice(client *ecsclient.Client, region, instanceType string) (map[string]float64, error) {
	describeSpotPriceHistoryRequest := &ecsclient.DescribeSpotPriceHistoryRequest{
		RegionId:     tea.String(region),
		InstanceType: tea.String(instanceType),
		NetworkType:  tea.String("vpc"),
		// With following time range, we get only one entry for eatch zones
		StartTime: tea.String("2024-10-09T06:00:00Z"),
	}
	priceResp, err := client.DescribeSpotPriceHistoryWithOptions(describeSpotPriceHistoryRequest, &util.RuntimeOptions{})
	if err != nil {
		klog.Errorf("Failed to get price of instance %s in region %s:%v", instanceType, region, err)
		return nil, err
	}
	if len(priceResp.Body.SpotPrices.SpotPriceType) == 0 {
		klog.Warningf("No spot price available for instance %s in region %s", instanceType, region)
		return nil, nil
	}

	ret := map[string]float64{}
	for _, spotPrice := range priceResp.Body.SpotPrices.SpotPriceType {
		ret[*spotPrice.ZoneId] = float64(tea.Float32Value(spotPrice.SpotPrice))
	}

	for _, spotPrice := range priceResp.Body.SpotPrices.SpotPriceType {
		ret[*spotPrice.ZoneId] = float64(tea.Float32Value(spotPrice.SpotPrice))
	}

	return ret, nil
}

type RegionalSpotInstancePrice struct {
	Region        string                              `json:"region"`
	InstanceTypes *map[string]*apis.InstanceTypePrice `json:"instanceTypes"`
}

type RegionalSpotInstancePriceSplit struct {
	Region       string                  `json:"region"`
	InstanceType string                  `json:"instanceType"`
	Info         *apis.InstanceTypePrice `json:"info"`
}

func (a *AlibabaCloudPriceClient) refreshSpotPrice() {
	rsiPrices := make([]*RegionalSpotInstancePrice, len(a.regionList))

	workqueue.ParallelizeUntil(context.Background(), 50, len(a.regionList), func(i int) {
		instanceTypes, err := a.listInstanceTypes(a.regionList[i])
		if err != nil {
			klog.Errorf("Failed to list instance types in region %s:%v", a.regionList[i], err)
			return
		}

		rsiPrices[i] = &RegionalSpotInstancePrice{
			Region:        a.regionList[i],
			InstanceTypes: &instanceTypes,
		}
	})

	n := 0
	for i := range rsiPrices {
		if rsiPrices[i] != nil {
			n += len(*rsiPrices[i].InstanceTypes)
		}
	}

	rsiPricess := make([]RegionalSpotInstancePriceSplit, 0, n)
	for i := range rsiPrices {
		if rsiPrices[i] != nil {
			for instanceType, info := range *rsiPrices[i].InstanceTypes {
				rsiPricess = append(rsiPricess, RegionalSpotInstancePriceSplit{
					Region:       rsiPrices[i].Region,
					InstanceType: instanceType,
					Info:         info,
				})
			}
		}
	}

	workqueue.ParallelizeUntil(context.Background(), 50, n, func(i int) {
		client, err := a.createECSClient(rsiPricess[i].Region)
		if err != nil {
			klog.Errorf("Failed to create ECS client in region %s:%v", rsiPricess[i].Region, err)
			return
		}

		spotPrice, err := getSpotPrice(client, rsiPricess[i].Region, rsiPricess[i].InstanceType)
		if err != nil {
			klog.Errorf("Failed to get spot price in region %s:%v", rsiPricess[i].Region, err)
			return
		}

		rsiPricess[i].Info.SpotPricePerHour = spotPrice
	})

	a.dataMutex.Lock()
	defer a.dataMutex.Unlock()
	for i := range rsiPricess {
		if _, ok := a.priceData[rsiPricess[i].Region]; !ok {
			a.priceData[rsiPricess[i].Region] = &apis.RegionalInstancePrice{
				InstanceTypePrices: map[string]*apis.InstanceTypePrice{},
			}
		}
		if _, ok := a.priceData[rsiPricess[i].Region].InstanceTypePrices[rsiPricess[i].InstanceType]; ok {
			rsiPricess[i].Info.OnDemandPricePerHour = a.priceData[rsiPricess[i].Region].InstanceTypePrices[rsiPricess[i].InstanceType].OnDemandPricePerHour
		}
		a.priceData[rsiPricess[i].Region].InstanceTypePrices[rsiPricess[i].InstanceType] = rsiPricess[i].Info
	}

	klog.Infof("All spot prices are refreshed for AlibabaCloud")
}

type ECSPrice struct {
	PricingInfo map[string]ECSPriceDetail `json:"pricingInfo"`
}

type ECSPriceDetail struct {
	Hours []ECSHoursPrice `json:"hours"`
}

type ECSHoursPrice struct {
	Price string `json:"price"`
}

func getECSPrice() (map[string]map[string]float64, error) {
	baseUrl := "https://www.aliyun.com/price/ecs/ecs-pricing/zh"
	baseResp, err := http.Get(baseUrl)
	if err != nil {
		klog.Errorf("Get ecs price failed: %v", err)
		return nil, err
	}
	defer baseResp.Body.Close()

	// Extract content
	data, err := io.ReadAll(baseResp.Body)
	if err != nil {
		klog.Errorf("Get ecs price failed: %v", err)
		return nil, err
	}

	// This is referring https://www.aliyun.com/price/ecs/ecs-pricing/zh#/?_k=zmi2qe
	pattern := `https://g.alicdn.com/aliyun/ecs-price-info/[0-9.]+`
	re := regexp.MustCompile(pattern)
	priceUrl := re.FindString(string(data))
	if priceUrl == "" {
		klog.Errorf("Cloud not find price url")
		return nil, fmt.Errorf("cloud not find price url")
	}

	reqUrl, err := url.JoinPath(priceUrl, "/price/download/instancePrice.json")
	if err != nil {
		klog.Errorf("Failed to get price request url: %v", err)
		return nil, err
	}
	resp, err := http.Get(reqUrl)
	if err != nil {
		klog.Errorf("Get ecs price failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	data, err = io.ReadAll(resp.Body)
	if err != nil {
		klog.Errorf("Get ecs price failed: %v", err)
		return nil, err
	}

	var ecsPrice ECSPrice
	err = json.Unmarshal(data, &ecsPrice)
	if err != nil {
		klog.Errorf("Get ecs price failed: %v", err)
		return nil, err
	}

	ret := map[string]map[string]float64{}
	for k, v := range ecsPrice.PricingInfo {
		parts := strings.Split(k, "::")
		regions := parts[0]
		os := parts[3]
		instanceType := parts[1]
		if os == "linux" {
			price, err := strconv.ParseFloat(v.Hours[0].Price, 64)
			if err != nil {
				klog.Errorf("Get ecs price failed: %v", err)
				return nil, err
			}
			if ret[regions] == nil {
				ret[regions] = map[string]float64{}
			}
			ret[regions][instanceType] = price
		}
	}

	return ret, nil
}

func (a *AlibabaCloudPriceClient) RefreshOnDemandPrice() {
	priceInfo, err := getECSPrice()
	if err != nil {
		return
	}

	handleFunc := func(paras ...interface{}) {
		region := paras[0].(string)
		instanceTypes, err := a.listInstanceTypes(region)
		if err != nil {
			return
		}

		for instanceType := range instanceTypes {
			instanceTypes[instanceType].OnDemandPricePerHour = priceInfo[region][instanceType]
			if _, ok := a.priceData[region]; !ok {
				continue
			}
			if _, ok := a.priceData[region].InstanceTypePrices[instanceType]; !ok {
				continue
			}
			instanceTypes[instanceType].SpotPricePerHour = a.priceData[region].InstanceTypePrices[instanceType].SpotPricePerHour
		}
		a.dataMutex.Lock()
		a.priceData[region] = &apis.RegionalInstancePrice{InstanceTypePrices: instanceTypes}
		a.dataMutex.Unlock()
	}

	priceTask := tools.NewParallelTask(handleFunc)
	for _, region := range a.regionList {
		klog.Infof("Start to handle region %s for on-demand", region)

		priceTask.Add([]interface{}{region})
	}
	priceTask.Process()

	klog.Infof("All on-demand prices are refreshed for AlibabaCloud")
}

func (a *AlibabaCloudPriceClient) listInstanceTypes(region string) (map[string]*apis.InstanceTypePrice, error) {
	client, err := a.createECSClient(region)
	if err != nil {
		return nil, err
	}

	zonesResp, err := client.DescribeZonesWithOptions(&ecsclient.DescribeZonesRequest{RegionId: tea.String(region)},
		&util.RuntimeOptions{})
	if err != nil {
		klog.Errorf("Failed to list zones in region %s:%v", region, err)
		return nil, err
	}

	typesResp, err := client.DescribeInstanceTypesWithOptions(&ecsclient.DescribeInstanceTypesRequest{},
		&util.RuntimeOptions{})
	if err != nil {
		klog.Errorf("Failed to list instance types in region %s:%v", region, err)
		return nil, err
	}

	availableTypesResp, err := client.DescribeAvailableResource(
		&ecsclient.DescribeAvailableResourceRequest{
			RegionId:            tea.String(region),
			DestinationResource: tea.String("InstanceType"),
			InstanceChargeType:  tea.String("PostPaid"),
		})
	if err != nil {
		klog.Errorf("Failed to list available instance types in region %s:%v", region, err)
		return nil, err
	}

	ret := map[string]*apis.InstanceTypePrice{}
	for _, item := range typesResp.Body.InstanceTypes.InstanceType {
		if !isSupportedResource(tea.StringValue(item.InstanceTypeId),
			availableTypesResp.Body.AvailableZones.AvailableZone[0].AvailableResources.AvailableResource[0].SupportedResources) {
			continue
		}
		ret[tea.StringValue(item.InstanceTypeId)] = &apis.InstanceTypePrice{
			Arch:   extractECSArch(tea.ToString(item.CpuArchitecture)),
			VCPU:   float64(tea.Int32Value(item.CpuCoreCount)),
			Memory: float64(tea.Float32Value(item.MemorySize)),
			GPU:    float64(tea.Int32Value(item.GPUAmount)),
			Zones: lo.Map(zonesResp.Body.Zones.Zone, func(item *ecsclient.DescribeZonesResponseBodyZonesZone, index int) string {
				return tea.StringValue(item.ZoneId)
			}),
		}
	}

	return ret, nil
}

func isSupportedResource(instanceType string,
	supportedResource *ecsclient.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResources) bool {
	for _, i := range supportedResource.SupportedResource {
		if tea.StringValue(i.Value) == instanceType {
			return true
		}
	}

	return false
}

func extractECSArch(unFormatedArch string) string {
	switch unFormatedArch {
	case "X86":
		return "amd64"
	case "ARM":
		return "arm64"
	default:
		return "amd64"
	}
}

var (
	ignoreRegions = map[string]struct{}{
		"ap-southeast-2": {}, // ap-southeast-2(Sydney) is shutdown
	}
)

func (a *AlibabaCloudPriceClient) initialRegions() error {
	// We use cn-hangzhou as the default region to list regions
	client, err := a.createECSClient("cn-hangzhou")
	if err != nil {
		return err
	}

	resp, err := client.DescribeRegionsWithOptions(&ecsclient.DescribeRegionsRequest{}, &util.RuntimeOptions{})
	if err != nil {
		klog.Errorf("Failed to list regions:%v", err)
		return err
	}

	for _, regionData := range resp.Body.Regions.Region {
		if _, ok := ignoreRegions[tea.StringValue(regionData.RegionId)]; ok {
			continue
		}
		a.regionList = append(a.regionList, tea.StringValue(regionData.RegionId))
	}

	return nil
}

func (a *AlibabaCloudPriceClient) createECSClient(region string) (*ecsclient.Client, error) {
	// Take one ak/sk from pool
	pick := rand.Intn(len(a.akskPool))
	ak, sk := a.akskPool[pick].AK, a.akskPool[pick].SK
	config := &openapi.Config{
		AccessKeyId:     tea.String(ak),
		AccessKeySecret: tea.String(sk),
		RegionId:        tea.String(region),
	}
	client, err := ecsclient.NewClient(config)
	if err != nil {
		klog.Errorf("Failed to create ecs client:%v", err)
		return nil, err
	}
	return client, nil
}

func (a *AlibabaCloudPriceClient) ListRegionsInstancesPrice() map[string]*apis.RegionalInstancePrice {
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()

	ret := make(map[string]*apis.RegionalInstancePrice)
	for k, v := range a.priceData {
		ret[k] = v.DeepCopy()
	}
	return ret
}

func (a *AlibabaCloudPriceClient) ListInstancesPrice(region string) *map[string]apis.RegionalInstancePrice {
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()

	d, ok := a.priceData[region]
	if !ok {
		return nil
	}
	return &map[string]apis.RegionalInstancePrice{
		region: *d.DeepCopy(),
	}
}

func (a *AlibabaCloudPriceClient) GetInstancePrice(region, instanceType string) *apis.InstanceTypePrice {
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()

	regionData, ok := a.priceData[region]
	if !ok {
		return nil
	}
	d, ok := regionData.InstanceTypePrices[instanceType]
	if !ok {
		return nil
	}

	return d
}
