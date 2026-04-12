package api

import (
	"context"
	"io"
	"net/http"
)

const warmupResponseReadLimit = 1024

func issueWarmupRequest(ctx context.Context, client *http.Client, method, endpoint string, headers map[string]string) error {
	if client == nil || endpoint == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return err
	}
	for key, value := range headers {
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, warmupResponseReadLimit))
	return nil
}
