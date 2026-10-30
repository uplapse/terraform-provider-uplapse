package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/uplapse/terraform-provider-uplapse/internal/client"
)

var (
	_ resource.Resource                   = (*monitorResource)(nil)
	_ resource.ResourceWithConfigure      = (*monitorResource)(nil)
	_ resource.ResourceWithImportState    = (*monitorResource)(nil)
	_ resource.ResourceWithValidateConfig = (*monitorResource)(nil)
)

// NewMonitorResource returns the uplapse_monitor resource.
func NewMonitorResource() resource.Resource { return &monitorResource{} }

type monitorResource struct {
	client *client.Client
}

var httpMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

func (r *monitorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_monitor"
}

func (r *monitorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An Uplapse monitor: a check run on a schedule from one or more locations, alerting when it fails.\n\n" +
			"Settings this resource does not model (alerting conditions, authentication, TLS assertions, journeys...) " +
			"can be managed in the app: updates read the stored monitor and change only the attributes below.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Monitor id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Monitor name, unique within the organisation (up to 255 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Check type: `" + strings.Join(client.MonitorTypes, "`, `") + "`. " +
					"`HTTPS` covers plain HTTP too; `API` is an HTTP check with request settings; `PORT` only connects, " +
					"`TCP` also sends `tcp_payload` and expects `tcp_expect`.",
				Required:   true,
				Validators: []validator.String{stringvalidator.OneOf(client.MonitorTypes...)},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "What to check: an `http(s)://` address for HTTP-family types, a host name for " +
					"`PING`, `PORT`, `TCP`, `SSH`, `DNS`, `TLS_CERT`, `DOMAIN_EXPIRY` and `MAIL_SERVER`, a domain for `MAIL_AUTH`. " +
					"Required for every type except `HEARTBEAT`. Hosts on internal networks are refused.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"path": schema.StringAttribute{
				MarkdownDescription: "Request path kept apart from `url` (`API` and `KEYWORD` monitors). Must start with `/`.",
				Optional:            true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^/`), "must start with /"),
					stringvalidator.LengthAtMost(255),
				},
			},
			"port": schema.Int64Attribute{
				MarkdownDescription: "Port. Required for `PORT` and `TCP`; `HTTPS` monitors default to 443 or 80 from the URL scheme, " +
					"`MAIL_SERVER` monitors to the protocol's port (587, 465, 143, 993, 110 or 995). Port 25 cannot be monitored.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.Int64{int64validator.Between(1, 65535)},
				PlanModifiers: []planmodifier.Int64{defaultPortWhileUnchanged{}},
			},
			"interval": schema.Int64Attribute{
				MarkdownDescription: "Seconds between checks (1-86400). Defaults to the plan's shortest interval; " +
					"a shorter one than the plan allows is refused by the API.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.Int64{int64validator.Between(1, 86400)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"timeout": schema.Int64Attribute{
				MarkdownDescription: "Seconds before a check gives up (1-300, not longer than `interval`).",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{int64validator.Between(1, 300)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"locations": schema.SetAttribute{
				MarkdownDescription: "Ids of the cities to check from (see the `uplapse_locations` data source). " +
					"Several cities are checked on every run. How many a monitor may use depends on the plan.",
				ElementType: types.Int64Type,
				Optional:    true,
			},
			"location_target_type": schema.StringAttribute{
				MarkdownDescription: "Check from a broader area instead of listed cities: `COUNTRY` (value: country id), " +
					"`CONTINENT` (value: continent name), `PRIVATE` (value: private location id), or `CITY` (value: city id).",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.OneOf("CITY", "COUNTRY", "CONTINENT", "PRIVATE"),
					stringvalidator.AlsoRequires(path.MatchRoot("location_target_value")),
				},
			},
			"location_target_value": schema.StringAttribute{
				MarkdownDescription: "Value for `location_target_type`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("location_target_type"))},
			},
			"keyword": schema.StringAttribute{
				MarkdownDescription: "Text the response must contain. Required for `KEYWORD`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"expected_status": schema.Int64Attribute{
				MarkdownDescription: "HTTP status code the response must have (100-599).",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.Between(100, 599)},
			},
			"tcp_payload": schema.StringAttribute{
				MarkdownDescription: "`TCP`: data written after connecting.",
				Optional:            true,
			},
			"tcp_expect": schema.StringAttribute{
				MarkdownDescription: "`TCP`: text the response must contain. Without it a `TCP` check is just `PORT`.",
				Optional:            true,
			},
			"dns_record_type": schema.StringAttribute{
				MarkdownDescription: "`DNS`: record type to resolve: `" + strings.Join(client.DNSRecordTypes, "`, `") + "`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf(client.DNSRecordTypes...)},
			},
			"expected_dns": schema.StringAttribute{
				MarkdownDescription: "`DNS`: answer the record must contain (MX answers are `\"<preference> <host>\"`).",
				Optional:            true,
			},
			"min_tls_days_left": schema.Int64Attribute{
				MarkdownDescription: "`TLS_CERT`: fail once fewer days than this remain on the certificate.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"min_domain_days_left": schema.Int64Attribute{
				MarkdownDescription: "`DOMAIN_EXPIRY`: fail once fewer days than this remain on the domain registration.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"heartbeat_grace_seconds": schema.Int64Attribute{
				MarkdownDescription: "`HEARTBEAT`: seconds of lateness tolerated after `interval` before the monitor fails (API default 60).",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"heartbeat_url": schema.StringAttribute{
				MarkdownDescription: "`HEARTBEAT`: the address your job calls to report in. Anyone with it can report, so it is sensitive.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{useStateWhileTypeUnchanged{}},
			},
			"mail_protocol": schema.StringAttribute{
				MarkdownDescription: "`MAIL_SERVER`: the protocol to check: `" + strings.Join(client.MailProtocolNames, "`, `") + "`. " +
					"The check reads the greeting, sets up TLS (STARTTLS on `SUBMISSION`, `IMAP` and `POP3`) and checks the certificate. " +
					"With a username and password set in the app (Advanced > Authentication) it also logs in; it never sends or reads mail.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.OneOf(client.MailProtocolNames...)},
			},
			"required_mta_sts_mode": schema.StringAttribute{
				MarkdownDescription: "`MAIL_AUTH`: fail unless the domain publishes MTA-STS in at least this mode: `testing` or `enforce`. " +
					"Left out, MTA-STS is reported but not required.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.OneOf(client.MtaStsModes...)},
			},
			"check_blacklists": schema.BoolAttribute{
				MarkdownDescription: "`MAIL_AUTH`: also fail when one of the domain's mail server addresses is on a public IP blacklist " +
					"(SpamCop, Barracuda, PSBL, Mailspike, UCEPROTECT level 1, DroneBL).",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"track_config_changes": schema.BoolAttribute{
				MarkdownDescription: "Record DNS, TLS and security-header snapshots and report changes between checks.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"paused": schema.BoolAttribute{
				MarkdownDescription: "Pause the monitor: no checks and no alerts. The API refuses to pause a monitor " +
					"that is down outside a maintenance window.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`ACTIVE` or `SUSPENDED` (paused, by you or by a plan downgrade).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{statusFromPaused{}},
			},
			"tags": schema.SetAttribute{
				MarkdownDescription: "Free-form tags.",
				ElementType:         types.StringType,
				Optional:            true,
			},
			"request": schema.SingleNestedAttribute{
				MarkdownDescription: "HTTP request settings for `HTTPS`, `API` and `KEYWORD` monitors. When the block " +
					"is left out, request settings made in the app are kept.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"method": schema.StringAttribute{
						MarkdownDescription: "HTTP method (default `GET`).",
						Optional:            true,
						Validators:          []validator.String{stringvalidator.OneOf(httpMethods...)},
					},
					"headers": schema.MapAttribute{
						MarkdownDescription: "Request headers. Values are sensitive; the API encrypts them and never " +
							"returns credential-bearing ones, so changes to those made outside Terraform are not detected.",
						ElementType: types.StringType,
						Optional:    true,
						Sensitive:   true,
					},
					"body": schema.StringAttribute{
						MarkdownDescription: "Request body.",
						Optional:            true,
						Sensitive:           true,
					},
					"follow_redirects": schema.BoolAttribute{
						MarkdownDescription: "Follow redirects.",
						Optional:            true,
					},
					"verify_ssl": schema.BoolAttribute{
						MarkdownDescription: "Fail on an invalid TLS certificate.",
						Optional:            true,
					},
					"user_agent": schema.StringAttribute{
						MarkdownDescription: "User-Agent header.",
						Optional:            true,
					},
				},
			},
			"alert_user_ids": schema.SetAttribute{
				MarkdownDescription: "Ids of the organisation's users alerted when the monitor fails. Left out, the " +
					"recipients chosen in the app are kept; set `[]` to remove them all.",
				ElementType: types.Int64Type,
				Optional:    true,
			},
			"alert_contact_group_ids": schema.SetAttribute{
				MarkdownDescription: "Ids of contact groups alerted when the monitor fails. Left out, the groups chosen " +
					"in the app are kept; set `[]` to remove them all.",
				ElementType: types.Int64Type,
				Optional:    true,
			},
		},
	}
}

func (r *monitorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// ValidateConfig mirrors the API's cheap, certain rules so a mistake shows at plan time. Anything that depends on
// the plan or stored data (limits, name uniqueness, internal hosts) is left to the API.
func (r *monitorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m monitorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Type.IsUnknown() || m.Type.IsNull() {
		return
	}
	typ := m.Type.ValueString()
	if typ != "HEARTBEAT" && m.URL.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("url"), "Missing url", "url is required for "+typ+" monitors.")
	}
	if (typ == "PORT" || typ == "TCP") && m.Port.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("port"), "Missing port", "port is required for "+typ+" monitors.")
	}
	if typ == "MAIL_SERVER" {
		if m.MailProtocol.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("mail_protocol"), "Missing mail_protocol", "mail_protocol is required for MAIL_SERVER monitors.")
		}
		if !m.Port.IsNull() && !m.Port.IsUnknown() && m.Port.ValueInt64() == 25 {
			resp.Diagnostics.AddAttributeError(path.Root("port"), "Port 25 cannot be monitored",
				"The checking locations' cloud providers block port 25. Check the submission port (587 or 465), "+
					"or check inbound mail with a MAIL_AUTH monitor.")
		}
	} else if !m.MailProtocol.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("mail_protocol"), "mail_protocol not used", "mail_protocol only applies to MAIL_SERVER monitors.")
	}
	if typ != "MAIL_AUTH" {
		if !m.RequiredMtaStsMode.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("required_mta_sts_mode"), "required_mta_sts_mode not used",
				"required_mta_sts_mode only applies to MAIL_AUTH monitors.")
		}
		if !m.CheckBlacklists.IsNull() && !m.CheckBlacklists.IsUnknown() && m.CheckBlacklists.ValueBool() {
			resp.Diagnostics.AddAttributeError(path.Root("check_blacklists"), "check_blacklists not used",
				"check_blacklists only applies to MAIL_AUTH monitors.")
		}
	}
	if typ == "KEYWORD" && m.Keyword.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("keyword"), "Missing keyword", "keyword is required for KEYWORD monitors.")
	}
	if !m.Timeout.IsNull() && !m.Timeout.IsUnknown() && !m.Interval.IsNull() && !m.Interval.IsUnknown() &&
		m.Timeout.ValueInt64() > m.Interval.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("timeout"), "Timeout longer than interval",
			fmt.Sprintf("timeout (%ds) cannot be longer than the interval (%ds).", m.Timeout.ValueInt64(), m.Interval.ValueInt64()))
	}
	if !m.URL.IsNull() && !m.URL.IsUnknown() {
		u := m.URL.ValueString()
		if i := strings.Index(u, "://"); i >= 0 {
			if s := strings.ToLower(u[:i]); s != "http" && s != "https" {
				resp.Diagnostics.AddAttributeError(path.Root("url"), "Invalid url", "url must start with http:// or https://, or be a host name.")
			}
		}
	}
}

func (r *monitorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan monitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := plan.toInput(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateMonitor(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Could not create monitor", err.Error())
		return
	}
	// Save the id at once so a failure below does not leave an untracked monitor.
	plan.ID = types.StringValue(strconv.FormatInt(created.ID, 10))
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), plan.ID)...)

	resp.Diagnostics.Append(plan.apply(ctx, created, false)...)
	resp.Diagnostics.Append(r.readHeartbeat(ctx, created, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *monitorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state monitorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid monitor id", err.Error())
		return
	}
	m, err := r.client.GetMonitor(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Could not read monitor", err.Error())
		return
	}
	// A freshly imported resource has only its id: read every attribute, including the optional blocks.
	importing := state.Name.IsNull()
	resp.Diagnostics.Append(state.apply(ctx, m, importing)...)
	resp.Diagnostics.Append(r.readHeartbeat(ctx, m, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *monitorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state monitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid monitor id", err.Error())
		return
	}
	in, diags := plan.toInput(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The port is unknown only when defaultPortWhileUnchanged let the API choose it again.
	in.ResetPort = plan.Port.IsUnknown()
	updated, err := r.client.UpdateMonitor(ctx, id, in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update monitor", err.Error())
		return
	}
	plan.ID = state.ID
	resp.Diagnostics.Append(plan.apply(ctx, updated, false)...)
	resp.Diagnostics.Append(r.readHeartbeat(ctx, updated, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *monitorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state monitorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid monitor id", err.Error())
		return
	}
	if err := r.client.DeleteMonitor(ctx, id); err != nil {
		resp.Diagnostics.AddError("Could not delete monitor", err.Error())
	}
}

func (r *monitorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Import a monitor by its numeric id, e.g. `terraform import uplapse_monitor.site 123`.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// readHeartbeat fills heartbeat_url: the ping address of a HEARTBEAT monitor, null for other types.
func (r *monitorResource) readHeartbeat(ctx context.Context, m *client.Monitor, model *monitorModel) (diags diag.Diagnostics) {
	if m.Type != "HEARTBEAT" {
		model.HeartbeatURL = types.StringNull()
		return nil
	}
	h, err := r.client.GetHeartbeat(ctx, m.ID)
	if err != nil {
		diags.AddError("Could not read the heartbeat address", err.Error())
		return diags
	}
	model.HeartbeatURL = types.StringValue(r.client.HeartbeatURL(h))
	return nil
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("monitor id must be a positive number, got %q", s)
	}
	return id, nil
}

// statusFromPaused plans status from paused, so pausing shows SUSPENDED in the plan rather than "known after apply".
type statusFromPaused struct{}

func (statusFromPaused) Description(context.Context) string {
	return "status follows paused"
}
func (m statusFromPaused) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (statusFromPaused) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	var paused types.Bool
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("paused"), &paused)...)
	if paused.IsUnknown() || paused.IsNull() {
		return
	}
	if paused.ValueBool() {
		resp.PlanValue = types.StringValue(client.StatusSuspended)
	} else {
		resp.PlanValue = types.StringValue(client.StatusActive)
	}
}

// useStateWhileTypeUnchanged keeps a computed value from state unless the monitor type changes.
type useStateWhileTypeUnchanged struct{}

func (useStateWhileTypeUnchanged) Description(context.Context) string {
	return "kept from state while the monitor type is unchanged"
}
func (m useStateWhileTypeUnchanged) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (useStateWhileTypeUnchanged) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.State.Raw.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}
	var planType, stateType types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("type"), &planType)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("type"), &stateType)...)
	if planType.Equal(stateType) {
		resp.PlanValue = req.StateValue
	}
}

// defaultPortWhileUnchanged keeps an API-chosen port from state while nothing it was chosen from changes: the type,
// the URL scheme (HTTPS: 443 or 80) and the mail protocol (MAIL_SERVER). When one of them changes, an unset port is
// left unknown so the API picks the new default.
type defaultPortWhileUnchanged struct{}

func (defaultPortWhileUnchanged) Description(context.Context) string {
	return "kept from state while the type, URL scheme and mail protocol are unchanged"
}
func (m defaultPortWhileUnchanged) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (defaultPortWhileUnchanged) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.StateValue.IsNull() || req.State.Raw.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}
	var planType, stateType, planProto, stateProto, planURL, stateURL types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("type"), &planType)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("type"), &stateType)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("mail_protocol"), &planProto)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("mail_protocol"), &stateProto)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("url"), &planURL)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("url"), &stateURL)...)
	if planType.IsUnknown() || planProto.IsUnknown() || planURL.IsUnknown() {
		return
	}
	if planType.Equal(stateType) && planProto.Equal(stateProto) && urlScheme(planURL) == urlScheme(stateURL) {
		resp.PlanValue = req.StateValue
	}
}

// urlScheme is the lower-case scheme of an address, or "" for a bare host name.
func urlScheme(u types.String) string {
	if u.IsNull() {
		return ""
	}
	if i := strings.Index(u.ValueString(), "://"); i >= 0 {
		return strings.ToLower(u.ValueString()[:i])
	}
	return ""
}
