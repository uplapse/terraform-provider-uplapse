// Package client is a small typed client for the parts of the Uplapse API the Terraform provider uses.
//
// Authentication is an organisation API key (Settings > API keys), sent in the X-API-Key header. The key's roles
// decide what it may do: reading needs any role, creating, changing and deleting monitors needs OWNER, DOMAIN_ADMIN
// or EDITOR.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the production API.
const DefaultEndpoint = "https://api.uplapse.com"

// Client talks to one Uplapse API endpoint with one API key. It is safe for concurrent use.
type Client struct {
	baseURL    *url.URL
	apiKey     string
	httpClient *http.Client
	userAgent  string
	// maxRetries bounds retries of rate-limited (429) requests and of GETs that met a gateway error.
	maxRetries int
	// sleep is replaced in tests so retries do not wait.
	sleep func(context.Context, time.Duration) error
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client (timeouts, transport).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithMaxRetries sets how often a rate-limited or gateway-failed request is retried.
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// New returns a client for endpoint (scheme and host, optionally a path prefix) authenticating with apiKey.
func New(endpoint, apiKey string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid endpoint %q: must start with https:// (or http:// for a local API)", endpoint)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: no host", endpoint)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("an API key is required")
	}
	c := &Client{
		baseURL:    u,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{Timeout: 60 * time.Second},
		userAgent:  "terraform-provider-uplapse",
		maxRetries: 3,
		sleep:      sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Endpoint returns the base URL without a trailing slash.
func (c *Client) Endpoint() string { return c.baseURL.String() }

// APIError is a non-2xx answer from the API. Message is the API's own explanation (plan limits, validation) when
// the body carried one.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	// Code is the API's machine-readable code when it sent one (e.g. "MONITOR_DOWN", "50020" for a plan limit).
	Code    string
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	hint := ""
	switch e.StatusCode {
	case http.StatusUnauthorized:
		hint = " (check the API key: it may be wrong, revoked or expired)"
	case http.StatusForbidden:
		hint = " (the API key's roles do not allow this; changing monitors needs OWNER, DOMAIN_ADMIN or EDITOR)"
	}
	if e.Code != "" && e.Code != strconv.Itoa(e.StatusCode) {
		return fmt.Sprintf("uplapse API %s %s: %d %s: %s%s", e.Method, e.Path, e.StatusCode, e.Code, msg, hint)
	}
	return fmt.Sprintf("uplapse API %s %s: %d: %s%s", e.Method, e.Path, e.StatusCode, msg, hint)
}

// IsNotFound reports whether err is an API 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// errorBody covers the API's error shapes: ErrorResponseDTO {code, status, message}, the plan-limit ErrorMessage
// {code, message}, MONITOR_DOWN {code: "MONITOR_DOWN", message}, and the rate limiter's {error}.
type errorBody struct {
	Code    json.RawMessage `json:"code"`
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
}

func parseAPIError(resp *http.Response, method, path string, body []byte) *APIError {
	e := &APIError{StatusCode: resp.StatusCode, Method: method, Path: path}
	var eb errorBody
	if len(bytes.TrimSpace(body)) > 0 && json.Unmarshal(body, &eb) == nil {
		e.Message = strings.TrimSpace(eb.Message)
		if e.Message == "" {
			e.Message = strings.TrimSpace(eb.Error)
		}
		if len(eb.Code) > 0 {
			var s string
			if json.Unmarshal(eb.Code, &s) == nil {
				e.Code = s
			} else {
				e.Code = strings.TrimSpace(string(eb.Code))
			}
		}
	} else if len(body) > 0 && !looksLikeHTML(body) {
		// A proxy or WAF answering in plain text: show a bounded excerpt.
		text := strings.TrimSpace(string(body))
		if len(text) > 300 {
			text = text[:300] + "..."
		}
		e.Message = text
	}
	if e.Message == "" && looksLikeHTML(body) && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound) {
		e.Message = "an HTML page answered instead of the API; the endpoint may be wrong or a firewall may block this path"
	}
	return e
}

func looksLikeHTML(b []byte) bool {
	t := bytes.TrimSpace(b)
	return bytes.HasPrefix(t, []byte("<")) || bytes.HasPrefix(bytes.ToLower(t), []byte("<!doctype"))
}

// do sends one request and decodes a 2xx JSON answer into out (when out is not nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		payload, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
	}
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}

	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return err
		}
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("uplapse API %s %s: %w", method, path, err)
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("uplapse API %s %s: reading response: %w", method, path, readErr)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil || len(bytes.TrimSpace(respBody)) == 0 {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("uplapse API %s %s: decoding response: %w", method, path, err)
			}
			return nil
		}

		if attempt < c.maxRetries && retryable(method, resp.StatusCode) {
			if err := c.sleep(ctx, retryDelay(resp, attempt)); err != nil {
				return err
			}
			continue
		}
		return parseAPIError(resp, method, path, respBody)
	}
}

// retryable: a 429 is refused before the request is handled, so any method may repeat it; gateway errors are
// repeated only for reads, because a write may have been applied.
func retryable(method string, status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if method == http.MethodGet {
		return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
	}
	return false
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if s := resp.Header.Get("Retry-After"); s != "" {
		if secs, err := strconv.Atoi(s); err == nil && secs >= 0 {
			if secs > 60 {
				secs = 60
			}
			return time.Duration(secs) * time.Second
		}
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
