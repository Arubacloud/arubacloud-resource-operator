package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubamt "github.com/Arubacloud/sdk-go/pkg/multitenant"

	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ---------------------------------------------------------------------------
// Fake CMP server
// ---------------------------------------------------------------------------
//
// fakeCMP is an httptest-backed stand-in for the Aruba CMP API. Controllers
// reach it through a real aruba.Client, so wrapper hydration (ID/State/tags/…)
// happens through the SDK exactly as it does in production — which is why the
// tests can no longer mock the client: the v1.0.4 wrappers cannot be populated
// with server-assigned fields outside the aruba package.
//
// The operator only ever LISTs resources (never Get-by-id), so every GET is a
// collection listing. Items are staged per collection, keyed by the last path
// segment (e.g. "projects", "blockStorages", "vpcs"). Create/Update/Delete
// responses are driven by the configurable status codes, letting the tests
// exercise the CMP error categories (semantic/transient/technical).
type fakeCMP struct {
	server *httptest.Server

	mu           sync.Mutex
	collections  map[string][]map[string]any
	getStatus    int // 0/200 = success; >=400 makes every list call fail
	postStatus   int
	putStatus    int
	deleteStatus int
	errKind      string // "" (no field errors → transient/technical), "validation" (semantic)
}

func newFakeCMP() *fakeCMP {
	f := &fakeCMP{
		collections:  map[string][]map[string]any{},
		postStatus:   http.StatusCreated,
		putStatus:    http.StatusOK,
		deleteStatus: http.StatusNoContent,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeCMP) close() { f.server.Close() }

// stage adds items to a collection identified by the last URL path segment.
func (f *fakeCMP) stage(collection string, items ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.collections[collection] = append(f.collections[collection], items...)
}

func (f *fakeCMP) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		if f.getStatus >= 400 {
			w.WriteHeader(f.getStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "list failed", "status": f.getStatus})
			return
		}
		seg := lastPathSegment(r.URL.Path)
		items := f.collections[seg]
		if items == nil {
			items = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"values": items, "total": len(items)})
	case http.MethodPost:
		f.writeCUD(w, f.postStatus)
	case http.MethodPut:
		f.writeCUD(w, f.putStatus)
	case http.MethodDelete:
		f.writeCUD(w, f.deleteStatus)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeCMP) writeCUD(w http.ResponseWriter, status int) {
	if status >= 400 {
		body := map[string]any{"title": "operation failed", "status": status}
		// A 4xx with a non-empty errors array is categorized Semantic by the
		// operator (validation); without it, Transient. 5xx is always Technical.
		if f.errKind == "validation" {
			body["errors"] = []map[string]any{{"field": "spec", "message": "invalid"}}
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	w.WriteHeader(status)
	// Success bodies are ignored by the operator's CMP actions, but the SDK
	// still parses 2xx bodies — emit a minimal valid resource envelope.
	_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"id": "cmp-generated", "name": "cmp"}})
}

func lastPathSegment(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ---------------------------------------------------------------------------
// CMP item builders
// ---------------------------------------------------------------------------

// cmpMeta builds a metadata block with the fields the operator reads
// (id, name, region). The project id is included so wrapper Ref extraction
// works for Update/Delete regardless of the List parent scope.
func cmpMeta(id, name string) map[string]any {
	return map[string]any{
		"id":       id,
		"name":     name,
		"location": map[string]any{"value": "ITBG-Bergamo"},
	}
}

// cmpItem is the generic staged item: metadata + status(state). Suitable for
// resources whose lifecycle the operator drives purely off ID/name/state.
func cmpItem(id, name, state string) map[string]any {
	return map[string]any{
		"metadata": cmpMeta(id, name),
		"status":   map[string]any{"state": state},
	}
}

// ---------------------------------------------------------------------------
// Reconciler wiring
// ---------------------------------------------------------------------------

// newTestReconciler builds a base Reconciler whose "test-tenant" client is a
// real aruba.Client pointed at the fake CMP server via a static token (no IDP
// round-trip). Controllers under test therefore exercise the genuine SDK
// request/response path.
func newTestReconciler(_ GinkgoTInterface, f *fakeCMP) *reconciler.Reconciler {
	client, err := aruba.NewClient(aruba.NewOptions().WithBaseURL(f.server.URL).WithToken("test-token"))
	Expect(err).To(Succeed())
	mt := arubamt.New()
	mt.Add("test-tenant", client)
	return reconciler.NewReconcilerForTest(k8sClient, k8sClient.Scheme(), mt)
}

func strPtr(s string) *string { return &s }

// findCondition is a test helper.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
