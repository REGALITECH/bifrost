package typesafe

import (
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

const (
	// creditsHeader carries the credits a hosted System One decision consumed,
	// as an exact decimal string.
	creditsHeader = "X-System-One-Credits"
	// hostedDomain is the hosted System One domain. Only it and its subdomains
	// bill in credits.
	hostedDomain = "system-one.dev"
)

// decimalPattern is a plain non-negative decimal: no sign, exponent, or
// fraction notation, so the value is exactly what the digits say.
var decimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// parseDecimal reads a plain non-negative decimal into an exact rational and
// returns the number of fractional digits.
func parseDecimal(s string) (*big.Rat, int, error) {
	if !decimalPattern.MatchString(s) {
		return nil, 0, fmt.Errorf("%q is not a non-negative decimal", s)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, 0, fmt.Errorf("%q is not a decimal", s)
	}
	scale := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		scale = len(s) - i - 1
	}
	return r, scale, nil
}

// creditPrice is a validated USD-per-credit price.
type creditPrice struct {
	rat   *big.Rat
	scale int
}

// parseCreditPrice validates the configured price. An empty string is unset.
func parseCreditPrice(s string) (*creditPrice, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	r, scale, err := parseDecimal(s)
	if err != nil {
		return nil, fmt.Errorf("typesafe_config.usd_per_credit: %w", err)
	}
	if r.Sign() <= 0 {
		return nil, fmt.Errorf("typesafe_config.usd_per_credit must be greater than zero")
	}
	return &creditPrice{rat: r, scale: scale}, nil
}

// cost multiplies credits (an exact decimal string) by the price in exact
// arithmetic. It returns the exact decimal USD string and its float64
// conversion, made only at the end.
func (p *creditPrice) cost(credits string) (string, float64, error) {
	c, cScale, err := parseDecimal(strings.TrimSpace(credits))
	if err != nil {
		return "", 0, err
	}
	total := new(big.Rat).Mul(c, p.rat)
	// A product of finite decimals is finite: its scale is the sum of scales.
	exact := total.FloatString(cScale + p.scale)
	if strings.Contains(exact, ".") {
		exact = strings.TrimRight(strings.TrimRight(exact, "0"), ".")
	}
	f, _ := total.Float64()
	return exact, f, nil
}

// isHostedEndpoint reports whether rawURL targets hosted System One: the
// parsed host is system-one.dev or a subdomain of it. A substring match never
// qualifies (evil-system-one.dev.example.com does not), and a base URL that
// proxies to system-one.dev under another host is not hosted.
func isHostedEndpoint(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return host == hostedDomain || strings.HasSuffix(host, "."+hostedDomain)
}

// replayedHeader marks a response System One served from a saved
// idempotent result; such a replay is not charged again and may omit credits.
const replayedHeader = "X-Idempotency-Replayed"

// requestIDHeader is System One's upstream request ID, logged so lost credits
// can be reconciled against the provider's records.
const requestIDHeader = "X-Typesafe-Request-Id"

// applyCredits prices a hosted decision from the credits header. A hosted
// response must carry the header unless it is an idempotent replay
// (X-Idempotency-Replayed: true), which keeps its token usage. A missing or
// malformed header otherwise fails the request: the decision was charged
// upstream, so guessing a price is not safe. Failures are logged with the
// upstream request ID and the raw header value for reconciliation.
func applyCredits(logger schemas.Logger, resp *fasthttp.Response, price *creditPrice, out *schemas.BifrostDecisionResponse) error {
	raw := string(resp.Header.Peek(creditsHeader))
	requestID := string(resp.Header.Peek(requestIDHeader))
	if raw == "" {
		if strings.EqualFold(strings.TrimSpace(string(resp.Header.Peek(replayedHeader))), "true") {
			return nil
		}
		logger.Error("[typesafe] hosted decision response missing %s: request_id=%q", creditsHeader, requestID)
		return fmt.Errorf("hosted System One response is missing the %s header; the decision cannot be priced", creditsHeader)
	}
	exact, f, err := price.cost(raw)
	if err != nil {
		logger.Error("[typesafe] hosted decision response has malformed %s: request_id=%q value=%q", creditsHeader, requestID, raw)
		return fmt.Errorf("invalid %s header: %w", creditsHeader, err)
	}
	if out.Usage == nil {
		out.Usage = &schemas.BifrostLLMUsage{}
	}
	out.Usage.Cost = &schemas.BifrostCost{RequestCost: f, TotalCost: f}
	out.Usage.CreditCostUSD = exact
	return nil
}
