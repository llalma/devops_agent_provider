package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/llalma/devops_agent_provider/internal/provider"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/llalma/devops_agent_provider",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), provider.New(opts.Address), opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}
