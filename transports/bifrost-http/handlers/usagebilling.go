package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// stampUsageBillingNonce makes governance bill a retried out-of-band usage
// report once.
//
// Governance deduplicates billing on (BillingNonce, RequestID, attempt), and
// ConvertToBifrostContext mints a fresh BillingNonce for every HTTP request, so
// a report retried with the same x-request-id would be billed again. The usage
// endpoints' contract is that a retry reuses x-request-id, so for them the
// nonce is derived from who reports and that ID instead: sha256 of the virtual
// key ID governance resolved (else the virtual key the request presented),
// "\x00", and x-request-id. Scoping it to the key keeps what the random nonce
// protects: another caller that picks the same x-request-id still gets its own
// nonce and is billed.
//
// A request without x-request-id keeps its per-request nonce, so every such
// report is billed, as before.
//
// Call it after the pre-hooks, where governance resolves the key, and before
// the post-hooks, which read the nonce. BillingNonce is a reserved key; the
// write lands because only core's plugin pipeline blocks reserved writes, and
// these handlers call the plugins directly, outside it.
func stampUsageBillingNonce(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext) {
	requestID := string(ctx.Request.Header.Peek("x-request-id"))
	if strings.TrimSpace(requestID) == "" {
		return
	}
	owner, _ := bifrostCtx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID).(string)
	if owner == "" {
		owner, _ = bifrostCtx.Value(schemas.BifrostContextKeyVirtualKey).(string)
	}
	sum := sha256.Sum256([]byte(owner + "\x00" + requestID))
	bifrostCtx.SetValue(schemas.BifrostContextKeyBillingNonce, hex.EncodeToString(sum[:]))
}
