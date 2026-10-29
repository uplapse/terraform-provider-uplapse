// Package provider implements the Uplapse Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/uplapse/terraform-provider-uplapse/internal/client"
)

const (
	envAPIKey   = "UPLAPSE_API_KEY"
	envEndpoint = "UPLAPSE_ENDPOINT"
)

var _ provider.Provider = (*uplapseProvider)(nil)

type uplapseProvider struct {
	version string
}

type providerModel struct {
	APIKey   types.String `tfsdk:"api_key"`
	Endpoint types.String `tfsdk:"endpoint"`
}

// New returns a provider factory for the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &uplapseProvider{version: version} }
}

func (p *uplapseProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "uplapse"
	resp.Version = p.version
}

func (p *uplapseProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [Uplapse](https://uplapse.com) uptime monitors as code.\n\n" +
			"Authenticate with an organisation API key (Settings > API keys in the app). Creating, changing and " +
			"deleting monitors needs a key with the `OWNER`, `DOMAIN_ADMIN` or `EDITOR` role; reading needs any role.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "Uplapse API key (`upm_...`). May also be set with the `" + envAPIKey + "` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Uplapse API. Defaults to `" + client.DefaultEndpoint + "`; may also be set with the `" + envEndpoint + "` environment variable.",
				Optional:            true,
			},
		},
	}
}

func (p *uplapseProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Unknown Uplapse API key",
			"The api_key depends on a value not known until apply. Set it statically or through the "+envAPIKey+" environment variable.")
	}
	if cfg.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Unknown Uplapse endpoint",
			"The endpoint depends on a value not known until apply. Set it statically or through the "+envEndpoint+" environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := os.Getenv(envAPIKey)
	if !cfg.APIKey.IsNull() {
		apiKey = cfg.APIKey.ValueString()
	}
	endpoint := os.Getenv(envEndpoint)
	if !cfg.Endpoint.IsNull() {
		endpoint = cfg.Endpoint.ValueString()
	}
	if endpoint == "" {
		endpoint = client.DefaultEndpoint
	}
	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing Uplapse API key",
			"Set api_key in the provider block or the "+envAPIKey+" environment variable. Create a key under Settings > API keys in the Uplapse app.")
		return
	}

	c, err := client.New(endpoint, apiKey, client.WithUserAgent("terraform-provider-uplapse/"+p.version))
	if err != nil {
		resp.Diagnostics.AddError("Invalid Uplapse provider configuration", err.Error())
		return
	}
	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *uplapseProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewMonitorResource}
}

func (p *uplapseProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewLocationsDataSource, NewMonitorDataSource}
}
