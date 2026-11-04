//go:build !windows

package winbridge

import (
	"context"
	"fmt"
)

func Run(ctx context.Context, source Source, ready chan<- error) error {
	err := fmt.Errorf("native Windows clipboard bridge requires Windows")
	ready <- err
	return err
}

func Station() (map[string]any, error) { return nil, fmt.Errorf("requires Windows") }

func ReadFormat(format string) ([]byte, error) { return nil, fmt.Errorf("requires Windows") }
