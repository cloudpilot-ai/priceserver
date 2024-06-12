package app

import (
	"context"
	"flag"

	"github.com/spf13/cobra"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog"

	"github.com/cloudpilot-ai/priceserver/cmd/app/options"
	"github.com/cloudpilot-ai/priceserver/pkg/apiserver/router"
	"github.com/cloudpilot-ai/priceserver/pkg/client"
	"github.com/cloudpilot-ai/priceserver/pkg/version"
)

func NewPriceServerCommand(ctx context.Context) *cobra.Command {
	opts := options.NewOptions()

	cmd := &cobra.Command{
		Use:  "priceserver",
		Long: "priceserver used to serve as the apiserver for price data query",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.ApplyAndValidate(); err != nil {
				return err
			}
			if err := run(ctx, opts); err != nil {
				return err
			}
			return nil
		},
	}

	fss := cliflag.NamedFlagSets{}
	logFlagSet := fss.FlagSet("log")
	klog.InitFlags(flag.CommandLine)
	logFlagSet.AddGoFlagSet(flag.CommandLine)
	cmd.Flags().AddFlagSet(logFlagSet)

	return cmd
}

func run(ctx context.Context, opts *options.Options) error {
	klog.Infof("Start cloudpilot-agent, version: %s, commit: %s...", version.Get().GitVersion, version.Get().GitCommit)

	awsPriceClient, err := client.NewAWSPriceClient(opts.AWSGlobalAK, opts.AWSGlobalSK, opts.AWSCNAK, opts.AWSCNSK, true)
	if err != nil {
		return err
	}
	alibabaCloudClient, err := client.NewAlibabaCloudPriceClient(opts.AlibabaCloudAKSKPool, true)
	if err != nil {
		return err
	}

	serverRouter := router.NewPriceServerRouter(awsPriceClient, alibabaCloudClient)

	go awsPriceClient.Run(ctx)
	go alibabaCloudClient.Run(ctx)
	if err := serverRouter.Run(":8080"); err != nil {
		klog.Fatalf("Failed to start priceserver router: %v", err)
	}

	<-ctx.Done()
	return nil
}
