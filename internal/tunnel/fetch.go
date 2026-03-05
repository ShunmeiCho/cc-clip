package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
)

var (
	ErrNoImage      = errors.New("no image in clipboard")
	ErrTokenInvalid = errors.New("token invalid or expired")
)

const defaultUserAgent = "cc-clip/0.1"

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL, token string, fetchTimeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: fetchTimeout,
		},
	}
}

func (c *Client) doRequest(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", defaultUserAgent)
	return c.httpClient.Do(req)
}

func (c *Client) ClipboardType() (daemon.ClipboardInfo, error) {
	resp, err := c.doRequest("/clipboard/type")
	if err != nil {
		return daemon.ClipboardInfo{}, fmt.Errorf("failed to check clipboard type: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return daemon.ClipboardInfo{}, ErrTokenInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return daemon.ClipboardInfo{}, fmt.Errorf("clipboard type check failed: %s", resp.Status)
	}

	var info daemon.ClipboardInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return daemon.ClipboardInfo{}, fmt.Errorf("failed to decode clipboard type: %w", err)
	}
	return info, nil
}

func (c *Client) FetchImage(outDir string) (string, error) {
	resp, err := c.doRequest("/clipboard/image")
	if err != nil {
		return "", fmt.Errorf("failed to fetch clipboard image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return "", ErrNoImage
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrTokenInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch image failed: %s", resp.Status)
	}

	if err := os.MkdirAll(outDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}

	ext := "png"
	ct := resp.Header.Get("Content-Type")
	if ct == "image/jpeg" {
		ext = "jpg"
	}

	randBytes := make([]byte, 4)
	rand.Read(randBytes)
	filename := fmt.Sprintf("%s-%s.%s", time.Now().Format("20060102-150405"), hex.EncodeToString(randBytes), ext)
	outPath := filepath.Join(outDir, filename)

	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("failed to create image file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("failed to write image file: %w", err)
	}

	return outPath, nil
}

func DefaultOutDir() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "claude-images")
	}
	return filepath.Join(os.TempDir(), "claude-images")
}
