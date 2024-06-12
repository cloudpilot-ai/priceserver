package options

import (
	"fmt"
	"os"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
	"github.com/cloudpilot-ai/priceserver/pkg/tools"
)

type Options struct {
	AWSGlobalAK string
	AWSGlobalSK string
	AWSCNAK     string
	AWSCNSK     string

	AlibabaCloudAKSKPool map[string]string
}

func NewOptions() *Options {
	return &Options{}
}

func (o *Options) ApplyAndValidate() error {
	o.AWSGlobalAK = os.Getenv(apis.AWSGlobalAKEnv)
	if o.AWSGlobalAK == "" {
		return fmt.Errorf("aws global access key is not set")
	}
	o.AWSGlobalSK = os.Getenv(apis.AWSGlobalSKEnv)
	if o.AWSGlobalSK == "" {
		return fmt.Errorf("aws global secret key is not set")
	}
	o.AWSCNAK = os.Getenv(apis.AWSCNAKEnv)
	if o.AWSCNAK == "" {
		return fmt.Errorf("aws china access key is not set")
	}
	o.AWSCNSK = os.Getenv(apis.AWSCNSKEnv)
	if o.AWSCNSK == "" {
		return fmt.Errorf("aws china secret key is not set")
	}
	o.AlibabaCloudAKSKPool = tools.ExtractAlibabaCloudAKSKPool()
	if len(o.AlibabaCloudAKSKPool) == 0 {
		return fmt.Errorf("alibaba cloud access key and secret key pool is not set")
	}

	return nil
}
