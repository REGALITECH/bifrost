package typesafe

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

// Unit pricing belongs to the model catalog. The provider must preserve the
// endpoint's token usage without requiring a provider-specific price or turning
// an unrelated response header into a precomputed cost that bypasses overrides.
func TestDecisionLeavesPricingToCatalog(t *testing.T) {
	for _, host := range []string{"api.typesafe.ai", "api.system-one.dev"} {
		for _, header := range []string{"", "12.4", "invalid"} {
			t.Run(host+"/credits="+header, func(t *testing.T) {
				calls := 0
				fixture, _ := newTypesafeFixture(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
					calls++
					if header != "" {
						w.Header().Set("X-System-One-Credits", header)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(fixtureSuccessBody))
				})
				provider, err := NewTypesafeProvider(&schemas.ProviderConfig{
					NetworkConfig: schemas.NetworkConfig{BaseURL: "http://" + host, DefaultRequestTimeoutInSeconds: 10},
				}, noopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				addr := strings.TrimPrefix(fixture.URL, "http://")
				provider.client.Dial = func(string) (net.Conn, error) { return net.Dial("tcp", addr) }
				ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
				resp, bifrostErr := provider.Decision(ctx, fixtureKey(), decisionRequest(t, "fixture", map[string]TypesafeQuestion{
					"q": {Type: TypesafeQuestionTypeNoul, Instructions: "Evaluate this state."},
				}))
				if bifrostErr != nil {
					t.Fatalf("decision must not require a provider-specific price: %v", bifrostErr)
				}
				if calls != 1 || resp.Usage == nil || resp.Usage.PromptTokens != 3 || resp.Usage.Cost != nil {
					t.Fatalf("provider must preserve usage for catalog pricing: calls=%d usage=%+v", calls, resp.Usage)
				}
			})
		}
	}
}
