package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrQdrantUnavailable = errors.New("qdrant unavailable")

type QdrantClient struct {
	BaseURL    string
	Collection string
	APIKey     string
	HTTPClient *http.Client
}

type QdrantPoint struct {
	ID      string
	Vector  []float64
	Payload map[string]any
}

type QdrantSearchResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}

func (c QdrantClient) Upsert(ctx context.Context, points []QdrantPoint) error {
	if len(points) == 0 {
		return nil
	}
	var payload struct {
		Points []QdrantPoint `json:"points"`
	}
	payload.Points = points
	_, err := c.do(ctx, http.MethodPut, "/points", payload)
	return err
}

func (c QdrantClient) Search(ctx context.Context, vector []float64, limit int) ([]QdrantSearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	var payload struct {
		Vector      []float64 `json:"vector"`
		Limit       int       `json:"limit"`
		WithPayload bool      `json:"with_payload"`
	}
	payload.Vector = vector
	payload.Limit = limit
	payload.WithPayload = true
	body, err := c.do(ctx, http.MethodPost, "/points/search", payload)
	if err != nil {
		return nil, err
	}
	var response struct {
		Result []QdrantSearchResult `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	return response.Result, nil
}

func (c QdrantClient) Health(ctx context.Context) error {
	_, err := c.doRaw(ctx, http.MethodGet, "/collections/"+c.Collection, nil)
	return err
}

func (c QdrantClient) do(ctx context.Context, method string, suffix string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return c.doRaw(ctx, method, "/collections/"+c.Collection+suffix, body)
}

func (c QdrantClient) doRaw(ctx context.Context, method string, path string, body []byte) ([]byte, error) {
	if c.BaseURL == "" || c.Collection == "" {
		return nil, ErrQdrantUnavailable
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimRight(c.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("api-key", c.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQdrantUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d", ErrQdrantUnavailable, resp.StatusCode)
	}
	var response bytes.Buffer
	if _, err := response.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	return response.Bytes(), nil
}
