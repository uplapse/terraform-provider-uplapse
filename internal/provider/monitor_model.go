package provider

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/uplapse/terraform-provider-uplapse/internal/client"
)

type monitorModel struct {
	ID                    types.String `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	Type                  types.String `tfsdk:"type"`
	URL                   types.String `tfsdk:"url"`
	Path                  types.String `tfsdk:"path"`
	Port                  types.Int64  `tfsdk:"port"`
	Interval              types.Int64  `tfsdk:"interval"`
	Timeout               types.Int64  `tfsdk:"timeout"`
	Locations             types.Set    `tfsdk:"locations"`
	LocationTargetType    types.String `tfsdk:"location_target_type"`
	LocationTargetValue   types.String `tfsdk:"location_target_value"`
	Keyword               types.String `tfsdk:"keyword"`
	ExpectedStatus        types.Int64  `tfsdk:"expected_status"`
	TCPPayload            types.String `tfsdk:"tcp_payload"`
	TCPExpect             types.String `tfsdk:"tcp_expect"`
	DNSRecordType         types.String `tfsdk:"dns_record_type"`
	ExpectedDNS           types.String `tfsdk:"expected_dns"`
	MinTLSDaysLeft        types.Int64  `tfsdk:"min_tls_days_left"`
	MinDomainDaysLeft     types.Int64  `tfsdk:"min_domain_days_left"`
	HeartbeatGraceSeconds types.Int64  `tfsdk:"heartbeat_grace_seconds"`
	HeartbeatURL          types.String `tfsdk:"heartbeat_url"`
	MailProtocol          types.String `tfsdk:"mail_protocol"`
	RequiredMtaStsMode    types.String `tfsdk:"required_mta_sts_mode"`
	CheckBlacklists       types.Bool   `tfsdk:"check_blacklists"`
	TrackConfigChanges    types.Bool   `tfsdk:"track_config_changes"`
	Paused                types.Bool   `tfsdk:"paused"`
	Status                types.String `tfsdk:"status"`
	Tags                  types.Set    `tfsdk:"tags"`
	Request               types.Object `tfsdk:"request"`
	AlertUserIDs          types.Set    `tfsdk:"alert_user_ids"`
	AlertContactGroupIDs  types.Set    `tfsdk:"alert_contact_group_ids"`
}

type requestModel struct {
	Method          types.String `tfsdk:"method"`
	Headers         types.Map    `tfsdk:"headers"`
	Body            types.String `tfsdk:"body"`
	FollowRedirects types.Bool   `tfsdk:"follow_redirects"`
	VerifySSL       types.Bool   `tfsdk:"verify_ssl"`
	UserAgent       types.String `tfsdk:"user_agent"`
}

var requestAttrTypes = map[string]attr.Type{
	"method":           types.StringType,
	"headers":          types.MapType{ElemType: types.StringType},
	"body":             types.StringType,
	"follow_redirects": types.BoolType,
	"verify_ssl":       types.BoolType,
	"user_agent":       types.StringType,
}

// toInput builds the API body from a plan. Unknown computed values are left out so the API keeps or chooses them.
func (m *monitorModel) toInput(ctx context.Context) (*client.MonitorInput, diag.Diagnostics) {
	var diags diag.Diagnostics
	in := &client.MonitorInput{
		Name:                m.Name.ValueString(),
		Type:                m.Type.ValueString(),
		URL:                 strPtr(m.URL),
		Path:                strPtr(m.Path),
		Port:                intPtr(m.Port),
		Interval:            intPtr(m.Interval),
		Timeout:             intPtr(m.Timeout),
		Keyword:             strPtr(m.Keyword),
		ExpectedStatus:      intPtr(m.ExpectedStatus),
		TCPPayload:          strPtr(m.TCPPayload),
		TCPExpect:           strPtr(m.TCPExpect),
		DNSRecordType:       strPtr(m.DNSRecordType),
		ExpectedDNS:         strPtr(m.ExpectedDNS),
		MinTLSDaysLeft:      intPtr(m.MinTLSDaysLeft),
		MinDomainDaysLeft:   intPtr(m.MinDomainDaysLeft),
		MailProtocol:        strPtr(m.MailProtocol),
		RequiredMtaStsMode:  strPtr(m.RequiredMtaStsMode),
		CheckBlacklists:     boolPtr(m.CheckBlacklists),
		LocationTargetType:  strPtr(m.LocationTargetType),
		LocationTargetValue: strPtr(m.LocationTargetValue),
		Locations:           []client.City{},
		Tags:                []string{},
	}
	if m.Type.ValueString() == "HEARTBEAT" {
		in.HeartbeatGraceSeconds = intPtr(m.HeartbeatGraceSeconds)
	}
	if !m.TrackConfigChanges.IsUnknown() && !m.TrackConfigChanges.IsNull() {
		v := m.TrackConfigChanges.ValueBool()
		in.TrackConfigChanges = &v
	}
	if !m.Paused.IsUnknown() && !m.Paused.IsNull() {
		s := client.StatusActive
		if m.Paused.ValueBool() {
			s = client.StatusSuspended
		}
		in.Status = &s
	}

	for _, id := range int64Set(ctx, m.Locations, &diags) {
		in.Locations = append(in.Locations, client.City{ID: id})
	}
	if !m.Tags.IsNull() && !m.Tags.IsUnknown() {
		var tags []string
		diags.Append(m.Tags.ElementsAs(ctx, &tags, false)...)
		sort.Strings(tags)
		in.Tags = append(in.Tags, tags...)
	}

	if !m.Request.IsNull() && !m.Request.IsUnknown() {
		var rm requestModel
		diags.Append(m.Request.As(ctx, &rm, basetypes.ObjectAsOptions{})...)
		req := &client.RequestDetails{
			Method:          strPtr(rm.Method),
			Body:            strPtr(rm.Body),
			FollowRedirects: boolPtr(rm.FollowRedirects),
			VerifySSL:       boolPtr(rm.VerifySSL),
			UserAgent:       strPtr(rm.UserAgent),
		}
		if !rm.Headers.IsNull() && !rm.Headers.IsUnknown() {
			req.Headers = map[string]string{}
			diags.Append(rm.Headers.ElementsAs(ctx, &req.Headers, false)...)
		}
		in.Request = req
	}

	if managed(m.AlertUserIDs) || managed(m.AlertContactGroupIDs) {
		// A set that is not managed here is left out, so UpdateMonitor's merge keeps its stored value.
		n := &client.NotificationInput{}
		if managed(m.AlertUserIDs) {
			users := []client.Ref{}
			for _, id := range int64Set(ctx, m.AlertUserIDs, &diags) {
				users = append(users, client.Ref{ID: id})
			}
			n.Users = &users
		}
		if managed(m.AlertContactGroupIDs) {
			groups := []client.Ref{}
			for _, id := range int64Set(ctx, m.AlertContactGroupIDs, &diags) {
				groups = append(groups, client.Ref{ID: id})
			}
			n.Groups = &groups
		}
		in.Notification = n
	}
	return in, diags
}

// apply copies an API monitor into the model. Where the API and the configuration may legitimately differ in form
// (masked secrets, empty versus absent lists, the implied CITY target) the configured form is kept. With importing
// set, the optional blocks Terraform otherwise leaves unmanaged (request, alert recipients) are read too.
func (m *monitorModel) apply(ctx context.Context, api *client.Monitor, importing bool) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(strconv.FormatInt(api.ID, 10))
	m.Name = types.StringValue(api.Name)
	m.Type = types.StringValue(api.Type)
	m.URL = strValue(api.URL)
	m.Path = strValue(api.Path)
	m.Port = intValue(api.Port)
	m.Interval = intValue(api.Interval)
	m.Timeout = intValue(api.Timeout)
	m.Keyword = strValue(api.Keyword)
	m.ExpectedStatus = intValue(api.ExpectedStatus)
	m.TCPPayload = strValue(api.TCPPayload)
	m.TCPExpect = strValue(api.TCPExpect)
	m.DNSRecordType = strValue(api.DNSRecordType)
	m.ExpectedDNS = strValue(api.ExpectedDNS)
	m.MinTLSDaysLeft = intValue(api.MinTLSDaysLeft)
	m.MinDomainDaysLeft = intValue(api.MinDomainDaysLeft)
	m.HeartbeatGraceSeconds = intValue(api.HeartbeatGraceSeconds)
	m.MailProtocol = strValue(api.MailProtocol)
	m.RequiredMtaStsMode = strValue(api.RequiredMtaStsMode)
	m.CheckBlacklists = types.BoolValue(api.CheckBlacklists != nil && *api.CheckBlacklists)
	m.TrackConfigChanges = types.BoolValue(api.TrackConfigChanges)
	m.Status = types.StringValue(api.Status)
	m.Paused = types.BoolValue(api.Status == client.StatusSuspended)

	ids := make([]int64, 0, len(api.Locations))
	for _, l := range api.Locations {
		ids = append(ids, l.ID)
	}
	m.Locations = int64SetValue(ids, m.Locations, &diags)

	// One city without a target type is stored as a CITY target with that city: that is the locations list again.
	if api.LocationTargetType != nil && *api.LocationTargetType == "CITY" && m.LocationTargetType.IsNull() && len(api.Locations) > 0 {
		m.LocationTargetType = types.StringNull()
		m.LocationTargetValue = types.StringNull()
	} else {
		m.LocationTargetType = strValue(api.LocationTargetType)
		m.LocationTargetValue = strValue(api.LocationTargetValue)
	}

	tags := append([]string(nil), api.Tags...)
	if len(tags) == 0 && (m.Tags.IsNull() || m.Tags.IsUnknown()) {
		m.Tags = types.SetNull(types.StringType)
	} else {
		v, d := types.SetValueFrom(ctx, types.StringType, tags)
		diags.Append(d...)
		m.Tags = v
	}

	if importing || (!m.Request.IsNull() && !m.Request.IsUnknown()) {
		m.Request = requestValue(ctx, api.Request, m.Request, &diags)
	}

	if importing || managed(m.AlertUserIDs) || managed(m.AlertContactGroupIDs) {
		var users, groups []int64
		if api.Notification != nil {
			for _, u := range api.Notification.Users {
				users = append(users, u.ID)
			}
			for _, g := range api.Notification.Groups {
				groups = append(groups, g.ID)
			}
		}
		if importing || managed(m.AlertUserIDs) {
			m.AlertUserIDs = int64SetValue(users, m.AlertUserIDs, &diags)
		}
		if importing || managed(m.AlertContactGroupIDs) {
			m.AlertContactGroupIDs = int64SetValue(groups, m.AlertContactGroupIDs, &diags)
		}
	}
	if !managed(m.AlertUserIDs) {
		m.AlertUserIDs = types.SetNull(types.Int64Type)
	}
	if !managed(m.AlertContactGroupIDs) {
		m.AlertContactGroupIDs = types.SetNull(types.Int64Type)
	}
	return diags
}

// requestValue converts the API request, keeping prior values where the API masks them.
func requestValue(ctx context.Context, api *client.RequestDetails, prior types.Object, diags *diag.Diagnostics) types.Object {
	if api == nil {
		return types.ObjectNull(requestAttrTypes)
	}
	var old requestModel
	hasOld := !prior.IsNull() && !prior.IsUnknown()
	if hasOld {
		diags.Append(prior.As(ctx, &old, basetypes.ObjectAsOptions{})...)
	}
	var oldHeaders map[string]string
	if hasOld && !old.Headers.IsNull() && !old.Headers.IsUnknown() {
		diags.Append(old.Headers.ElementsAs(ctx, &oldHeaders, false)...)
	}

	rm := requestModel{
		Method:          strValue(api.Method),
		Body:            strValue(api.Body),
		FollowRedirects: boolValue(api.FollowRedirects),
		VerifySSL:       boolValue(api.VerifySSL),
		UserAgent:       strValue(api.UserAgent),
		Headers:         types.MapNull(types.StringType),
	}
	if api.Method != nil {
		rm.Method = types.StringValue(strings.ToUpper(*api.Method))
	}
	if api.Body != nil && strings.Contains(*api.Body, client.Mask) && hasOld && !old.Body.IsNull() {
		rm.Body = old.Body
	}
	if len(api.Headers) > 0 {
		h := make(map[string]string, len(api.Headers))
		for k, v := range api.Headers {
			if v == client.Mask {
				if ov, ok := oldHeaders[k]; ok {
					v = ov
				}
			}
			h[k] = v
		}
		v, d := types.MapValueFrom(ctx, types.StringType, h)
		diags.Append(d...)
		rm.Headers = v
	} else if hasOld && !old.Headers.IsNull() && len(oldHeaders) == 0 {
		rm.Headers = old.Headers // configured as {}
	}

	obj, d := types.ObjectValueFrom(ctx, requestAttrTypes, rm)
	diags.Append(d...)
	return obj
}

func managed(s types.Set) bool { return !s.IsNull() && !s.IsUnknown() }

func int64Set(ctx context.Context, s types.Set, diags *diag.Diagnostics) []int64 {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []int64
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// int64SetValue is null for an empty list unless the prior value was a configured empty set.
func int64SetValue(ids []int64, prior types.Set, diags *diag.Diagnostics) types.Set {
	if len(ids) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.SetNull(types.Int64Type)
	}
	elems := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		elems = append(elems, types.Int64Value(id))
	}
	v, d := types.SetValue(types.Int64Type, elems)
	diags.Append(d...)
	return v
}

func strPtr(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

func intPtr(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := v.ValueInt64()
	return &i
}

func boolPtr(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}

// strValue treats an empty string like an absent one: the API stores either for "not set".
func strValue(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

func intValue(i *int64) types.Int64 {
	if i == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*i)
}

func boolValue(b *bool) types.Bool {
	if b == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*b)
}
