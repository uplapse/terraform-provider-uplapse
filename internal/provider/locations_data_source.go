package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/uplapse/terraform-provider-uplapse/internal/client"
)

var (
	_ datasource.DataSource              = (*locationsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*locationsDataSource)(nil)
)

// NewLocationsDataSource returns the uplapse_locations data source.
func NewLocationsDataSource() datasource.DataSource { return &locationsDataSource{} }

type locationsDataSource struct {
	client *client.Client
}

type locationsModel struct {
	Providers types.List      `tfsdk:"providers"`
	Continent types.String    `tfsdk:"continent"`
	Country   types.String    `tfsdk:"country"`
	Locations []locationModel `tfsdk:"locations"`
}

type locationModel struct {
	ID        types.Int64  `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Country   types.String `tfsdk:"country"`
	CountryID types.Int64  `tfsdk:"country_id"`
	Continent types.String `tfsdk:"continent"`
}

func (d *locationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_locations"
}

func (d *locationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Cities monitors can be checked from. Use their `id` in `uplapse_monitor.locations`.",
		Attributes: map[string]schema.Attribute{
			"providers": schema.ListAttribute{
				MarkdownDescription: "Only cities served by these clouds (`aws`, `gcp`, `azure`). Defaults to all three.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators: []validator.List{listvalidator.ValueStringsAre(
					stringvalidator.OneOf("aws", "gcp", "azure"))},
			},
			"continent": schema.StringAttribute{
				MarkdownDescription: "Only cities on this continent (case-insensitive, e.g. `Europe`).",
				Optional:            true,
			},
			"country": schema.StringAttribute{
				MarkdownDescription: "Only cities in this country (case-insensitive country name, e.g. `Germany`).",
				Optional:            true,
			},
			"locations": schema.ListNestedAttribute{
				MarkdownDescription: "Matching cities, sorted by continent, country and name.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.Int64Attribute{Computed: true, MarkdownDescription: "City id."},
						"name":       schema.StringAttribute{Computed: true, MarkdownDescription: "City name."},
						"country":    schema.StringAttribute{Computed: true, MarkdownDescription: "Country name."},
						"country_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "Country id (for `location_target_type = \"COUNTRY\"`)."},
						"continent":  schema.StringAttribute{Computed: true, MarkdownDescription: "Continent (region) name."},
					},
				},
			},
		},
	}
}

func (d *locationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

func (d *locationsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg locationsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var providers []string
	if !cfg.Providers.IsNull() {
		resp.Diagnostics.Append(cfg.Providers.ElementsAs(ctx, &providers, false)...)
	}
	locs, err := d.client.ListLocations(ctx, providers)
	if err != nil {
		resp.Diagnostics.AddError("Could not list locations", err.Error())
		return
	}
	cfg.Locations = []locationModel{}
	for _, l := range locs {
		if !cfg.Continent.IsNull() && !strings.EqualFold(cfg.Continent.ValueString(), l.Continent) {
			continue
		}
		if !cfg.Country.IsNull() && !strings.EqualFold(cfg.Country.ValueString(), l.CountryName) {
			continue
		}
		cfg.Locations = append(cfg.Locations, locationModel{
			ID:        types.Int64Value(l.ID),
			Name:      types.StringValue(l.Name),
			Country:   types.StringValue(l.CountryName),
			CountryID: types.Int64Value(l.CountryID),
			Continent: types.StringValue(l.Continent),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
