package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// testBrave returns a BraveSearch pointed at a local server. Every test below
// exercises the REAL Search method — only the endpoint changes — so a bug in
// Search cannot hide behind a test-only reimplementation.
func testBrave(endpoint string, timeout time.Duration) *BraveSearch {
	return &BraveSearch{apiKey: "test-key-123", timeout: timeout, endpoint: endpoint}
}

func TestBraveSearch_EmptyKey(t *testing.T) {
	if NewBraveSearch("") != nil {
		t.Fatal("expected nil BraveSearch for an empty key")
	}
	if NewBraveSearch("   \n") != nil {
		t.Fatal("expected nil BraveSearch for a whitespace-only key")
	}
}

// TrimSpace matters: an Infisical secret routinely carries a trailing newline,
// and "Bearer key\n" style breakage is exactly the bug that cost a release cycle
// on the Hermes key. Here it would land in the X-Subscription-Token header.
func TestBraveSearch_TrimsKey(t *testing.T) {
	b := NewBraveSearch("  abc123\n")
	if b == nil {
		t.Fatal("expected a client for a non-empty key")
	}
	if b.apiKey != "abc123" {
		t.Fatalf("expected the key to be trimmed, got %q", b.apiKey)
	}
}

func TestBraveSearch_EmptyQuery(t *testing.T) {
	b := testBrave("http://127.0.0.1:1", 5*time.Second)
	if got := b.Search(context.Background(), ""); !strings.Contains(got, "query vide") {
		t.Fatalf("expected an empty-query error, got: %s", got)
	}
}

func TestBraveSearch_Success(t *testing.T) {
	var gotToken, gotQuery, gotCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Subscription-Token")
		gotQuery = r.URL.Query().Get("q")
		gotCount = r.URL.Query().Get("count")

		var resp braveResponse
		resp.Query.Original = "activités ESL classe"
		resp.Web.Results = []braveWebResult{
			{Title: "Fun ESL Activities", URL: "https://example.com/esl", Description: "Great activities for ESL classes", Age: "2 days ago"},
			{Title: "Teaching English Games", URL: "https://example.com/games", Description: "Games for English learners"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	got := testBrave(srv.URL, 5*time.Second).Search(context.Background(), "activités ESL classe")

	if gotToken != "test-key-123" {
		t.Errorf("expected the key in X-Subscription-Token, got %q", gotToken)
	}
	// Accents must survive the query encoding: the teacher's subjects are French.
	if gotQuery != "activités ESL classe" {
		t.Errorf("expected the query to reach Brave verbatim, got %q", gotQuery)
	}
	if gotCount != "5" {
		t.Errorf("expected count=5, got %q", gotCount)
	}
	for _, want := range []string{
		"Fun ESL Activities", "https://example.com/esl",
		"Great activities for ESL classes", "2 days ago",
		"Teaching English Games",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected the result to contain %q, got:\n%s", want, got)
		}
	}
}

// Every failure mode must end with the same instruction, because that sentence
// is the whole fallback design: the agent keeps its own search ability and the
// teacher never sees a dead end.
func TestBraveSearch_FailuresTellTheAgentToSearchItself(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantHas string
	}{
		{"rate limited", http.StatusTooManyRequests, "", "quota dépassé"},
		{"unauthorized", http.StatusUnauthorized, "", "clé API invalide"},
		{"forbidden", http.StatusForbidden, "", "clé API invalide"},
		{"server error", http.StatusInternalServerError, "boom", "HTTP 500"},
		{"bad gateway", http.StatusBadGateway, "", "HTTP 502"},
		{"invalid json", http.StatusOK, "{not json", "réponse invalide"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got := testBrave(srv.URL, 5*time.Second).Search(context.Background(), "test")
			if !strings.Contains(got, tc.wantHas) {
				t.Fatalf("expected %q in the message, got: %s", tc.wantHas, got)
			}
			if !strings.Contains(got, "Cherche par toi-même") {
				t.Fatalf("expected the fallback instruction, got: %s", got)
			}
		})
	}
}

func TestBraveSearch_Timeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // outlive the client timeout without a fixed sleep
	}))
	defer srv.Close()
	defer close(release)

	got := testBrave(srv.URL, 50*time.Millisecond).Search(context.Background(), "slow query")
	if !strings.Contains(got, "timeout") {
		t.Fatalf("expected a timeout message, got: %s", got)
	}
	if !strings.Contains(got, "Cherche par toi-même") {
		t.Fatalf("expected the fallback instruction, got: %s", got)
	}
}

// A cancelled job must not leave a request hanging: the Search context is
// derived from the job context, so an aborted prompt cancels the search too.
func TestBraveSearch_HonoursCallerCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	got := testBrave(srv.URL, 10*time.Second).Search(ctx, "cancelled")
	if !strings.Contains(got, "Cherche par toi-même") {
		t.Fatalf("expected a graceful message on cancellation, got: %s", got)
	}
}

func TestBraveSearch_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var resp braveResponse
		resp.Web.Results = []braveWebResult{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	got := testBrave(srv.URL, 5*time.Second).Search(context.Background(), "xyzzy introuvable")
	if !strings.Contains(got, "Aucun résultat") {
		t.Fatalf("expected a no-results message, got: %s", got)
	}
	// No results is NOT a failure: telling the model to search on its own here
	// would make it retry a query Brave answered correctly with "nothing".
	if strings.Contains(got, "Cherche par toi-même") {
		t.Fatalf("empty results must not be reported as a Brave failure, got: %s", got)
	}
}

// braveMaxResults is a context budget, not a display preference: a gateway that
// returns 20 results must not put 20 of them into the prompt.
func TestBraveSearch_CapsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var resp braveResponse
		for i := 0; i < 20; i++ {
			resp.Web.Results = append(resp.Web.Results, braveWebResult{
				Title: "Result " + string(rune('A'+i)),
				URL:   "https://example.com/",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	got := testBrave(srv.URL, 5*time.Second).Search(context.Background(), "many")
	if n := strings.Count(got, "https://example.com/"); n != braveMaxResults {
		t.Fatalf("expected %d results, got %d:\n%s", braveMaxResults, n, got)
	}
}

// --- Integration: exactly ONE real Brave call, and only when a key is present ---

func TestBraveSearch_RealAPI(t *testing.T) {
	key := os.Getenv("BRAVE_API_KEY")
	if key == "" {
		t.Skip("BRAVE_API_KEY not set — skipping the single real Brave call")
	}
	b := NewBraveSearch(key)
	if b == nil {
		t.Fatal("NewBraveSearch returned nil for a non-empty key")
	}

	got := b.Search(context.Background(), "Shakespeare plays list")
	if strings.Contains(got, "Cherche par toi-même") {
		t.Fatalf("real Brave call failed: %s", got)
	}
	if !strings.Contains(got, "Résultats web") {
		t.Fatalf("expected the formatted header, got: %s", got)
	}
	t.Logf("real Brave result (%d chars):\n%s", len(got), got)
}
