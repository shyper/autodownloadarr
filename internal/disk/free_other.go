//go:build !unix

package disk

import "errors"

// Free is not available on this platform.
func Free(string) (uint64, error) {
	return 0, errors.ErrUnsupported
}
