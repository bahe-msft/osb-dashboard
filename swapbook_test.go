//go:build swapbook

package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSwapbookHandler(t *testing.T) {
	handler, err := NewSwapbookHandler()
	if err != nil {
		t.Fatalf("NewSwapbookHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := http.Get(server.URL + "/_swapbook/manifest.json")
	if err != nil {
		t.Fatalf("GET manifest: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var manifest struct {
		Stories []struct {
			Name string `json:"name"`
		} `json:"stories"`
	}
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(manifest.Stories) != 6 {
		t.Fatalf("stories = %d, want 6", len(manifest.Stories))
	}

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/_swapbook/preview/pool-details/active%20sandboxes", want: "default-pool"},
		{path: "/_swapbook/preview/sandbox-overview/mixed%20states", want: "OpenSandbox"},
		{path: "/assets/styles.css", want: "OpenSandbox dashboard UI guidelines"},
	} {
		response, err := http.Get(server.URL + test.path)
		if err != nil {
			t.Fatalf("GET %s: %v", test.path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", test.path, readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d: %s", test.path, response.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(string(body), test.want) {
			t.Errorf("GET %s body does not contain %q", test.path, test.want)
		}
	}
}
