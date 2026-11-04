package winbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
)

type Source interface {
	Type(context.Context) (daemon.ClipboardInfo, error)
	Image(context.Context) ([]byte, error)
}

type HTTPSource struct {
	BaseURL, TokenFile string
	Client             *http.Client
}

func NewHTTPSource(port int, tokenFile string) *HTTPSource {
	return &HTTPSource{fmt.Sprintf("http://127.0.0.1:%d", port), tokenFile, &http.Client{Timeout: 10 * time.Second}}
}

func (s *HTTPSource) request(ctx context.Context, endpoint string) (*http.Response, error) {
	data, err := os.ReadFile(s.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("read bridge token: %w", err)
	}
	token := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if token == "" {
		return nil, fmt.Errorf("bridge token is empty")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.BaseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "cc-clip/win-bridge")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("bridge %s: HTTP %d", endpoint, resp.StatusCode)
	}
	return resp, nil
}

func (s *HTTPSource) Type(ctx context.Context) (daemon.ClipboardInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := s.request(ctx, "/clipboard/type")
	if err != nil {
		return daemon.ClipboardInfo{}, err
	}
	defer resp.Body.Close()
	var info daemon.ClipboardInfo
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info)
	return info, err
}

func (s *HTTPSource) Image(ctx context.Context) ([]byte, error) {
	resp, err := s.request(ctx, "/clipboard/image")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > MaxEncodedBytes {
		return nil, fmt.Errorf("clipboard image exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxEncodedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxEncodedBytes {
		return nil, fmt.Errorf("clipboard image exceeds size limit or is empty")
	}
	return data, nil
}
