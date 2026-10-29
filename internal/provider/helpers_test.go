package provider

import (
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func tfjsonPath(name string) tfjsonpath.Path { return tfjsonpath.New(name) }

func knownString(s string) knownvalue.Check { return knownvalue.StringExact(s) }
