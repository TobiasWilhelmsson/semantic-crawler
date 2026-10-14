package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type HTTPDeliveryClient struct {
	client           *http.Client
	MaxResponseBytes int64
}

func NewHTTPDeliveryClient(client *http.Client) *HTTPDeliveryClient {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	guarded := *client
	guarded.CheckRedirect = noHTTPRedirects
	return &HTTPDeliveryClient{client: &guarded}
}

func (client *HTTPDeliveryClient) Send(ctx context.Context, endpoint string, payload DeliveryPayload) (int, string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, "", fmt.Errorf("encode delivery payload: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return 0, "", fmt.Errorf("create delivery request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("deliver approved candidate: %w", err)
	}
	defer response.Body.Close()
	limit := client.MaxResponseBytes
	if limit == 0 {
		limit = defaultMaxDeliveryBytes
	}
	body, err := readBoundedResponse(response.Body, limit)
	if err != nil {
		return response.StatusCode, "", fmt.Errorf("read delivery response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, string(body), fmt.Errorf("delivery returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, string(body), nil
}
