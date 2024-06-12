package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"k8s.io/klog"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
)

type QueryClientInterface interface {
	Run(ctx context.Context)
	ListInstancesDetails(region string) *apis.RegionalInstancePrice
	GetInstanceDetails(region, instanceType string) *apis.InstanceTypePrice
	TriggerRefreshData()
}

type QueryClientImpl struct {
	region      string
	awsEndpoint string

	triggerChannel chan struct{}

	awsMutex     sync.Mutex
	awsPriceData map[string]*apis.RegionalInstancePrice
}

func NewQueryClient(awsEndpoint, region string) (QueryClientInterface, error) {
	ret := &QueryClientImpl{
		region:         region,
		awsEndpoint:    awsEndpoint,
		triggerChannel: make(chan struct{}, 100),
		awsPriceData:   map[string]*apis.RegionalInstancePrice{},
	}
	if err := ret.refreshData(); err != nil {
		return nil, err
	}

	return ret, nil
}

func (q *QueryClientImpl) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute * 30)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.triggerChannel:
			_ = q.refreshData()
		case <-ticker.C:
			_ = q.refreshData()
		}
	}
}

func (q *QueryClientImpl) refreshSpecificInstanceTypeData(region, instanceType string) *apis.InstanceTypePrice {
	url := fmt.Sprintf("%s/api/v1/aws/ec2/regions/%s/types/%s/price", q.awsEndpoint, region, instanceType)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		klog.Errorf("Failed to create request: %v", err)
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		klog.Errorf("Failed to get price data: %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		klog.Errorf("Failed to get price data: %s", resp.Status)
		return nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Errorf("Failed to read price data: %v", err)
		return nil
	}

	var price apis.InstanceTypePrice
	err = json.Unmarshal(data, &price)
	if err != nil {
		klog.Errorf("Failed to unmarshal price data: %v", err)
		return nil
	}
	q.awsPriceData[region].InstanceTypePrices[instanceType] = &price

	return &price
}

func (q *QueryClientImpl) refreshData() error {
	url := fmt.Sprintf("%s/api/v1/aws/ec2/price", q.awsEndpoint)
	if q.region != "" {
		url = fmt.Sprintf("%s/api/v1/aws/ec2/regions/%s/price", q.awsEndpoint, q.region)
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		klog.Errorf("Failed to create request: %v", err)
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		klog.Errorf("Failed to get price data: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		klog.Errorf("Failed to get price data: %s", resp.Status)
		return err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Errorf("Failed to read price data: %v", err)
		return err
	}

	q.awsMutex.Lock()
	defer q.awsMutex.Unlock()
	err = json.Unmarshal(data, &q.awsPriceData)
	if err != nil {
		klog.Errorf("Failed to unmarshal price data: %v", err)
		return err
	}

	return nil
}

func (q *QueryClientImpl) ListInstancesDetails(region string) *apis.RegionalInstancePrice {
	q.awsMutex.Lock()
	defer q.awsMutex.Unlock()

	if _, ok := q.awsPriceData[region]; !ok {
		return nil
	}
	return q.awsPriceData[region].DeepCopy()
}

func (q *QueryClientImpl) GetInstanceDetails(region, instanceType string) *apis.InstanceTypePrice {
	q.awsMutex.Lock()
	defer q.awsMutex.Unlock()

	if _, ok := q.awsPriceData[region]; !ok {
		return nil
	}
	ret, ok := q.awsPriceData[region].InstanceTypePrices[instanceType]
	if !ok {
		return q.refreshSpecificInstanceTypeData(region, instanceType)
	}
	return ret
}

func (q *QueryClientImpl) TriggerRefreshData() {
	q.triggerChannel <- struct{}{}
}
