package main

import (
	"fmt"

	"github.com/cloudpilot-ai/priceserver/pkg/tools"
)

func main() {
	c, err := tools.NewQueryClient("https://pre-price.cloudpilot.ai", "alibabacloud", "cn-beijing")
	if err != nil {
		panic(err)
	}

	d := c.ListInstancesDetails("cn-beijing")
	fmt.Println(d)
}
