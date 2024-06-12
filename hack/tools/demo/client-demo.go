package main

import (
	"fmt"

	"github.com/cloudpilot-ai/priceserver/pkg/tools"
)

func main() {
	c, err := tools.NewQueryClient("https://pre-price.cloudpilot.ai", "")
	if err != nil {
		panic(err)
	}

	d := c.GetInstanceDetails("us-east-2", "t2.xlarge")
	fmt.Println(d)
}
