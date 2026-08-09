package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	BaseURL string
	Client  *http.Client
}

type Sample struct {
	Pod       string    `json:"pod"`
	TimeStamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		Client:  http.DefaultClient,
	}
}

func (c *Client) QueryRange(ctx context.Context, query string, start time.Time, end time.Time, step int) ([]Sample, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", formatTime(start))
	params.Set("end", formatTime(end))
	params.Set("step", strconv.Itoa(step))
	return c.doRequest(ctx, "/api/v1/query_range", params)
}

func (c *Client) QueryInstant(ctx context.Context, query string) ([]Sample, error) {
	params := url.Values{}
	params.Set("query", query)
	return c.doRequest(ctx, "/api/v1/query", params)
}

func formatTime(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

func (c *Client) doRequest(ctx context.Context, path string, params url.Values) ([]Sample, error) {
	u, err := url.Parse(c.BaseURL + path)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var promResp struct {
		Status string `json:"status"`
		Data   *struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]any           `json:"values"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data,omitempty"`
		Error string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&promResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if promResp.Status != "success" {
		return nil, fmt.Errorf("prometheus API error: %s", promResp.Error)
	}

	if promResp.Data == nil {
		return nil, nil
	}

	var samples []Sample
	for _, r := range promResp.Data.Result {
		pod := r.Metric["pod"]

		for _, v := range r.Values {
			if len(v) < 2 {
				continue
			}
			ts, err := toTime(v[0])
			if err != nil {
				continue
			}
			val, err := toFloat64(v[1])
			if err != nil {
				continue
			}
			samples = append(samples, Sample{Pod: pod, TimeStamp: ts, Value: val})
		}

		if len(r.Value) >= 2 {
			ts, err := toTime(r.Value[0])
			if err != nil {
				continue
			}
			val, err := toFloat64(r.Value[1])
			if err != nil {
				continue
			}
			samples = append(samples, Sample{Pod: pod, TimeStamp: ts, Value: val})
		}
	}

	return samples, nil
}

func toTime(v any) (time.Time, error) {
	sec, ok := v.(float64)
	if !ok {
		return time.Time{}, fmt.Errorf("expected float64 timestamp, got %T", v)
	}
	secInt := int64(sec)
	nsec := int64((sec - float64(secInt)) * 1e9)
	return time.Unix(secInt, nsec), nil
}

func toFloat64(v any) (float64, error) {
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("expected string value, got %T", v)
	}
	return strconv.ParseFloat(s, 64)
}
