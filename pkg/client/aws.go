package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	savingsplanstypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
	"github.com/samber/lo"
	"k8s.io/klog"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
)

type PriceItem struct {
	Product struct {
		Attributes struct {
			InstanceType string `json:"instanceType"`
			VCPU         string `json:"vcpu"`
			Memory       string `json:"memory"`
			GPU          string `json:"gpu"`
		} `json:"attributes"`
	} `json:"product"`
	Terms struct {
		OnDemand map[string]struct {
			PriceDimensions map[string]struct {
				PricePerUnit map[string]string `json:"pricePerUnit"`
			} `json:"priceDimensions"`
		} `json:"onDemand"`
	} `json:"terms"`
}

type AWSPriceClient struct {
	globalAK string
	globalSK string
	cnAK     string
	cnSK     string

	triggerChannel chan apis.RegionTypeKey

	dataMutex sync.Mutex
	priceData map[string]*apis.RegionalInstancePrice
}

func NewAWSPriceClient(globalAK, globalSK, cnAK, cnSK string, initialSpotUpdate bool) (*AWSPriceClient, error) {
	data, err := file.ReadFile("builtin-data/aws_price.json")
	if err != nil {
		return nil, err
	}

	client := &AWSPriceClient{
		globalAK:       globalAK,
		globalSK:       globalSK,
		cnAK:           cnAK,
		cnSK:           cnSK,
		triggerChannel: make(chan apis.RegionTypeKey, 100),
		priceData:      map[string]*apis.RegionalInstancePrice{},
	}
	if err := json.Unmarshal(data, &client.priceData); err != nil {
		return nil, err
	}

	if initialSpotUpdate {
		client.refreshSpotPrices("", "")
	}

	return client, nil
}

func (a *AWSPriceClient) Run(ctx context.Context) {
	odTicker := time.NewTicker(time.Hour * 24 * 7)
	defer odTicker.Stop()

	spotTicker := time.NewTicker(time.Minute * 30)
	defer spotTicker.Stop()

	for {
		select {
		case <-odTicker.C:
			a.RefreshOnDemandPrice("", "")
			a.RefreshSavingsPlanPrice("", "")
		case <-spotTicker.C:
			a.refreshSpotPrices("", "")
		case <-ctx.Done():
			return
		case k := <-a.triggerChannel:
			a.RefreshOnDemandPrice(k.Region, k.InstanceType)
			a.RefreshSavingsPlanPrice(k.Region, k.InstanceType)
			a.refreshSpotPrices(k.Region, k.InstanceType)
		}
	}
}

func (a *AWSPriceClient) putSpotPriceData(region string, priceData []types.SpotPrice) {
	a.dataMutex.Lock()
	defer a.dataMutex.Unlock()

	if _, ok := a.priceData[region]; !ok {
		a.priceData[region] = &apis.RegionalInstancePrice{
			InstanceTypePrices: make(map[string]*apis.InstanceTypePrice),
		}
	}

	for _, item := range priceData {
		instanceType := string(item.InstanceType)
		price, err := strconv.ParseFloat(*item.SpotPrice, 64)
		if err != nil || price == 0 {
			klog.Errorf("Failed to parse price, %v", err)
			continue
		}

		d, ok := a.priceData[region].InstanceTypePrices[instanceType]
		if !ok {
			continue
		}
		if d.SpotPricePerHour == nil {
			d.SpotPricePerHour = make(map[string]float64)
		}

		d.SpotPricePerHour[*item.AvailabilityZone] = price
		a.priceData[region].InstanceTypePrices[instanceType] = d
	}
}

func (a *AWSPriceClient) newEC2Client(region string) (*ec2.Client, error) {
	ak := a.globalAK
	sk := a.globalSK
	if strings.HasPrefix(region, "cn-") {
		ak = a.cnAK
		sk = a.cnSK
	}
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		klog.Errorf("failed to load config, %v", err)
		return nil, err
	}
	return ec2.NewFromConfig(cfg), nil
}

var spotBaseFilter = []types.Filter{
	{Name: aws.String("product-description"), Values: []string{"Linux/UNIX"}},
}

func (a *AWSPriceClient) handleSpotPrice(region string, filters []types.Filter) {
	client, err := a.newEC2Client(region)
	if err != nil {
		return
	}
	startTime := aws.Time(time.Now())
	token := ""
	for {
		input := &ec2.DescribeSpotPriceHistoryInput{
			Filters:   filters,
			StartTime: startTime,
			NextToken: nil,
		}
		if token != "" {
			input.NextToken = aws.String(token)
		}

		data, err := client.DescribeSpotPriceHistory(context.Background(), input)
		if err != nil {
			klog.Errorf("failed to get spot price(%s), %v", region, err)
			return
		}

		if data.NextToken != nil {
			token = *data.NextToken
		}

		a.putSpotPriceData(region, data.SpotPriceHistory)

		if data.NextToken == nil || *data.NextToken == "" {
			break
		}
	}
}

func (a *AWSPriceClient) refreshSpotPrices(region, instanceType string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	filters := spotBaseFilter
	if instanceType != "" {
		filters = append(filters, types.Filter{
			Name:   aws.String("instance-type"),
			Values: []string{instanceType},
		})
	}

	list, err := a.listRegions()
	if err != nil {
		return
	}
	if region != "" {
		list = []string{region}
	}

	handleFunc := func(region string) {
		defer wg.Done()

		sem <- struct{}{}
		defer func() {
			<-sem
		}()

		a.handleSpotPrice(region, filters)
	}

	for _, region := range list {
		klog.Infof("Start to handle region %s", region)
		wg.Add(1)
		go handleFunc(region)
	}

	wg.Wait()
	klog.Infof("All spot prices are refreshed for AWS")
}

func resolvePricingEndpointRegion(region string) string {
	// pricing API doesn't have an endpoint in all regions
	pricingAPIRegion := "us-east-1"
	if strings.HasPrefix(region, "ap-") {
		pricingAPIRegion = "ap-south-1"
	} else if strings.HasPrefix(region, "cn-") {
		pricingAPIRegion = "cn-northwest-1"
	} else if strings.HasPrefix(region, "eu-") {
		pricingAPIRegion = "eu-central-1"
	}
	return pricingAPIRegion
}

func (a *AWSPriceClient) newPriceClient(region string) (*pricing.Client, error) {
	ak := a.globalAK
	sk := a.globalSK
	if strings.HasPrefix(region, "cn-") {
		ak = a.cnAK
		sk = a.cnSK
	}
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		klog.Errorf("failed to load config, %v", err)
		return nil, err
	}

	return pricing.NewFromConfig(cfg), nil
}

func (a *AWSPriceClient) newSavingsPlanClient(region string) (*savingsplans.Client, error) {
	ak := a.globalAK
	sk := a.globalSK
	if strings.HasPrefix(region, "cn-") {
		ak = a.cnAK
		sk = a.cnSK
	}

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")))
	if err != nil {
		klog.Errorf("failed to load config, %v", err)
		return nil, err
	}

	return savingsplans.NewFromConfig(cfg), nil
}

var onDemandBaseFilters = []pricingtypes.Filter{
	{
		Field: aws.String("tenancy"),
		Type:  pricingtypes.FilterTypeTermMatch,
		Value: aws.String("Shared"),
	},
	{
		Field: aws.String("productFamily"),
		Type:  pricingtypes.FilterTypeTermMatch,
		Value: aws.String("Compute Instance"),
	},
	{
		Field: aws.String("serviceCode"),
		Type:  pricingtypes.FilterTypeTermMatch,
		Value: aws.String("AmazonEC2"),
	},
	{
		Field: aws.String("preInstalledSw"),
		Type:  pricingtypes.FilterTypeTermMatch,
		Value: aws.String("NA"),
	},
	{
		Field: aws.String("operatingSystem"),
		Type:  pricingtypes.FilterTypeTermMatch,
		Value: aws.String("Linux"),
	},
	{
		Field: aws.String("capacitystatus"),
		Type:  pricingtypes.FilterTypeTermMatch,
		Value: aws.String("Used"),
	},
	{
		Field: aws.String("marketoption"),
		Type:  pricingtypes.FilterTypeTermMatch,
		Value: aws.String("OnDemand"),
	},
}

func (a *AWSPriceClient) handleOnDemandPrice(region string, filters []pricingtypes.Filter) {
	zones, err := a.getAvailableZones(region)
	if err != nil {
		klog.Errorf("failed to get available zones, %v", err)
		return
	}

	client, err := a.newPriceClient(resolvePricingEndpointRegion(region))
	if err != nil {
		return
	}

	currentFilter := []pricingtypes.Filter{
		{
			Field: aws.String("regionCode"),
			Type:  pricingtypes.FilterTypeTermMatch,
			Value: aws.String(region),
		},
	}
	currentFilter = append(currentFilter, filters...)

	token := ""
	for {
		input := &pricing.GetProductsInput{
			ServiceCode: aws.String("AmazonEC2"),
			Filters:     currentFilter,
			NextToken:   nil,
		}
		if token != "" {
			input.NextToken = aws.String(token)
		}

		data, err := client.GetProducts(context.Background(), input)
		if err != nil {
			klog.Errorf("failed to get ondemand price, %v", err)
			return
		}

		if data.NextToken != nil {
			token = *data.NextToken
		}

		a.putOnDemandPriceData(region, zones, data.PriceList)

		if data.NextToken == nil || *data.NextToken == "" {
			break
		}
	}
}

func (a *AWSPriceClient) RefreshOnDemandPrice(region, instanceType string) {
	filters := onDemandBaseFilters
	if instanceType != "" {
		filters = append(filters, pricingtypes.Filter{
			Field: aws.String("instanceType"),
			Type:  pricingtypes.FilterTypeTermMatch,
			Value: aws.String(instanceType),
		})
	}

	list, err := a.listRegions()
	if err != nil {
		return
	}
	if region != "" {
		list = []string{region}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	handleFunc := func(region string) {
		defer wg.Done()
		sem <- struct{}{}
		defer func() {
			<-sem
		}()

		a.handleOnDemandPrice(region, filters)
	}

	for _, region := range list {
		klog.Infof("Start to handle region %s for on-demand", region)
		wg.Add(1)
		go handleFunc(region)
	}

	wg.Wait()
	klog.Infof("All ondemand prices are refreshed")
}

func (a *AWSPriceClient) listRegions() ([]string, error) {
	globalEC2Client, err := a.newEC2Client("us-east-2")
	if err != nil {
		return nil, err
	}

	globalOutput, err := globalEC2Client.DescribeRegions(context.Background(), &ec2.DescribeRegionsInput{AllRegions: aws.Bool(true)})
	if err != nil {
		klog.Errorf("Failed to list all global regions:%v", err)
		return nil, err
	}

	ret := lo.Map(globalOutput.Regions, func(item types.Region, index int) string {
		return aws.ToString(item.RegionName)
	})

	cnEC2Client, err := a.newEC2Client("cn-north-1")
	if err != nil {
		return nil, err
	}

	cnOutput, err := cnEC2Client.DescribeRegions(context.Background(), &ec2.DescribeRegionsInput{AllRegions: aws.Bool(true)})
	if err != nil {
		klog.Errorf("Failed to list all cn regions:%v", err)
		return nil, err
	}

	ret = append(ret, lo.Map(cnOutput.Regions, func(item types.Region, index int) string {
		return aws.ToString(item.RegionName)
	})...)

	return ret, nil
}

func (a *AWSPriceClient) handleSavingsPlanPrice(region string,
	baseFilters []savingsplanstypes.SavingsPlanOfferingRateFilterElement) {
	filters := append(baseFilters, savingsplanstypes.SavingsPlanOfferingRateFilterElement{
		Name: savingsplanstypes.SavingsPlanRateFilterAttributeRegion,
		Values: []string{
			region,
		},
	})
	queryPara := &savingsplans.DescribeSavingsPlansOfferingRatesInput{
		Filters: filters,
		SavingsPlanPaymentOptions: []savingsplanstypes.SavingsPlanPaymentOption{
			savingsplanstypes.SavingsPlanPaymentOptionAllUpfront,
			savingsplanstypes.SavingsPlanPaymentOptionPartialUpfront,
			savingsplanstypes.SavingsPlanPaymentOptionNoUpfront,
		},
		SavingsPlanTypes: []savingsplanstypes.SavingsPlanType{
			savingsplanstypes.SavingsPlanTypeCompute,
			savingsplanstypes.SavingsPlanTypeEc2Instance,
		},
	}
	client, err := a.newSavingsPlanClient(region)
	if err != nil {
		klog.Errorf("failed to create savings plan client, %v", err)
		return
	}

	token := ""
	for {
		input := queryPara
		if token != "" {
			input.NextToken = aws.String(token)
		}

		data, err := client.DescribeSavingsPlansOfferingRates(context.Background(), input)
		if err != nil {
			klog.Errorf("failed to get savings plan price, %v", err)
			return
		}

		if aws.ToString(data.NextToken) != "" {
			token = *data.NextToken
		}

		a.putSavingsPlanPriceData(region, data.SearchResults)

		if data.NextToken == nil || *data.NextToken == "" {
			break
		}
	}
}

func (a *AWSPriceClient) RefreshSavingsPlanPrice(region, instanceType string) {
	baseFilters := []savingsplanstypes.SavingsPlanOfferingRateFilterElement{
		{
			Name: savingsplanstypes.SavingsPlanRateFilterAttributeProductDescription,
			Values: []string{
				"Linux/UNIX",
			},
		},
		{
			Name: savingsplanstypes.SavingsPlanRateFilterAttributeTenancy,
			Values: []string{
				"shared",
			},
		},
	}
	if instanceType != "" {
		baseFilters = append(baseFilters, savingsplanstypes.SavingsPlanOfferingRateFilterElement{
			Name: savingsplanstypes.SavingsPlanRateFilterAttributeInstanceType,
			Values: []string{
				instanceType,
			},
		})
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	handleFunc := func(region string) {
		defer wg.Done()

		sem <- struct{}{}
		defer func() {
			<-sem
		}()

		a.handleSavingsPlanPrice(region, baseFilters)
	}

	list, err := a.listRegions()
	if err != nil {
		return
	}

	if region != "" {
		list = []string{region}
	}

	for _, region := range list {
		klog.Infof("Start to handle region %s for saving plan", region)
		wg.Add(1)
		go handleFunc(region)
	}

	wg.Wait()
	klog.Infof("All savings plan prices are refreshed")
}

func (a *AWSPriceClient) getAvailableZones(region string) ([]string, error) {
	client, err := a.newEC2Client(region)
	if err != nil {
		klog.Errorf("failed to create ec2 client, %v", err)
		return nil, err
	}
	in := ec2.DescribeAvailabilityZonesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("region-name"),
				Values: []string{region},
			},
		},
	}

	out, err := client.DescribeAvailabilityZones(context.Background(), &in)
	if err != nil {
		klog.Errorf("failed to get available zones for %s, %v", region, err)
		return nil, err
	}

	var ret []string
	for _, a := range out.AvailabilityZones {
		ret = append(ret, *a.ZoneName)
	}

	return ret, nil
}

func extractArch(instanceType string) (string, error) {
	// Following logic is based on: https://docs.aws.amazon.com/zh_cn/ec2/latest/instancetypes/instance-type-names.html
	if len(instanceType) < 3 {
		return "", fmt.Errorf("instance type %s is invalid", instanceType)
	}
	pre := instanceType[:3]
	arch := pre[2]
	switch arch {
	case 'a', 'i':
		return "amd64", nil
	case 'g':
		return "arm64", nil
	default:
		return "amd64", nil
	}
}

func extractMemory(memory string) (float64, error) {
	s := strings.TrimSuffix(memory, " GiB")
	return strconv.ParseFloat(s, 64)
}

func extractInstanceType(props []savingsplanstypes.SavingsPlanOfferingRateProperty) (string, error) {
	for _, v := range props {
		if *v.Name == "instanceType" {
			return *v.Value, nil
		}
	}

	return "", fmt.Errorf("failed to extract instance family")
}

func extractPaymentOption(op savingsplanstypes.SavingsPlanPaymentOption) apis.AWSEC2SPPaymentOption {
	switch op {
	case savingsplanstypes.SavingsPlanPaymentOptionAllUpfront:
		return apis.AWSEC2SPPaymentOptionAllUpfront
	case savingsplanstypes.SavingsPlanPaymentOptionPartialUpfront:
		return apis.AWSEC2SPPaymentOptionPartialUpfront
	default:
		return apis.AWSEC2SPPaymentOptionNoUpfront
	}
}

func (a *AWSPriceClient) putSavingsPlanPriceData(region string, rate []savingsplanstypes.SavingsPlanOfferingRate) {
	a.dataMutex.Lock()
	defer a.dataMutex.Unlock()

	for _, r := range rate {
		planType := r.SavingsPlanOffering.PlanType
		termLength := fmt.Sprintf("%dyr", r.SavingsPlanOffering.DurationSeconds/(60*60*24*365))
		paymentOption := extractPaymentOption(r.SavingsPlanOffering.PaymentOption)
		instanceType, err := extractInstanceType(r.Properties)
		if err != nil {
			klog.Errorf("failed to extract instance type, %v", err)
			continue
		}
		key := fmt.Sprintf("%s/%s/%s", planType, termLength, paymentOption)

		d, ok := a.priceData[region]
		if !ok {
			d = &apis.RegionalInstancePrice{
				InstanceTypePrices: map[string]*apis.InstanceTypePrice{},
			}
		}
		ins, ok := d.InstanceTypePrices[instanceType]
		if !ok {
			ins = &apis.InstanceTypePrice{}
		}
		if ins.AWSEC2Billing == nil {
			ins.AWSEC2Billing = map[string]apis.AWSEC2Billing{}
		}
		rate, err := strconv.ParseFloat(*r.Rate, 64)
		if err != nil {
			klog.Errorf("failed to parse rate to float, %v", err)
			continue
		}

		ins.AWSEC2Billing[key] = apis.AWSEC2Billing{Rate: rate}

		d.InstanceTypePrices[instanceType] = ins
		a.priceData[region] = d
	}
}

func (a *AWSPriceClient) putOnDemandPriceData(region string, zones []string, priceData []string) {
	storeFunc := func(item PriceItem) {
		a.dataMutex.Lock()
		defer a.dataMutex.Unlock()

		d, ok := a.priceData[region]
		if !ok {
			d = &apis.RegionalInstancePrice{
				InstanceTypePrices: map[string]*apis.InstanceTypePrice{},
			}
		}
		ins, ok := d.InstanceTypePrices[item.Product.Attributes.InstanceType]
		if !ok {
			ins = &apis.InstanceTypePrice{}
		}
		ins.Zones = zones

		var err error
		ins.Arch, err = extractArch(item.Product.Attributes.InstanceType)
		if err != nil {
			return
		}
		ins.VCPU, err = strconv.ParseFloat(item.Product.Attributes.VCPU, 64)
		if err != nil {
			klog.Errorf("failed to parse vcpu, %v", err)
			return
		}
		ins.Memory, err = extractMemory(item.Product.Attributes.Memory)
		if err != nil {
			klog.Errorf("failed to parse memory, %v", err)
			return
		}

		if item.Product.Attributes.GPU != "" {
			ins.GPU, err = strconv.ParseFloat(item.Product.Attributes.GPU, 64)
			if err != nil {
				klog.Errorf("failed to parse gpu, %v", err)
				return
			}
		}

		currency := "USD"
		if strings.HasPrefix(region, "cn-") {
			currency = "CNY"
		}
		for _, term := range item.Terms.OnDemand {
			for _, v := range term.PriceDimensions {
				price, err := strconv.ParseFloat(v.PricePerUnit[currency], 64)
				if err != nil || price == 0 {
					continue
				}
				ins.OnDemandPricePerHour = price
			}
		}

		d.InstanceTypePrices[item.Product.Attributes.InstanceType] = ins
		a.priceData[region] = d
	}

	for _, outer := range priceData {
		var pItem PriceItem
		err := json.Unmarshal([]byte(outer), &pItem)
		if err != nil {
			klog.Errorf("failed to unmarshal, %v", err)
			continue
		}
		storeFunc(pItem)
	}
}

func (a *AWSPriceClient) ListRegionsInstancesPrice() map[string]*apis.RegionalInstancePrice {
	a.dataMutex.Lock()
	defer a.dataMutex.Unlock()

	ret := make(map[string]*apis.RegionalInstancePrice)
	for k, v := range a.priceData {
		ret[k] = v.DeepCopy()
		// TODO: this line is used to ensure the api compatibility, we should remove this line in the future
		ret[k].InstanceTypeEC2Price = ret[k].InstanceTypePrices
	}
	return ret
}

func (a *AWSPriceClient) ListInstancesPrice(region string) *map[string]apis.RegionalInstancePrice {
	a.dataMutex.Lock()
	defer a.dataMutex.Unlock()

	d, ok := a.priceData[region]
	if !ok {
		return nil
	}

	regionData := d.DeepCopy()
	// TODO: this line is used to ensure the api compatibility, we should remove this line in the future
	regionData.InstanceTypeEC2Price = regionData.InstanceTypePrices

	ret := map[string]apis.RegionalInstancePrice{
		region: *regionData,
	}

	return &ret
}

func (a *AWSPriceClient) GetInstancePrice(region, instanceType string) *apis.InstanceTypePrice {
	a.dataMutex.Lock()
	defer a.dataMutex.Unlock()

	regionData, ok := a.priceData[region]
	if !ok {
		return nil
	}
	d, ok := regionData.InstanceTypePrices[instanceType]
	if !ok {
		a.triggerChannel <- apis.RegionTypeKey{Region: region, InstanceType: instanceType}
		return nil
	}

	return d
}
