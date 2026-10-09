package accounting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type PushRequest struct {
	ClientRef   string `json:"client_ref"`
	Type        string `json:"type"`
	AmountPaise int64  `json:"amount_paise"`
	SourceID    string `json:"source_id"`
}

type Result struct {
	ExternalRef string
	Duplicate   bool
}

type PushError struct {
	Message    string
	Retryable  bool
	Timeout    bool
	StatusCode int
}

func (e *PushError) Error() string { return e.Message }

type Client struct {
	base string
	http *http.Client
}

func NewClient(base string, timeout time.Duration) *Client {
	return &Client{
		base: base,
		http: &http.Client{Timeout: timeout},
	}
}

type mockResponse struct {
	ExternalRef string `json:"external_ref"`
}

func (c *Client) Push(ctx context.Context, req PushRequest) (Result, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return Result{}, &PushError{Message: err.Error(), Retryable: false}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/vouchers", bytes.NewReader(payload))
	if err != nil {
		return Result{}, &PushError{Message: err.Error(), Retryable: false}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Result{}, &PushError{
			Message:   fmt.Sprintf("transport error: %v", err),
			Retryable: true,
			Timeout:   isTimeout(err),
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		var mr mockResponse
		if err := json.Unmarshal(body, &mr); err != nil || mr.ExternalRef == "" {
			return Result{}, &PushError{Message: "success response missing external_ref", Retryable: true, StatusCode: resp.StatusCode}
		}
		return Result{ExternalRef: mr.ExternalRef}, nil

	case resp.StatusCode == http.StatusConflict:
		var mr mockResponse
		if err := json.Unmarshal(body, &mr); err != nil || mr.ExternalRef == "" {
			return Result{}, &PushError{Message: "malformed 409 response without external_ref", Retryable: true, StatusCode: resp.StatusCode}
		}
		return Result{ExternalRef: mr.ExternalRef, Duplicate: true}, nil

	case resp.StatusCode >= 500:
		return Result{}, &PushError{Message: fmt.Sprintf("accounting server error: %d", resp.StatusCode), Retryable: true, StatusCode: resp.StatusCode}

	default:
		return Result{}, &PushError{Message: fmt.Sprintf("non-retryable accounting response: %d %s", resp.StatusCode, string(body)), Retryable: false, StatusCode: resp.StatusCode}
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
