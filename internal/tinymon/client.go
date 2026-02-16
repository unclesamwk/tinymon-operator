package tinymon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type Host struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Description string `json:"description,omitempty"`
	Topic       string `json:"topic,omitempty"`
	Enabled     int    `json:"enabled"`
}

type Check struct {
	HostAddress     string      `json:"host_address"`
	Type            string      `json:"type"`
	Config          interface{} `json:"config,omitempty"`
	IntervalSeconds int         `json:"interval_seconds,omitempty"`
	Enabled         int         `json:"enabled"`
}

type Result struct {
	HostAddress string  `json:"host_address"`
	CheckType   string  `json:"check_type"`
	Status      string  `json:"status"`
	Value       float64 `json:"value,omitempty"`
	Message     string  `json:"message,omitempty"`
}

type BulkRequest struct {
	Results []Result `json:"results"`
}

func (c *Client) do(method, path string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

func (c *Client) UpsertHost(host Host) error {
	_, code, err := c.do("POST", "/api/push/hosts", host)
	if err != nil {
		return err
	}
	if code != 200 && code != 201 {
		return fmt.Errorf("upsert host %s: unexpected status %d", host.Address, code)
	}
	return nil
}

func (c *Client) DeleteHost(address string) error {
	body := map[string]string{"address": address}
	_, code, err := c.do("DELETE", "/api/push/hosts", body)
	if err != nil {
		return err
	}
	if code != 200 && code != 404 {
		return fmt.Errorf("delete host %s: unexpected status %d", address, code)
	}
	return nil
}

func (c *Client) UpsertCheck(check Check) error {
	_, code, err := c.do("POST", "/api/push/checks", check)
	if err != nil {
		return err
	}
	if code != 200 && code != 201 {
		return fmt.Errorf("upsert check %s/%s: unexpected status %d", check.HostAddress, check.Type, code)
	}
	return nil
}

func (c *Client) DeleteCheck(hostAddress, checkType string) error {
	body := map[string]string{"host_address": hostAddress, "type": checkType}
	_, code, err := c.do("DELETE", "/api/push/checks", body)
	if err != nil {
		return err
	}
	if code != 200 && code != 404 {
		return fmt.Errorf("delete check %s/%s: unexpected status %d", hostAddress, checkType, code)
	}
	return nil
}

func (c *Client) PushResult(result Result) error {
	_, code, err := c.do("POST", "/api/push/results", result)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("push result %s/%s: unexpected status %d", result.HostAddress, result.CheckType, code)
	}
	return nil
}

func (c *Client) PushBulk(results []Result) error {
	req := BulkRequest{Results: results}
	_, code, err := c.do("POST", "/api/push/bulk", req)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("push bulk: unexpected status %d", code)
	}
	return nil
}
