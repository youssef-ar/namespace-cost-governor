package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueryRange(t *testing.T) {
	var gotPath string
	var gotQuery, gotStart, gotEnd, gotStep string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		gotStart = r.URL.Query().Get("start")
		gotEnd = r.URL.Query().Get("end")
		gotStep = r.URL.Query().Get("step")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result": []interface{}{
					map[string]interface{}{
						"metric": map[string]string{"pod": "nginx-abc"},
						"values": []interface{}{
							[]interface{}{float64(1000), "42.5"},
							[]interface{}{float64(2000), "43.0"},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Client: srv.Client()}
	start := time.Unix(100, 0)
	end := time.Unix(200, 0)

	samples, err := c.QueryRange(context.Background(), "up", start, end, 15)
	if err != nil {
		t.Fatalf("QueryRange failed: %v", err)
	}

	if gotPath != "/api/v1/query_range" {
		t.Errorf("path = %q, want /api/v1/query_range", gotPath)
	}
	if gotQuery != "up" {
		t.Errorf("query = %q, want up", gotQuery)
	}
	if gotStart != "100" {
		t.Errorf("start = %q, want 100", gotStart)
	}
	if gotEnd != "200" {
		t.Errorf("end = %q, want 200", gotEnd)
	}
	if gotStep != "15" {
		t.Errorf("step = %q, want 15", gotStep)
	}

	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}

	if samples[0].Pod != "nginx-abc" {
		t.Errorf("samples[0].Pod = %q, want nginx-abc", samples[0].Pod)
	}
	if !samples[0].TimeStamp.Equal(time.Unix(1000, 0)) {
		t.Errorf("samples[0].TimeStamp = %v, want %v", samples[0].TimeStamp, time.Unix(1000, 0))
	}
	if samples[0].Value != 42.5 {
		t.Errorf("samples[0].Value = %f, want 42.5", samples[0].Value)
	}

	if samples[1].Pod != "nginx-abc" {
		t.Errorf("samples[1].Pod = %q, want nginx-abc", samples[1].Pod)
	}
	if !samples[1].TimeStamp.Equal(time.Unix(2000, 0)) {
		t.Errorf("samples[1].TimeStamp = %v, want %v", samples[1].TimeStamp, time.Unix(2000, 0))
	}
	if samples[1].Value != 43.0 {
		t.Errorf("samples[1].Value = %f, want 43.0", samples[1].Value)
	}
}

func TestQueryInstant(t *testing.T) {
	var gotPath string
	var gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result": []interface{}{
					map[string]interface{}{
						"metric": map[string]string{"pod": "redis-xyz"},
						"value":  []interface{}{float64(3000), "99.9"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Client: srv.Client()}

	samples, err := c.QueryInstant(context.Background(), "up")
	if err != nil {
		t.Fatalf("QueryInstant failed: %v", err)
	}

	if gotPath != "/api/v1/query" {
		t.Errorf("path = %q, want /api/v1/query", gotPath)
	}
	if gotQuery != "up" {
		t.Errorf("query = %q, want up", gotQuery)
	}

	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(samples))
	}

	if samples[0].Pod != "redis-xyz" {
		t.Errorf("samples[0].Pod = %q, want redis-xyz", samples[0].Pod)
	}
	if !samples[0].TimeStamp.Equal(time.Unix(3000, 0)) {
		t.Errorf("samples[0].TimeStamp = %v, want %v", samples[0].TimeStamp, time.Unix(3000, 0))
	}
	if samples[0].Value != 99.9 {
		t.Errorf("samples[0].Value = %f, want 99.9", samples[0].Value)
	}
}

func TestQueryRange_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "internal error")
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Client: srv.Client()}
	_, err := c.QueryRange(context.Background(), "up", time.Now(), time.Now(), 15)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQueryRange_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  "bad query",
		})
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Client: srv.Client()}
	_, err := c.QueryRange(context.Background(), "up", time.Now(), time.Now(), 15)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQueryInstant_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "internal error")
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Client: srv.Client()}
	_, err := c.QueryInstant(context.Background(), "up")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQueryInstant_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  "bad query",
		})
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Client: srv.Client()}
	_, err := c.QueryInstant(context.Background(), "up")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("http://prometheus:9090")
	if c.BaseURL != "http://prometheus:9090" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.Client != http.DefaultClient {
		t.Error("Client should be http.DefaultClient")
	}
}
