package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/uplapse/terraform-provider-uplapse/internal/provider"
)

// Regenerate docs/ from the schema and examples/ (builds the provider and runs the local terraform):
//go:generate go tool tfplugindocs generate --provider-name uplapse

// version is set by goreleaser.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/uplapse/uplapse",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
