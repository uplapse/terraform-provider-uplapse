package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/uplapse/terraform-provider-uplapse/internal/client"
)

var (
	_ datasource.DataSource              = (*monitorDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*monitorDataSource)(nil)
)

// NewMonitorDataSource returns the uplapse_monitor data source.
func NewMonitorDataSource() datasource.DataSource { return &monitorDataSource{} }

type monitorDataSource struct {
	client *client.Client
}

type monitorDataModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Type           types.String `tfsdk:"type"`
	URL            types.String `tfsdk:"url"`
	Port           types.Int64  `tfsdk:"port"`
	Interval       types.Int64  `tfsdk:"interval"`
	Timeout        types.Int64  `tfsdk:"timeout"`
	Locations      types.Set    `tfsdk:"locations"`
	Keyword        types.String `tfsdk:"keyword"`
	ExpectedStatus types.Int64  `tfsdk:"expected_status"`
	Status         types.String `tfsdk:"status"`
	Paused         types.Bool   `tfsdk:"paused"`
	Tags           types.Set    `tfsdk:"tags"`

	MailProtocol       types.String `tfsdk:"mail_protocol"`
	RequiredMtaStsMode types.String `tfsdk:"required_mta_sts_mode"`
	CheckBlacklists    types.Bool   `tfsdk:"check_blacklists"`
}

func (d *monitorDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_monitor"
}

func (d *monitorDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one monitor by id, for example one created in the app.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Required: true, MarkdownDescription: "Monitor id."},
			"name":            schema.StringAttribute{Computed: true, MarkdownDescription: "Monitor name."},
			"type":            schema.StringAttribute{Computed: true, MarkdownDescription: "Check type."},
			"url":             schema.StringAttribute{Computed: true, MarkdownDescription: "Checked address or host."},
			"port":            schema.Int64Attribute{Computed: true, MarkdownDescription: "Port."},
			"interval":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Seconds between checks."},
			"timeout":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Check timeout in seconds."},
			"locations":       schema.SetAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "City ids checked from."},
			"keyword":         schema.StringAttribute{Computed: true, MarkdownDescription: "Required keyword."},
			"expected_status": schema.Int64Attribute{Computed: true, MarkdownDescription: "Expected HTTP status."},
			"status":          schema.StringAttribute{Computed: true, MarkdownDescription: "`ACTIVE` or `SUSPENDED`."},
			"paused":          schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the monitor is paused."},
			"tags":            schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Tags."},
			"mail_protocol":   schema.StringAttribute{Computed: true, MarkdownDescription: "`MAIL_SERVER`: protocol checked."},
			"required_mta_sts_mode": schema.StringAttribute{Computed: true,
				MarkdownDescription: "`MAIL_AUTH`: MTA-STS mode required, if any."},
			"check_blacklists": schema.BoolAttribute{Computed: true,
				MarkdownDescription: "`MAIL_AUTH`: whether mail server addresses are checked against IP blacklists."},
		},
	}
}

func (d *monitorDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *monitorDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg monitorDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(cfg.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("id"), "Invalid monitor id", err.Error())
		return
	}
	m, err := d.client.GetMonitor(ctx, id)
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Monitor not found", fmt.Sprintf("No monitor %d in this organisation.", id))
			return
		}
		resp.Diagnostics.AddError("Could not read monitor", err.Error())
		return
	}
	// Reuse the resource's conversion, then copy the subset this data source exposes.
	var full monitorModel
	full.Locations = types.SetNull(types.Int64Type)
	full.Tags = types.SetNull(types.StringType)
	full.AlertUserIDs = types.SetNull(types.Int64Type)
	full.AlertContactGroupIDs = types.SetNull(types.Int64Type)
	full.Request = types.ObjectNull(requestAttrTypes)
	resp.Diagnostics.Append(full.apply(ctx, m, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out := monitorDataModel{
		ID: types.StringValue(strconv.FormatInt(m.ID, 10)), Name: full.Name, Type: full.Type, URL: full.URL,
		Port: full.Port, Interval: full.Interval, Timeout: full.Timeout, Locations: full.Locations,
		Keyword: full.Keyword, ExpectedStatus: full.ExpectedStatus, Status: full.Status, Paused: full.Paused,
		Tags: full.Tags, MailProtocol: full.MailProtocol, RequiredMtaStsMode: full.RequiredMtaStsMode,
		CheckBlacklists: full.CheckBlacklists,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
