//go:build unix

package disk

import "syscall"

// Free returns the bytes available to unprivileged users on the file system
// that contains path.
func Free(path string) (uint64, error) {
	if path == "" {
		return 0, nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
