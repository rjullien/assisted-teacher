package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BraveSearch calls the Brave Web Search API and returns a formatted text
// summary suitable for injection into a tool result.
//
// Design choices:
//   - Single-flight: no parallelism against Brave — their free tier rate-limits
//     at 1 req/s, and even paid plans reject bursts.
//   - Graceful degradation: if the API is unreachable, over-quota, or returns a
//     non-2xx, the error tells the model to fall back to its own search ability
//     (Lya's gateway has search_fallback_providers, pi can be told to search
//     differently). The caller never aborts the job.
//   - Timeout: 8 s. Brave is typically <1 s; if it takes longer the teacher
//     should not wait forever.
type BraveSearch struct {
	apiKey  string
	timeout time.Duration
	// endpoint is the Brave Web Search URL. A field rather than a constant so
	// brave_test.go can point it at a local httptest server and exercise the
	// real Search code path — the alternative (a second copy of Search in the
	// test file) would test the copy, not the code that ships.
	endpoint string
}

const (
	braveSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"
	braveDefaultTimeout = 8 * time.Second
	braveMaxResults     = 5
)

// NewBraveSearch creates a client. Returns nil if apiKey is empty — callers
// must check for nil before offering the web_search tool.
func NewBraveSearch(apiKey string) *BraveSearch {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return nil
	}
	log.Printf("brave: search configured (keyLen=%d, keyFp=%s)", len(key), keyFingerprint(key))
	return &BraveSearch{
		apiKey:   key,
		timeout:  braveDefaultTimeout,
		endpoint: braveSearchEndpoint,
	}
}

// braveWebResult is the subset of a Brave web result we extract.
type braveWebResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Age         string `json:"age"`
}

// braveResponse is the top-level response from the Web Search API.
type braveResponse struct {
	Web struct {
		Results []braveWebResult `json:"results"`
	} `json:"web"`
	Query struct {
		Original string `json:"original"`
	} `json:"query"`
}

// Search performs a web search and returns a formatted text result.
// On any error, it returns a user-readable message — never an empty string.
func (b *BraveSearch) Search(ctx context.Context, query string) string {
	if query == "" {
		return "Erreur : paramètre query vide."
	}

	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	endpoint := b.endpoint
	if endpoint == "" {
		endpoint = braveSearchEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "Recherche Brave indisponible (endpoint invalide). Cherche par toi-même avec tes propres capacités de recherche."
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("count", fmt.Sprintf("%d", braveMaxResults))
	params.Set("search_lang", "fr")
	params.Set("text_decorations", "false")
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "Recherche Brave indisponible (erreur interne). Cherche par toi-même avec tes propres capacités de recherche."
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "Recherche Brave indisponible (timeout). Cherche par toi-même avec tes propres capacités de recherche."
		}
		return fmt.Sprintf("Recherche Brave indisponible (réseau : %v). Cherche par toi-même avec tes propres capacités de recherche.", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return "Recherche Brave indisponible (quota dépassé, 429). Cherche par toi-même avec tes propres capacités de recherche."
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		log.Printf("brave: auth rejected (HTTP %d)", resp.StatusCode)
		return "Recherche Brave indisponible (clé API invalide). Cherche par toi-même avec tes propres capacités de recherche."
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Printf("brave: HTTP %d body=%s", resp.StatusCode, truncateStr(string(raw), 200))
		return fmt.Sprintf("Recherche Brave indisponible (HTTP %d). Cherche par toi-même avec tes propres capacités de recherche.", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "Recherche Brave indisponible (lecture réponse). Cherche par toi-même avec tes propres capacités de recherche."
	}

	var result braveResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "Recherche Brave indisponible (réponse invalide). Cherche par toi-même avec tes propres capacités de recherche."
	}

	if len(result.Web.Results) == 0 {
		return fmt.Sprintf("Aucun résultat trouvé pour « %s ».", query)
	}

	// Format results as readable text for the model.
	var sb strings.Builder
	fmt.Fprintf(&sb, "Résultats web pour « %s » :\n\n", query)
	for i, r := range result.Web.Results {
		if i >= braveMaxResults {
			break
		}
		fmt.Fprintf(&sb, "%d. %s\n", i+1, r.Title)
		fmt.Fprintf(&sb, "   %s\n", r.URL)
		if r.Description != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Description)
		}
		if r.Age != "" {
			fmt.Fprintf(&sb, "   (%s)\n", r.Age)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
