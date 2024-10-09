package main

import (
	"fmt"

	"github.com/cloudpilot-ai/priceserver/pkg/tools"
)

func main() {
	c, err := tools.NewQueryClient("https://pre-price.cloudpilot.ai", "alibabacloud", "")
	if err != nil {
		panic(err)
	}

	regions := c.ListRegions()
	fmt.Println(regions)
	d := c.ListInstancesDetails("cn-beijing")
	fmt.Println(d)
}
