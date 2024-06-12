package main

import (
	"os"

	pkgserver "k8s.io/apiserver/pkg/server"
	"k8s.io/component-base/cli"

	"github.com/cloudpilot-ai/priceserver/cmd/app"
)

func main() {
	ctx := pkgserver.SetupSignalContext()
	cmd := app.NewPriceServerCommand(ctx)
	code := cli.Run(cmd)
	os.Exit(code)
}
