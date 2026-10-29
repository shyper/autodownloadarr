package download

import "testing"

func TestPreferArticleName(t *testing.T) {
	for _, tc := range []struct {
		current, article string
		want             bool
	}{
		{"post001", "film.part01.rar", true},
		{"post001.dat", "film.rar", true},
		{"film.rar", "post001", false},
		{"film.rar", "film.rar", false},
		{"", "film.mkv", true},
		{"", "", false},
	} {
		if got := preferArticleName(tc.current, tc.article); got != tc.want {
			t.Errorf("preferArticleName(%q, %q) = %v, want %v", tc.current, tc.article, got, tc.want)
		}
	}
}
