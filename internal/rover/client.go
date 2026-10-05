package rover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	mu      sync.RWMutex
	base    string
	token   string
	http    *http.Client
	changed chan struct{}
}

func New(address, token string) *Client {
	c := &Client{token: token, changed: make(chan struct{}), http: &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: dialRover, ResponseHeaderTimeout: 2 * time.Second, DisableKeepAlives: true}}}
	c.SetAddress(address)
	return c
}

func (c *Client) SetAddress(address string) {
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	c.mu.Lock()
	next := strings.TrimRight(address, "/")
	if next != c.base {
		close(c.changed)
		c.changed = make(chan struct{})
		c.base = next
	}
	c.mu.Unlock()
}

func (c *Client) Changed() <-chan struct{} { c.mu.RLock(); defer c.mu.RUnlock(); return c.changed }

func (c *Client) Address() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.base
}

func (c *Client) do(ctx context.Context, method, path string, input any, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Address()+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("X-Rover-Token", c.token)
	}
	req.Header.Set("X-Rover-Controller", "hub")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("rover HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(output)
	}
	return nil
}

func (c *Client) Move(ctx context.Context, direction string) error {
	return c.do(ctx, http.MethodGet, "/api/move?direction="+url.QueryEscape(direction), nil, nil)
}

func (c *Client) Speed(ctx context.Context, speed int) error {
	return c.do(ctx, http.MethodGet, fmt.Sprintf("/api/speed?value=%d", speed), nil, nil)
}

func (c *Client) Light(ctx context.Context, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	return c.do(ctx, http.MethodGet, "/api/light?on="+value, nil, nil)
}

type NetworkStatus struct {
	Mode          string `json:"mode"`
	PreferredMode string `json:"preferred_mode"`
	SSID          string `json:"ssid"`
	IP            string `json:"ip"`
	APIP          string `json:"ap_ip"`
	STAIP         string `json:"sta_ip"`
	STAConfigured bool   `json:"sta_configured"`
	SavedSSID     string `json:"saved_ssid"`
	Phase         string `json:"phase"`
	LastError     string `json:"last_error"`
	Fallback      bool   `json:"fallback"`
}

type ScanStatus struct {
	Phase    string `json:"phase"`
	Networks []struct {
		SSID     string `json:"ssid"`
		RSSI     int    `json:"rssi"`
		Security string `json:"security"`
	} `json:"networks"`
	Error string `json:"error"`
}

func (c *Client) Network(ctx context.Context) (NetworkStatus, error) {
	var status NetworkStatus
	err := c.do(ctx, http.MethodGet, "/api/network", nil, &status)
	return status, err
}

func (c *Client) SetNetwork(ctx context.Context, mode, ssid, password string) error {
	payload := map[string]string{"mode": mode}
	if ssid != "" {
		payload["ssid"] = ssid
		payload["password"] = password
	}
	return c.do(ctx, http.MethodPost, "/api/network", payload, nil)
}

func (c *Client) Scan(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/wifi/scan", nil, nil)
}

func (c *Client) ScanStatus(ctx context.Context) (ScanStatus, error) {
	var status ScanStatus
	err := c.do(ctx, http.MethodGet, "/api/wifi/scan", nil, &status)
	return status, err
}

func (c *Client) OpenStream(ctx context.Context) (*http.Response, error) {
	base, err := url.Parse(c.Address())
	if err != nil {
		return nil, err
	}
	host := base.Hostname()
	base.Host = net.JoinHostPort(host, "81")
	base.Path = "/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: dialRover, ResponseHeaderTimeout: 3 * time.Second, DisableKeepAlives: true}
	client := &http.Client{Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("rover stream HTTP %d", resp.StatusCode)
	}
	return resp, nil
}
