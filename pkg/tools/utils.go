package tools

import (
	"os"
	"strings"

	"github.com/cloudpilot-ai/priceserver/pkg/apis"
)

func ExtractAlibabaCloudAKSKPool() map[string]string {
	akskPool := os.Getenv(apis.AlibabaCloudAKSKPoolEnv)
	if akskPool == "" {
		return nil
	}

	akskMap := make(map[string]string)
	for _, aksk := range strings.Split(akskPool, ",") {
		aksk = strings.TrimSpace(aksk)
		if aksk == "" {
			continue
		}
		akskArray := strings.Split(aksk, ":")
		if len(akskArray) != 2 {
			continue
		}
		akskMap[akskArray[0]] = akskArray[1]
	}

	return akskMap
}
