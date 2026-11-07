//go:build !linux && !darwin && !freebsd

package accounting

import (
	"errors"
	"os"
)

func lockFile(string) (*os.File, error) { return nil, errors.New("native accounting requires Linux") }
