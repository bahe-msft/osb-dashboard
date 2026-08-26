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
			Name     string `json:"name"`
			Variants []struct {
				Name     string `json:"name"`
				Controls []struct {
					Name string `json:"name"`
				} `json:"controls"`
			} `json:"variants"`
		} `json:"stories"`
	}
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(manifest.Stories) != 6 {
		t.Fatalf("stories = %d, want 6", len(manifest.Stories))
	}
	wantControls := map[string]int{"Sandbox overview": 5, "Pools": 4, "Pool details": 2, "Sandbox details": 2}
	for _, story := range manifest.Stories {
		if want, ok := wantControls[story.Name]; ok {
			if len(story.Variants) != 1 || story.Variants[0].Name != "states" || len(story.Variants[0].Controls) != want {
				t.Errorf("%s variants = %#v, want one states variant with %d controls", story.Name, story.Variants, want)
			}
		}
	}

	response, err = http.Get(server.URL + "/_swapbook/inspection.json")
	if err != nil {
		t.Fatalf("GET inspection spec: %v", err)
	}
	var inspection swapbookInspectionDocument
	if err := json.NewDecoder(response.Body).Decode(&inspection); err != nil {
		response.Body.Close()
		t.Fatalf("decode inspection spec: %v", err)
	}
	response.Body.Close()
	if inspection.Version != 1 || len(inspection.Viewports) != 2 || len(inspection.Cases) != 12 {
		t.Errorf("inspection spec version/viewports/cases = %d/%d/%d, want 1/2/12", inspection.Version, len(inspection.Viewports), len(inspection.Cases))
	}

	response, err = http.Get(server.URL + "/_swapbook/mocks/sandbox-details/states")
	if err != nil {
		t.Fatalf("GET sandbox-detail mocks: %v", err)
	}
	var mocks []struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(response.Body).Decode(&mocks); err != nil {
		response.Body.Close()
		t.Fatalf("decode sandbox-detail mocks: %v", err)
	}
	response.Body.Close()
	wantMock := "/dashboard/sandboxes/sandbox-pool-1/fragment?pool=default-pool"
	foundMock := false
	for _, mock := range mocks {
		foundMock = foundMock || mock.Path == wantMock
	}
	if !foundMock {
		t.Errorf("sandbox-detail mocks do not contain %q: %#v", wantMock, mocks)
	}

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/_swapbook/preview/pool-details/states", want: "default-pool"},
		{path: "/_swapbook/preview/sandbox-overview/states", want: "OpenSandbox"},
		{path: "/dashboard/overview?running=false&pending=false&paused=true&failed=true&error=true", want: "sandbox-paused"},
		{path: "/dashboard/pools?ready=false&scaling=true&atCapacity=true&degraded=true", want: "scaling-pool"},
		{path: "/dashboard/swapbook/pool-detail?state=degraded&activeSandboxes=true", want: "Degraded"},
		{path: "/dashboard/swapbook/sandbox-detail?state=paused&fromPool=true", want: "default-pool"},
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
