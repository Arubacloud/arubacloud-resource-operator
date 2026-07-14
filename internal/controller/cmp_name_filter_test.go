package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
)

func TestFilterByName(t *testing.T) {
	type item struct{ name string }
	items := []item{{"a"}, {"b"}, {"a"}, {"c"}}
	got := filterByName(items, "a", func(i item) string { return i.name })
	if len(got) != 2 {
		t.Fatalf("want 2 matches for %q, got %d", "a", len(got))
	}
	for _, g := range got {
		if g.name != "a" {
			t.Errorf("unexpected match %q", g.name)
		}
	}
	if n := len(filterByName(items, "missing", func(i item) string { return i.name })); n != 0 {
		t.Errorf("want 0 matches for missing name, got %d", n)
	}
	if n := len(filterByName([]item(nil), "a", func(i item) string { return i.name })); n != 0 {
		t.Errorf("want 0 matches for nil slice, got %d", n)
	}
}

func TestIsCMPNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"http 404", &aruba.HTTPError{StatusCode: http.StatusNotFound}, true},
		{"http 500", &aruba.HTTPError{StatusCode: http.StatusInternalServerError}, false},
		{"wrapped 404", fmt.Errorf("context: %w", &aruba.HTTPError{StatusCode: http.StatusNotFound}), true},
	}
	for _, tc := range tests {
		if got := isCMPNotFound(tc.err); got != tc.want {
			t.Errorf("%s: isCMPNotFound = %v, want %v", tc.name, got, tc.want)
		}
	}
}
