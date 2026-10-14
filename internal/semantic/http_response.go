package semantic

import (
	"fmt"
	"io"
	"net/http"
)

const DefaultMaxPageBytes int64 = 8 * 1024 * 1024
const defaultMaxModelBytes int64 = 8 * 1024 * 1024
const defaultMaxDeliveryBytes int64 = 1024 * 1024

func readBoundedResponse(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 || limit == 1<<63-1 {
		return nil, fmt.Errorf("invalid response size limit")
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("decoded response exceeds %d bytes", limit)
	}
	return data, nil
}

func noHTTPRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}
