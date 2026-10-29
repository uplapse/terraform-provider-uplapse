package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Monitor types the API accepts (uptime-api enums/MonitorType.java).
var MonitorTypes = []string{
	"HTTPS", "API", "KEYWORD", "PING", "PORT", "TCP", "SSH", "DNS", "TLS_CERT", "HEARTBEAT",
	"DOMAIN_EXPIRY", "MAIL_AUTH", "CERT_TRANSPARENCY", "JOURNEY", "BROWSER", "FORM",
}

// DNSRecordTypes are the record types a DNS monitor may query.
var DNSRecordTypes = []string{"A", "AAAA", "CNAME", "TXT", "MX", "NS", "CAA"}

// Monitor statuses.
const (
	StatusActive    = "ACTIVE"
	StatusSuspended = "SUSPENDED"
)

// Mask is what the API returns in place of a credential (a sensitive request header value, a secret in a body).
// Sending it back means "keep what is stored".
const Mask = "********"

// Monitor is a monitor as the API returns it (MonitorDTO). Fields the provider does not use are left out.
type Monitor struct {
	ID                    int64                `json:"id"`
	Name                  string               `json:"name"`
	Type                  string               `json:"type"`
	URL                   *string              `json:"url"`
	Path                  *string              `json:"path"`
	Port                  *int64               `json:"port"`
	Interval              *int64               `json:"interval"`
	Timeout               *int64               `json:"timeout"`
	Keyword               *string              `json:"keyword"`
	ExpectedStatus        *int64               `json:"expectedStatus"`
	TCPPayload            *string              `json:"tcpPayload"`
	TCPExpect             *string              `json:"tcpExpect"`
	DNSRecordType         *string              `json:"dnsRecordType"`
	ExpectedDNS           *string              `json:"expectedDns"`
	MinTLSDaysLeft        *int64               `json:"minTlsDaysLeft"`
	MinDomainDaysLeft     *int64               `json:"minDomainDaysLeft"`
	HeartbeatGraceSeconds *int64               `json:"heartbeatGraceSeconds"`
	TrackConfigChanges    bool                 `json:"trackConfigChanges"`
	Status                string               `json:"status"`
	StatusReason          *string              `json:"statusReason"`
	Locations             []City               `json:"locations"`
	LocationTargetType    *string              `json:"locationTargetType"`
	LocationTargetValue   *string              `json:"locationTargetValue"`
	Tags                  []string             `json:"tags"`
	Request               *RequestDetails      `json:"request"`
	Notification          *NotificationDetails `json:"notification"`
}

// City is a check location as a monitor carries it (CityDTO).
type City struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name,omitempty"`
	Country *Country `json:"country,omitempty"`
}

// Country of a City.
type Country struct {
	ID     int64  `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	ISO2   string `json:"iso2,omitempty"`
	Region string `json:"region,omitempty"`
}

// RequestDetails is the HTTP request an HTTP-family monitor sends (RequestDetailsDTO).
type RequestDetails struct {
	ID              *int64            `json:"id,omitempty"`
	Method          *string           `json:"method"`
	Headers         map[string]string `json:"headers"`
	Body            *string           `json:"body"`
	FollowRedirects *bool             `json:"followRedirects"`
	VerifySSL       *bool             `json:"verifySsl"`
	UserAgent       *string           `json:"userAgent"`
}

// NotificationDetails lists who is alerted: individual users and contact groups.
type NotificationDetails struct {
	ID     *int64 `json:"id,omitempty"`
	Users  []Ref  `json:"users"`
	Groups []Ref  `json:"groups"`
}

// Ref references another object by id.
type Ref struct {
	ID int64 `json:"id"`
}

// MonitorInput is what the provider sends on create and update.
//
// Fields without omitempty are always sent, so a nil value clears the stored one. Fields with omitempty are left
// as stored when nil: the provider uses that for values the API computes (interval, timeout, port, status).
// Request and Notification are only sent when the configuration manages them.
type MonitorInput struct {
	Name                  string             `json:"name"`
	Type                  string             `json:"type"`
	URL                   *string            `json:"url"`
	Path                  *string            `json:"path"`
	Port                  *int64             `json:"port,omitempty"`
	Interval              *int64             `json:"interval,omitempty"`
	Timeout               *int64             `json:"timeout,omitempty"`
	Keyword               *string            `json:"keyword"`
	ExpectedStatus        *int64             `json:"expectedStatus"`
	TCPPayload            *string            `json:"tcpPayload"`
	TCPExpect             *string            `json:"tcpExpect"`
	DNSRecordType         *string            `json:"dnsRecordType"`
	ExpectedDNS           *string            `json:"expectedDns"`
	MinTLSDaysLeft        *int64             `json:"minTlsDaysLeft"`
	MinDomainDaysLeft     *int64             `json:"minDomainDaysLeft"`
	HeartbeatGraceSeconds *int64             `json:"heartbeatGraceSeconds,omitempty"`
	TrackConfigChanges    *bool              `json:"trackConfigChanges,omitempty"`
	Status                *string            `json:"status,omitempty"`
	Locations             []City             `json:"locations"`
	LocationTargetType    *string            `json:"locationTargetType"`
	LocationTargetValue   *string            `json:"locationTargetValue"`
	Tags                  []string           `json:"tags"`
	Request               *RequestDetails    `json:"request,omitempty"`
	Notification          *NotificationInput `json:"notification,omitempty"`
}

// NotificationInput changes alert recipients. A nil list is left as stored; an empty one removes them all.
type NotificationInput struct {
	Users  *[]Ref `json:"users,omitempty"`
	Groups *[]Ref `json:"groups,omitempty"`
}

func monitorPath(id int64) string { return "/v1/monitor/" + strconv.FormatInt(id, 10) }

// GetMonitor reads one monitor. A monitor that does not exist (or belongs to another organisation) is a 404,
// see IsNotFound.
func (c *Client) GetMonitor(ctx context.Context, id int64) (*Monitor, error) {
	var m Monitor
	if err := c.do(ctx, http.MethodGet, monitorPath(id), nil, nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateMonitor creates a monitor (POST /v1/monitor/save).
func (c *Client) CreateMonitor(ctx context.Context, in *MonitorInput) (*Monitor, error) {
	if in == nil {
		return nil, fmt.Errorf("monitor input is required")
	}
	var m Monitor
	if err := c.do(ctx, http.MethodPost, "/v1/monitor/save", nil, in, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// readOnlyMonitorKeys are returned by GET but are not part of a save.
var readOnlyMonitorKeys = []string{
	"date", "updateDate", "statusReason", "checkHeaderHasValue", "uptimeSLA", "downtimes", "lastHeartbeatAt",
}

// UpdateMonitor changes a monitor (PUT /v1/monitor/{id}).
//
// The API replaces the monitor with the body it is sent, so a body carrying only the provider's fields would erase
// settings made in the web app that the provider does not model (alerting conditions, authentication, TLS
// assertions...). UpdateMonitor therefore reads the stored monitor, lays the input over it and sends the result:
// the same round trip the web app's editor makes. Nested request and notification objects are merged key by key so
// their row ids are kept, which the API requires.
func (c *Client) UpdateMonitor(ctx context.Context, id int64, in *MonitorInput) (*Monitor, error) {
	if in == nil {
		return nil, fmt.Errorf("monitor input is required")
	}
	var current map[string]any
	if err := c.do(ctx, http.MethodGet, monitorPath(id), nil, nil, &current); err != nil {
		return nil, err
	}
	if current == nil {
		current = map[string]any{}
	}
	overlay, err := toMap(in)
	if err != nil {
		return nil, err
	}
	for _, k := range readOnlyMonitorKeys {
		delete(current, k)
	}
	for k, v := range overlay {
		if k == "request" || k == "notification" {
			if cur, ok := current[k].(map[string]any); ok {
				if nv, ok := v.(map[string]any); ok {
					for nk, nvv := range nv {
						cur[nk] = nvv
					}
					current[k] = cur
					continue
				}
			}
		}
		current[k] = v
	}
	current["id"] = id

	var m Monitor
	if err := c.do(ctx, http.MethodPut, monitorPath(id), nil, current, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteMonitor deletes a monitor and its history. Deleting a monitor that is already gone is not an error.
func (c *Client) DeleteMonitor(ctx context.Context, id int64) error {
	err := c.do(ctx, http.MethodDelete, monitorPath(id), nil, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// HeartbeatConfig is the ping address of a HEARTBEAT monitor.
type HeartbeatConfig struct {
	MonitorID    int64  `json:"monitorId"`
	HeartbeatKey string `json:"heartbeatKey"`
	PingPath     string `json:"pingPath"`
	GraceSeconds *int64 `json:"graceSeconds"`
}

// GetHeartbeat returns the ping address of a HEARTBEAT monitor.
func (c *Client) GetHeartbeat(ctx context.Context, id int64) (*HeartbeatConfig, error) {
	var h HeartbeatConfig
	if err := c.do(ctx, http.MethodGet, monitorPath(id)+"/heartbeat", nil, nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// HeartbeatURL is the full address a job calls to report in.
func (c *Client) HeartbeatURL(h *HeartbeatConfig) string {
	if h == nil || h.PingPath == "" {
		return ""
	}
	return c.Endpoint() + "/" + strings.TrimLeft(h.PingPath, "/")
}

func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	return m, nil
}
