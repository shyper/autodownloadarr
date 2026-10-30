package movies

// RegisterDownloadQueue registers a persistent queue to wake when a shared slot
// becomes available. Callbacks run without the services or manager locks held.
func (s *Services) RegisterDownloadQueue(wake func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downloadQueues = append(s.downloadQueues, wake)
}

// AcquireDownload reserves a slot across both movie and episode downloads.
func (s *Services) AcquireDownload(limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.downloads >= max(limit, 1) {
		return false
	}
	s.downloads++
	return true
}

// ReleaseDownload wakes both queues after a download has fully finished.
func (s *Services) ReleaseDownload() {
	s.mu.Lock()
	s.downloads--
	queues := append([]func(){}, s.downloadQueues...)
	s.mu.Unlock()
	for _, wake := range queues {
		go wake()
	}
}
