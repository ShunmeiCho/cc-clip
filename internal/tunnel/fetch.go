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

var randReader io.Reader = rand.Reader

var (
	ErrNoImage      = errors.New("no image in clipboard")
	ErrTokenInvalid = errors.New("token invalid or expired")
)

const defaultUserAgent = "cc-clip/0.1"
const maxFetchImageSize = 20 * 1024 * 1024

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
	if resp.ContentLength > maxFetchImageSize {
		return "", fmt.Errorf("fetch image failed: response exceeds 20MB limit")
	}

	if err := os.MkdirAll(outDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}

	ext := "png"
	ct := resp.Header.Get("Content-Type")
	if ct == "image/jpeg" {
		ext = "jpg"
	}

	suffix, err := randomHexSuffix(4)
	if err != nil {
		return "", fmt.Errorf("failed to generate filename suffix: %w", err)
	}
	filename := fmt.Sprintf("%s-%s.%s", time.Now().Format("20060102-150405"), suffix, ext)
	outPath := filepath.Join(outDir, filename)

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create image file: %w", err)
	}

	n, err := io.Copy(f, io.LimitReader(resp.Body, maxFetchImageSize+1))
	if err != nil {
		f.Close()
		os.Remove(outPath)
		return "", fmt.Errorf("failed to write image file: %w", err)
	}
	if n > maxFetchImageSize {
		f.Close()
		os.Remove(outPath)
		return "", fmt.Errorf("fetch image failed: response exceeds 20MB limit")
	}

	// Flush to disk and surface any deferred write errors (e.g. ENOSPC) that
	// only become visible on Sync/Close. Returning the error lets the shim fall
	// back instead of emitting a truncated image.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(outPath)
		return "", fmt.Errorf("failed to sync image file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("failed to close image file: %w", err)
	}

	return outPath, nil
}

func randomHexSuffix(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func DefaultOutDir() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "claude-images")
	}
	return filepath.Join(os.TempDir(), "claude-images")
}
